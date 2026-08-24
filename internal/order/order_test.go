package order

import (
	"context"
	"errors"
	"testing"
)

type repository struct {
	calls  int
	inputs []ItemInput
}

func (r *repository) Create(_ context.Context, _ uint64, _ string, items []ItemInput) (Order, error) {
	r.calls++
	r.inputs = items
	return Order{ID: 1}, nil
}
func (r *repository) FindOwned(context.Context, uint64, uint64) (Order, error) {
	return Order{}, ErrOrderNotFound
}

func TestServiceCreateValidatesItems(t *testing.T) {
	r := &repository{}
	service := NewService(r)
	if _, err := service.Create(context.Background(), 1, []ItemInput{{SKUID: 2, Quantity: 3}}); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if r.calls != 1 {
		t.Fatalf("calls=%d", r.calls)
	}
	for _, items := range [][]ItemInput{{}, {{SKUID: 0, Quantity: 1}}, {{SKUID: 2, Quantity: 101}}, {{SKUID: 2, Quantity: 1}, {SKUID: 2, Quantity: 1}}} {
		if _, err := service.Create(context.Background(), 1, items); !errors.Is(err, ErrInvalidOrder) {
			t.Fatalf("items=%v error=%v", items, err)
		}
	}
}
