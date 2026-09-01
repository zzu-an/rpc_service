// Package notification owns in-app notifications and Kafka consumption idempotency.
package notification

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"service_rpc/internal/order/events"
)

const BusinessTypeOrderCreated = "ORDER_CREATED"

var (
	ErrInvalidArgument      = errors.New("invalid notification argument")
	ErrNotificationNotFound = errors.New("notification not found")
)

type Notification struct {
	ID           uint64
	UserID       uint64
	BusinessType string
	Title        string
	Body         string
	OrderNo      string
	CreatedAt    time.Time
	ReadAt       *time.Time
}

type Page struct {
	Items    []Notification
	Page     int
	PageSize int
	Total    int64
}

type Repository interface {
	ConsumeOrderCreated(ctx context.Context, event events.OrderCreatedV1) (created bool, err error)
	ListOwned(ctx context.Context, userID uint64, page, pageSize int) (Page, error)
	MarkReadOwned(ctx context.Context, userID, notificationID uint64, readAt time.Time) error
}

type Service struct{ repository Repository }

func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, fmt.Errorf("notification repository is required")
	}
	return &Service{repository: repository}, nil
}

func (s *Service) ConsumeOrderCreated(ctx context.Context, event events.OrderCreatedV1) (bool, error) {
	if err := event.Validate(); err != nil {
		return false, err
	}
	return s.repository.ConsumeOrderCreated(ctx, event)
}

func (s *Service) List(ctx context.Context, userID uint64, page, pageSize int) (Page, error) {
	if userID == 0 || page < 1 || pageSize < 1 || pageSize > 100 {
		return Page{}, ErrInvalidArgument
	}
	return s.repository.ListOwned(ctx, userID, page, pageSize)
}

func (s *Service) MarkRead(ctx context.Context, userID, notificationID uint64) error {
	if userID == 0 || notificationID == 0 {
		return ErrInvalidArgument
	}
	return s.repository.MarkReadOwned(ctx, userID, notificationID, time.Now().UTC().Truncate(time.Millisecond))
}

func ContentFromOrder(event events.OrderCreatedV1) (title, body string) {
	// 通知只依赖事件中的最小事实，绝不能回查 orders 表。这样通知库故障或消费积压
	// 只影响自己的 lag，订单服务也无需向每一个新增下游开放数据库权限。
	return "订单创建成功", fmt.Sprintf("订单 %s 已创建，金额 %d 分。", strings.TrimSpace(event.OrderNo), event.TotalAmountCent)
}
