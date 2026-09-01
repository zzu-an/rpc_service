// Package order contains basic v0.1 order rules. Inventory is intentionally
// absent; concurrent stock correctness is a v0.2 problem.
package order

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidOrder  = errors.New("invalid order")
	ErrOrderNotFound = errors.New("order not found")
	ErrOrderConflict = errors.New("order idempotency payload conflict")
)

type ItemInput struct {
	SKUID         uint64
	ProductName   string
	SKUCode       string
	SKUName       string
	UnitPriceCent int64
	Quantity      int64
}
type Item struct {
	ID            uint64
	SKUID         uint64
	ProductName   string
	SKUCode       string
	SKUName       string
	UnitPriceCent int64
	Quantity      int64
	SubtotalCent  int64
}
type Order struct {
	ID              uint64
	OrderNo         string
	UserID          uint64
	Status          uint8
	TotalAmountCent int64
	Items           []Item
	CreatedAt       time.Time
}

type Repository interface {
	Create(ctx context.Context, userID uint64, orderNo string, items []ItemInput) (Order, error)
	FindOwned(ctx context.Context, userID, orderID uint64) (Order, error)
}

// IdempotentRepository 是 RPC 写路径使用的显式接口。order_no 是跨 inventory/order
// 本地事务传递的业务幂等键；Replayed 只表示读回同载荷原订单，不表示重新执行了 INSERT。
type IdempotentRepository interface {
	CreateIdempotent(ctx context.Context, userID uint64, orderNo string, items []ItemInput) (CreateResult, error)
	FindOwnedByOrderNo(ctx context.Context, userID uint64, orderNo string) (Order, error)
}

type RPCRepository interface {
	Repository
	IdempotentRepository
}

// ProductSnapshotReader 是普通订单进入 order repository 前的快照信任边界。
// 实现由 product-rpc adapter 提供；repository 不得为了校验价格重新 JOIN 商品表。
type ProductSnapshotReader interface {
	GetOrderItemSnapshot(ctx context.Context, skuID uint64) (ItemInput, error)
}

type CreateResult struct {
	Order    Order
	Replayed bool
}

type Service struct {
	repository Repository
	idempotent IdempotentRepository
	snapshots  ProductSnapshotReader
}

func NewService(repository Repository) *Service { return &Service{repository: repository} }

func NewRPCService(repository RPCRepository, snapshots ProductSnapshotReader) (*Service, error) {
	if repository == nil || snapshots == nil {
		return nil, fmt.Errorf("order repository and product snapshot reader are required")
	}
	return &Service{repository: repository, idempotent: repository, snapshots: snapshots}, nil
}

func (s *Service) Create(ctx context.Context, userID uint64, items []ItemInput) (Order, error) {
	if userID == 0 || len(items) == 0 {
		return Order{}, ErrInvalidOrder
	}
	seen := make(map[uint64]struct{}, len(items))
	for _, item := range items {
		if item.SKUID == 0 || item.Quantity < 1 || item.Quantity > 100 {
			return Order{}, ErrInvalidOrder
		}
		if _, exists := seen[item.SKUID]; exists {
			return Order{}, ErrInvalidOrder
		}
		seen[item.SKUID] = struct{}{}
	}
	if s.snapshots != nil {
		resolved := make([]ItemInput, 0, len(items))
		for _, requested := range items {
			snapshot, err := s.snapshots.GetOrderItemSnapshot(ctx, requested.SKUID)
			if err != nil {
				return Order{}, err
			}
			// 数量只能来自本次用户请求；名称和价格只能来自 product-rpc。
			// 不允许 adapter 返回另一个 sku_id，防止错误缓存或契约映射产生串货。
			if snapshot.SKUID != requested.SKUID {
				return Order{}, ErrInvalidOrder
			}
			snapshot.Quantity = requested.Quantity
			resolved = append(resolved, snapshot)
		}
		items = resolved
	}
	orderNo, err := newOrderNo()
	if err != nil {
		return Order{}, fmt.Errorf("generate order number: %w", err)
	}
	return s.repository.Create(ctx, userID, orderNo, items)
}

func (s *Service) CreateSeckill(ctx context.Context, userID uint64, orderNo string, item ItemInput) (CreateResult, error) {
	orderNo = strings.TrimSpace(orderNo)
	if s == nil || s.idempotent == nil || userID == 0 || orderNo == "" || item.SKUID == 0 || item.Quantity != 1 ||
		strings.TrimSpace(item.ProductName) == "" || strings.TrimSpace(item.SKUCode) == "" || strings.TrimSpace(item.SKUName) == "" || item.UnitPriceCent < 0 {
		return CreateResult{}, ErrInvalidOrder
	}
	// 秒杀快照来自 inventory reservation，不能在积压后重新查 product-rpc：商品可能已改名/变价，
	// 同一 order_no 会因此变成冲突载荷。稳定 key + 冻结值让提交未知可安全恢复。
	return s.idempotent.CreateIdempotent(ctx, userID, orderNo, []ItemInput{item})
}

func (s *Service) FindSeckill(ctx context.Context, userID uint64, orderNo string) (Order, error) {
	orderNo = strings.TrimSpace(orderNo)
	if s == nil || s.idempotent == nil || userID == 0 || orderNo == "" {
		return Order{}, ErrOrderNotFound
	}
	return s.idempotent.FindOwnedByOrderNo(ctx, userID, orderNo)
}

func (s *Service) Get(ctx context.Context, userID, orderID uint64) (Order, error) {
	if userID == 0 || orderID == 0 {
		return Order{}, ErrOrderNotFound
	}
	return s.repository.FindOwned(ctx, userID, orderID)
}

func newOrderNo() (string, error) {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return fmt.Sprintf("O%d%s", time.Now().UTC().UnixMilli(), hex.EncodeToString(random)), nil
}
