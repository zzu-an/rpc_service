package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zeromicro/go-zero/rest/pathvar"

	"service_rpc/internal/order"
	"service_rpc/internal/seckill"
)

type seckillOrderRepository struct {
	result seckill.PurchaseResult
	err    error
	userID uint64
	itemID uint64
}

func (*seckillOrderRepository) CreateActivity(context.Context, seckill.CreateActivityInput) (seckill.Activity, error) {
	return seckill.Activity{}, nil
}
func (*seckillOrderRepository) AddItem(context.Context, seckill.AddItemInput) (seckill.Item, error) {
	return seckill.Item{}, nil
}
func (*seckillOrderRepository) SetActivityStatus(context.Context, uint64, uint8) error { return nil }
func (r *seckillOrderRepository) Purchase(_ context.Context, userID, itemID uint64, _ string, _ time.Time) (seckill.PurchaseResult, error) {
	r.userID, r.itemID = userID, itemID
	return r.result, r.err
}

func TestCreateSeckillOrderHandlerUsesAuthenticatedIdentity(t *testing.T) {
	repository := &seckillOrderRepository{result: seckill.PurchaseResult{
		Order:    order.Order{ID: 31, OrderNo: "S31", UserID: 7, TotalAmountCent: 1234, Items: []order.Item{{ID: 1, SKUID: 9, Quantity: 1, UnitPriceCent: 1234, SubtotalCent: 1234}}},
		Replayed: true,
	}}
	service := seckill.NewService(repository)
	request := httptest.NewRequest(http.MethodPost, "/v1/seckill/items/9/orders", nil)
	request = request.WithContext(context.WithValue(request.Context(), authenticatedUserIDKey{}, uint64(7)))
	request = pathvar.WithVars(request, map[string]string{"itemId": "9"})
	recorder := httptest.NewRecorder()
	createSeckillOrderHandler(service)(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if repository.userID != 7 || repository.itemID != 9 {
		t.Fatalf("repository identity user=%d item=%d", repository.userID, repository.itemID)
	}
	var response seckillOrderResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Data.Replayed || response.Data.Order.ID != 31 {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestCreateSeckillOrderHandlerErrorMapping(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
	}{
		{name: "unavailable", err: seckill.ErrUnavailable, status: http.StatusConflict},
		{name: "sold out", err: seckill.ErrOutOfStock, status: http.StatusConflict},
		{name: "busy", err: seckill.ErrInventoryBusy, status: http.StatusServiceUnavailable},
		{name: "internal", err: errors.New("db"), status: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := seckill.NewService(&seckillOrderRepository{err: test.err})
			request := httptest.NewRequest(http.MethodPost, "/v1/seckill/items/9/orders", nil)
			request = request.WithContext(context.WithValue(request.Context(), authenticatedUserIDKey{}, uint64(7)))
			request = pathvar.WithVars(request, map[string]string{"itemId": "9"})
			recorder := httptest.NewRecorder()
			createSeckillOrderHandler(service)(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), test.status)
			}
		})
	}
}

func TestCreateSeckillOrderHandlerRequiresIdentity(t *testing.T) {
	service := seckill.NewService(&seckillOrderRepository{})
	request := httptest.NewRequest(http.MethodPost, "/v1/seckill/items/9/orders", nil)
	recorder := httptest.NewRecorder()
	createSeckillOrderHandler(service)(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s, want 401", recorder.Code, recorder.Body.String())
	}
}
