package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zeromicro/go-zero/rest/pathvar"

	"service_rpc/internal/order"
)

type orderRepository struct {
	createUserID uint64
	inputs       []order.ItemInput
}

func (r *orderRepository) Create(_ context.Context, userID uint64, _ string, inputs []order.ItemInput) (order.Order, error) {
	r.createUserID, r.inputs = userID, inputs
	return order.Order{ID: 5, OrderNo: "O-test", UserID: userID, Status: 1, TotalAmountCent: 2468, Items: []order.Item{{ID: 1, SKUID: inputs[0].SKUID, ProductName: "Product", SKUCode: "sku", SKUName: "Default", UnitPriceCent: 1234, Quantity: inputs[0].Quantity, SubtotalCent: 2468}}}, nil
}
func (r *orderRepository) FindOwned(_ context.Context, userID, orderID uint64) (order.Order, error) {
	if userID != 9 || orderID != 5 {
		return order.Order{}, order.ErrOrderNotFound
	}
	return order.Order{ID: 5, UserID: 9, TotalAmountCent: 2468, Items: []order.Item{}}, nil
}

func TestOrderHandlersIgnoreClientAmountsAndEnforceOwner(t *testing.T) {
	repository := &orderRepository{}
	service := order.NewService(repository)
	request := httptest.NewRequest(http.MethodPost, "/v1/orders", bytes.NewBufferString(`{"items":[{"sku_id":3,"quantity":2}],"total_amount_cent":1}`))
	request = request.WithContext(context.WithValue(request.Context(), authenticatedUserIDKey{}, uint64(9)))
	recorder := httptest.NewRecorder()
	createOrderHandler(service)(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response orderResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Data.TotalAmountCent != 2468 || repository.createUserID != 9 || len(repository.inputs) != 1 {
		t.Fatalf("response=%+v repository=%+v", response, repository)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/orders/5", nil)
	request = request.WithContext(context.WithValue(request.Context(), authenticatedUserIDKey{}, uint64(10)))
	request = pathvar.WithVars(request, map[string]string{"orderId": "5"})
	recorder = httptest.NewRecorder()
	getOrderHandler(service)(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("other user status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestOrderHandlersRequireAuthentication(t *testing.T) {
	repository := &orderRepository{}
	service := order.NewService(repository)

	request := httptest.NewRequest(http.MethodPost, "/v1/orders", bytes.NewBufferString(`{"items":[{"sku_id":3,"quantity":2}]}`))
	recorder := httptest.NewRecorder()
	createOrderHandler(service)(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/orders/5", nil)
	request = pathvar.WithVars(request, map[string]string{"orderId": "5"})
	recorder = httptest.NewRecorder()
	getOrderHandler(service)(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("get status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
