package seckill

import (
	"context"
	"strings"
	"time"
)

type CreateInventoryItemInput struct {
	ActivityID uint64
	SKUID      uint64
	Stock      int64
}

type InventoryItem struct {
	ID             uint64
	ActivityID     uint64
	Snapshot       FrozenSKUSnapshot
	InitialStock   int64
	AvailableStock int64
	Version        uint64
	CreatedAt      time.Time
}

type ProductSnapshotReader interface {
	GetActiveSKUSnapshot(ctx context.Context, skuID uint64) (FrozenSKUSnapshot, error)
}

type InventoryItemRepository interface {
	CreateInventoryItem(ctx context.Context, input CreateInventoryItemInput, snapshot FrozenSKUSnapshot) (InventoryItem, error)
	ListActivityItems(ctx context.Context, activityID uint64) ([]InventoryItem, error)
}

func (s *InventoryItemService) ListActivityItems(ctx context.Context, activityID uint64) ([]InventoryItem, error) {
	if s == nil || s.repository == nil || activityID == 0 {
		return nil, ErrInvalidArgument
	}
	return s.repository.ListActivityItems(ctx, activityID)
}

type InventoryItemService struct {
	repository InventoryItemRepository
	products   ProductSnapshotReader
}

func NewInventoryItemService(repository InventoryItemRepository, products ProductSnapshotReader) *InventoryItemService {
	return &InventoryItemService{repository: repository, products: products}
}

func (s *InventoryItemService) CreateSeckillItem(ctx context.Context, input CreateInventoryItemInput) (InventoryItem, error) {
	if s == nil || s.repository == nil || s.products == nil || input.ActivityID == 0 || input.SKUID == 0 || input.Stock <= 0 {
		return InventoryItem{}, ErrInvalidArgument
	}
	// product-rpc 返回的是“此刻可用于新业务”的值对象；保存后 inventory 拥有该副本，
	// 之后商品改名/变价不会影响已接受的秒杀与同 order_no 重放。
	snapshot, err := s.products.GetActiveSKUSnapshot(ctx, input.SKUID)
	if err != nil {
		return InventoryItem{}, err
	}
	if snapshot.SKUID != input.SKUID || strings.TrimSpace(snapshot.ProductName) == "" || strings.TrimSpace(snapshot.SKUCode) == "" || strings.TrimSpace(snapshot.SKUName) == "" || snapshot.UnitPriceCent < 0 {
		return InventoryItem{}, ErrInvalidArgument
	}
	return s.repository.CreateInventoryItem(ctx, input, snapshot)
}
