// Package seckill 定义 v0.2 单机秒杀的领域规则和应用服务边界。
//
// 这里故意不依赖 MySQL 或 go-zero：库存锁策略属于基础设施实现，HTTP 参数解析属于传输层。
// 面试中常问“领域层为什么不直接写 SQL”，核心原因是业务不变量应能脱离具体存储被单元测试。
package seckill

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"service_rpc/internal/order"
)

const (
	StatusEnabled  uint8 = 1
	StatusDisabled uint8 = 2
)

var (
	ErrInvalidArgument  = errors.New("invalid seckill argument")
	ErrActivityNotFound = errors.New("seckill activity not found")
	ErrItemNotFound     = errors.New("seckill item not found")
	ErrUnavailable      = errors.New("seckill unavailable")
	ErrOutOfStock       = errors.New("seckill item out of stock")
	ErrInventoryBusy    = errors.New("seckill inventory contention exceeded retry limit")
	ErrConflict         = errors.New("seckill conflict")
)

type Activity struct {
	ID        uint64
	Name      string
	StartAt   time.Time
	EndAt     time.Time
	Status    uint8
	CreatedAt time.Time
}

type Item struct {
	ID             uint64
	ActivityID     uint64
	SKUID          uint64
	InitialStock   int64
	AvailableStock int64
	Version        uint64
	CreatedAt      time.Time
}

type CreateActivityInput struct {
	Name    string
	StartAt time.Time
	EndAt   time.Time
}

type AddItemInput struct {
	ActivityID uint64
	SKUID      uint64
	Stock      int64
}

type PurchaseResult struct {
	Order    order.Order
	Replayed bool
}

// Repository 是当前单体内的明确持久化边界。
// Purchase 必须把扣库存、写订单和写抢购记录放在同一事务；Service 不持有 *sql.Tx，避免把数据库细节泄漏到领域层。
type Repository interface {
	CreateActivity(ctx context.Context, input CreateActivityInput) (Activity, error)
	AddItem(ctx context.Context, input AddItemInput) (Item, error)
	SetActivityStatus(ctx context.Context, activityID uint64, status uint8) error
	Purchase(ctx context.Context, userID, itemID uint64, orderNo string, now time.Time) (PurchaseResult, error)
}

type Service struct {
	repository Repository
	now        func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, now: time.Now}
}

func (s *Service) CreateActivity(ctx context.Context, input CreateActivityInput) (Activity, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.StartAt = input.StartAt.UTC()
	input.EndAt = input.EndAt.UTC()
	if input.Name == "" || input.StartAt.IsZero() || input.EndAt.IsZero() || !input.EndAt.After(input.StartAt) {
		return Activity{}, ErrInvalidArgument
	}
	return s.repository.CreateActivity(ctx, input)
}

func (s *Service) AddItem(ctx context.Context, input AddItemInput) (Item, error) {
	if input.ActivityID == 0 || input.SKUID == 0 || input.Stock <= 0 {
		return Item{}, ErrInvalidArgument
	}
	return s.repository.AddItem(ctx, input)
}

func (s *Service) SetActivityStatus(ctx context.Context, activityID uint64, status uint8) error {
	if activityID == 0 || (status != StatusEnabled && status != StatusDisabled) {
		return ErrInvalidArgument
	}
	return s.repository.SetActivityStatus(ctx, activityID, status)
}

func (s *Service) Purchase(ctx context.Context, userID, itemID uint64) (PurchaseResult, error) {
	if userID == 0 || itemID == 0 {
		return PurchaseResult{}, ErrInvalidArgument
	}
	orderNo, err := newOrderNo()
	if err != nil {
		return PurchaseResult{}, fmt.Errorf("generate seckill order number: %w", err)
	}

	// now 只读取一次，确保临界时间点不会出现“校验开始时间时尚未开始、校验结束时间时已经结束”的自相矛盾结果。
	return s.repository.Purchase(ctx, userID, itemID, orderNo, s.now().UTC())
}

func newOrderNo() (string, error) {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return fmt.Sprintf("S%d%s", time.Now().UTC().UnixMilli(), hex.EncodeToString(random)), nil
}
