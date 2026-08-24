// Package order contains basic v0.1 order rules. Inventory is intentionally
// absent; concurrent stock correctness is a v0.2 problem.
package order

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidOrder  = errors.New("invalid order")
	ErrOrderNotFound = errors.New("order not found")
)

type ItemInput struct {
	SKUID    uint64
	Quantity int64
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

type Service struct{ repository Repository }

func NewService(repository Repository) *Service { return &Service{repository: repository} }

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
	orderNo, err := newOrderNo()
	if err != nil {
		return Order{}, fmt.Errorf("generate order number: %w", err)
	}
	return s.repository.Create(ctx, userID, orderNo, items)
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
