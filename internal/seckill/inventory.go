package seckill

import (
	"context"
	"strings"
	"time"
)

// FrozenSKUSnapshot 是 inventory 在创建秒杀 item 时持有的值副本。预留和后续订单都使用它，
// 不在异步积压后重新查询 product-rpc，避免同一 order_no 因变价形成不同载荷。
type FrozenSKUSnapshot struct {
	SKUID         uint64
	ProductName   string
	SKUCode       string
	SKUName       string
	UnitPriceCent int64
}

type InventoryReservationInput struct {
	OrderNo    string
	ActivityID uint64
	ItemID     uint64
	UserID     uint64
	ReservedAt time.Time
}

type InventoryReservation struct {
	ID         uint64
	OrderNo    string
	ActivityID uint64
	ItemID     uint64
	UserID     uint64
	Snapshot   FrozenSKUSnapshot
	ReservedAt time.Time
	Replayed   bool
}

type InventoryReservationRepository interface {
	ReserveSeckillStock(ctx context.Context, input InventoryReservationInput) (InventoryReservation, error)
}

type InventoryService struct {
	repository InventoryReservationRepository
}

func NewInventoryService(repository InventoryReservationRepository) *InventoryService {
	return &InventoryService{repository: repository}
}

func (s *InventoryService) ReserveSeckillStock(ctx context.Context, input InventoryReservationInput) (InventoryReservation, error) {
	input.OrderNo = strings.TrimSpace(input.OrderNo)
	if s == nil || s.repository == nil || input.OrderNo == "" || input.ActivityID == 0 || input.ItemID == 0 || input.UserID == 0 || input.ReservedAt.IsZero() {
		return InventoryReservation{}, ErrInvalidArgument
	}
	// Protobuf 契约是 Unix 毫秒，DB 是 DATETIME(3)。在领域边界统一精度，保证首次结果与重放读取逐字段一致。
	input.ReservedAt = input.ReservedAt.UTC().Truncate(time.Millisecond)
	return s.repository.ReserveSeckillStock(ctx, input)
}
