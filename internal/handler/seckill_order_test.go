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

type handlerGate struct{ orderNo string }

func (g handlerGate) Reserve(context.Context, seckill.ReservationInput) (seckill.Reservation, error) {
	return seckill.Reservation{OrderNo: g.orderNo}, nil
}

type handlerReader struct{}

func (handlerReader) LoadPreheatSnapshot(context.Context, uint64) (seckill.PreheatSnapshot, error) {
	return seckill.PreheatSnapshot{}, nil
}

type handlerCache struct{}

func (handlerCache) PublishActivity(context.Context, seckill.PreheatSnapshot, time.Time) (seckill.PreheatResult, error) {
	return seckill.PreheatResult{}, nil
}
func (handlerCache) InvalidateItems(context.Context, []uint64) error { return nil }

type handlerStream struct {
	input        seckill.ReservationInput
	orderNo      string
	status       seckill.AsyncResultStatus
	order        order.Order
	findOrderErr error
	resultErr    error
}

func (s *handlerStream) ReserveAndEnqueue(_ context.Context, input seckill.ReservationInput) (seckill.Reservation, error) {
	s.input = input
	return seckill.Reservation{OrderNo: s.orderNo}, nil
}
func (s *handlerStream) FindStreamResult(context.Context, uint64, string) (seckill.AsyncResultStatus, error) {
	return s.status, s.resultErr
}
func (s *handlerStream) FindOrderOwned(context.Context, uint64, string) (order.Order, error) {
	return s.order, s.findOrderErr
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
		{name: "cache not ready", err: seckill.ErrCacheNotReady, status: http.StatusServiceUnavailable},
		{name: "redis unavailable", err: seckill.ErrAdmissionFailure, status: http.StatusServiceUnavailable},
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

func TestCreateSeckillOrderHandlerReturnsAcceptedOnlyAfterAtomicStreamEnqueue(t *testing.T) {
	repository := &seckillOrderRepository{}
	stream := &handlerStream{orderNo: "T1-9-0000000000000001"}
	service, err := seckill.NewServiceWithStreamAdmission(repository, handlerReader{}, handlerCache{}, handlerGate{orderNo: "unused"}, stream, stream, stream)
	if err != nil {
		t.Fatalf("NewServiceWithStreamAdmission() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/seckill/items/9/orders", nil)
	request = request.WithContext(context.WithValue(request.Context(), authenticatedUserIDKey{}, uint64(7)))
	request = pathvar.WithVars(request, map[string]string{"itemId": "9"})
	recorder := httptest.NewRecorder()
	createSeckillOrderHandler(service)(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response queuedSeckillOrderResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Data.OrderNo != stream.orderNo || response.Data.Status != "QUEUED" || stream.input.UserID != 7 {
		t.Fatalf("response=%+v stream_input=%+v", response, stream.input)
	}
}

func TestGetSeckillOrderResultHandlerAndOwnership404(t *testing.T) {
	repository := &seckillOrderRepository{}
	stream := &handlerStream{orderNo: "T1-9-0000000000000001", order: order.Order{ID: 21, OrderNo: "T1-9-0000000000000001", UserID: 7}}
	service, err := seckill.NewServiceWithStreamAdmission(repository, handlerReader{}, handlerCache{}, handlerGate{orderNo: "unused"}, stream, stream, stream)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/seckill/orders/S-result/result", nil)
	request = request.WithContext(context.WithValue(request.Context(), authenticatedUserIDKey{}, uint64(7)))
	request = pathvar.WithVars(request, map[string]string{"orderNo": "S-result"})
	recorder := httptest.NewRecorder()
	getSeckillOrderResultHandler(service)(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response seckillOrderResultResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil || response.Data.Status != "SUCCEEDED" || response.Data.Order == nil {
		t.Fatalf("response=%+v error=%v", response, err)
	}

	stream.findOrderErr = seckill.ErrAsyncResultNotFound
	stream.resultErr = seckill.ErrAsyncResultNotFound
	recorder = httptest.NewRecorder()
	getSeckillOrderResultHandler(service)(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
