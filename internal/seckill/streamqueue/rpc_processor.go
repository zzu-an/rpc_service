package streamqueue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"service_rpc/internal/seckill"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ErrRPCRequestRejected       = errors.New("orchestrator RPC request permanently rejected")
	ErrRPCDependencyUnavailable = errors.New("orchestrator RPC dependency unavailable")
	ErrReservedWithoutOrder     = errors.New("inventory reserved but order not confirmed")
)

type ReservedSnapshot struct {
	SKUID         uint64
	ProductName   string
	SKUCode       string
	SKUName       string
	UnitPriceCent int64
}

type ReservationResult struct {
	Snapshot ReservedSnapshot
	Replayed bool
}

type InventoryRPC interface {
	Reserve(ctx context.Context, userID, activityID, itemID uint64, orderNo string, reservedAt time.Time) (ReservationResult, error)
}

type OrderRPC interface {
	CreateSeckill(ctx context.Context, userID, activityID, itemID uint64, orderNo string, reservedAt time.Time, snapshot ReservedSnapshot) (replayed bool, err error)
}

type RPCProcessorConfig struct {
	TaskTimeout      time.Duration
	InventoryTimeout time.Duration
	OrderTimeout     time.Duration
}

func (c RPCProcessorConfig) Validate() error {
	if c.TaskTimeout <= 0 || c.InventoryTimeout <= 0 || c.OrderTimeout <= 0 || c.InventoryTimeout+c.OrderTimeout > c.TaskTimeout {
		return fmt.Errorf("orchestrator RPC budgets must be positive and fit within task timeout")
	}
	return nil
}

type RPCProcessor struct {
	inventory InventoryRPC
	orders    OrderRPC
	config    RPCProcessorConfig
}

func NewRPCProcessor(inventory InventoryRPC, orders OrderRPC, config RPCProcessorConfig) (*RPCProcessor, error) {
	if inventory == nil || orders == nil {
		return nil, fmt.Errorf("inventory-rpc and order-rpc clients are required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &RPCProcessor{inventory: inventory, orders: orders, config: config}, nil
}

func (p *RPCProcessor) ProcessStreamTask(ctx context.Context, userID, activityID, itemID uint64, orderNo string, reservedAt time.Time) (seckill.PurchaseResult, error) {
	if userID == 0 || activityID == 0 || itemID == 0 || orderNo == "" || reservedAt.IsZero() {
		return seckill.PurchaseResult{}, ErrRPCRequestRejected
	}
	taskContext, cancelTask := context.WithTimeout(ctx, p.config.TaskTimeout)
	defer cancelTask()
	inventoryContext, cancelInventory := context.WithTimeout(taskContext, p.config.InventoryTimeout)
	reservation, err := p.inventory.Reserve(inventoryContext, userID, activityID, itemID, orderNo, reservedAt)
	cancelInventory()
	if err != nil {
		return seckill.PurchaseResult{}, classifyRPCError(err)
	}

	orderContext, cancelOrder := context.WithTimeout(taskContext, p.config.OrderTimeout)
	_, err = p.orders.CreateSeckill(orderContext, userID, activityID, itemID, orderNo, reservedAt, reservation.Snapshot)
	cancelOrder()
	if err != nil {
		classified := classifyRPCError(err)
		// inventory 和 order 是两个服务本地事务，中间不存在原子提交。这里记录缺口并保留 PEL，
		// 不能盲目“加回库存”：order 可能已提交但响应丢失，补回会放出第二份资格。
		return seckill.PurchaseResult{}, fmt.Errorf("%w: %v", ErrReservedWithoutOrder, classified)
	}
	// Kafka 不在该关键路径：order-rpc 的本地 Outbox 会在订单提交后独立发布领域事件。
	return seckill.PurchaseResult{Replayed: reservation.Replayed}, nil
}

func classifyRPCError(err error) error {
	switch status.Code(err) {
	case codes.InvalidArgument, codes.NotFound, codes.AlreadyExists, codes.FailedPrecondition:
		return fmt.Errorf("%w: %v", ErrRPCRequestRejected, err)
	case codes.DeadlineExceeded:
		// gRPC status error 不一定满足 errors.Is(context.DeadlineExceeded)。显式归一化后，
		// DLQ 与指标才能得到稳定 error_code，而不是依赖 transport 的错误字符串。
		return fmt.Errorf("%w: %v", context.DeadlineExceeded, err)
	case codes.Unavailable:
		return fmt.Errorf("%w: %v", ErrRPCDependencyUnavailable, err)
	default:
		return err
	}
}

var _ TaskProcessor = (*RPCProcessor)(nil)
