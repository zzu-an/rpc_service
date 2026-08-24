package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/httpx"

	"service_rpc/internal/auth"
	"service_rpc/internal/order"
)

type createOrderItemRequest struct {
	SKUID    uint64 `json:"sku_id"`
	Quantity int64  `json:"quantity"`
}

type createOrderRequest struct {
	Items []createOrderItemRequest `json:"items"`
}

type orderPath struct {
	OrderID uint64 `path:"orderId"`
}

type orderItemPayload struct {
	ID            uint64 `json:"id"`
	SKUID         uint64 `json:"sku_id"`
	ProductName   string `json:"product_name"`
	SKUCode       string `json:"sku_code"`
	SKUName       string `json:"sku_name"`
	UnitPriceCent int64  `json:"unit_price_cent"`
	Quantity      int64  `json:"quantity"`
	SubtotalCent  int64  `json:"subtotal_cent"`
}

type orderPayload struct {
	ID              uint64             `json:"id"`
	OrderNo         string             `json:"order_no"`
	Status          uint8              `json:"status"`
	TotalAmountCent int64              `json:"total_amount_cent"`
	Items           []orderItemPayload `json:"items"`
	CreatedAt       string             `json:"created_at"`
}

type orderResponse struct {
	Code      string       `json:"code"`
	Message   string       `json:"message"`
	Data      orderPayload `json:"data"`
	RequestID string       `json:"request_id"`
}

// RegisterOrderRoutes protects both order operations with identity only. An
// administrator does not gain access to another user's order through RBAC.
func RegisterOrderRoutes(server *rest.Server, tokens *auth.TokenManager, service *order.Service) {
	server.AddRoutes([]rest.Route{
		{Method: http.MethodPost, Path: "/v1/orders", Handler: authenticate(tokens)(createOrderHandler(service))},
		{Method: http.MethodGet, Path: "/v1/orders/:orderId", Handler: authenticate(tokens)(getOrderHandler(service))},
	})
}

func createOrderHandler(service *order.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := r.Context().Value(authenticatedUserIDKey{}).(uint64)
		if !ok || userID == 0 {
			writeError(r, w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		var request createOrderRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid order")
			return
		}
		items := make([]order.ItemInput, 0, len(request.Items))
		for _, item := range request.Items {
			items = append(items, order.ItemInput{SKUID: item.SKUID, Quantity: item.Quantity})
		}
		created, err := service.Create(r.Context(), userID, items)
		if err != nil {
			writeOrderError(r, w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, orderResponse{Code: "OK", Data: toOrderPayload(created)})
	}
}

func getOrderHandler(service *order.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := r.Context().Value(authenticatedUserIDKey{}).(uint64)
		if !ok || userID == 0 {
			writeError(r, w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		var request orderPath
		if err := httpx.ParsePath(r, &request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid order ID")
			return
		}
		found, err := service.Get(r.Context(), userID, request.OrderID)
		if err != nil {
			writeOrderError(r, w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, orderResponse{Code: "OK", Data: toOrderPayload(found)})
	}
}

func writeOrderError(r *http.Request, w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, order.ErrInvalidOrder):
		writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid order")
	case errors.Is(err, order.ErrOrderNotFound):
		writeError(r, w, http.StatusNotFound, "ORDER_NOT_FOUND", "order not found")
	default:
		writeError(r, w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	}
}

func toOrderPayload(value order.Order) orderPayload {
	items := make([]orderItemPayload, 0, len(value.Items))
	for _, item := range value.Items {
		items = append(items, orderItemPayload{ID: item.ID, SKUID: item.SKUID, ProductName: item.ProductName, SKUCode: item.SKUCode, SKUName: item.SKUName, UnitPriceCent: item.UnitPriceCent, Quantity: item.Quantity, SubtotalCent: item.SubtotalCent})
	}
	createdAt := ""
	if !value.CreatedAt.IsZero() {
		createdAt = value.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	return orderPayload{ID: value.ID, OrderNo: value.OrderNo, Status: value.Status, TotalAmountCent: value.TotalAmountCent, Items: items, CreatedAt: createdAt}
}
