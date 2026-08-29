package handler

import (
	"errors"
	"net/http"

	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/httpx"

	"service_rpc/internal/auth"
	"service_rpc/internal/seckill"
)

type seckillItemPath struct {
	ItemID uint64 `path:"itemId"`
}

type seckillOrderResultPath struct {
	OrderNo string `path:"orderNo"`
}

type seckillOrderData struct {
	Order    orderPayload `json:"order"`
	Replayed bool         `json:"replayed"`
}

type seckillOrderResponse struct {
	Code      string           `json:"code"`
	Message   string           `json:"message"`
	Data      seckillOrderData `json:"data"`
	RequestID string           `json:"request_id"`
}

type queuedSeckillOrderData struct {
	OrderNo  string `json:"order_no"`
	Status   string `json:"status"`
	Replayed bool   `json:"replayed"`
}

type queuedSeckillOrderResponse struct {
	Code      string                 `json:"code"`
	Message   string                 `json:"message"`
	Data      queuedSeckillOrderData `json:"data"`
	RequestID string                 `json:"request_id"`
}

type seckillOrderResultData struct {
	OrderNo string        `json:"order_no"`
	Status  string        `json:"status"`
	Order   *orderPayload `json:"order,omitempty"`
}

type seckillOrderResultResponse struct {
	Code      string                 `json:"code"`
	Message   string                 `json:"message"`
	Data      seckillOrderResultData `json:"data"`
	RequestID string                 `json:"request_id"`
}

// RegisterSeckillOrderRoutes 只要求身份认证：用户只能为 Token 中的自己下单，user_id 不接受请求体输入。
// 这是防止越权的常见设计点——服务端可信身份必须来自认证上下文，不能相信客户端提交的用户 ID。
func RegisterSeckillOrderRoutes(server *rest.Server, tokens *auth.TokenManager, service *seckill.Service) {
	server.AddRoutes([]rest.Route{
		{Method: http.MethodPost, Path: "/v1/seckill/items/:itemId/orders", Handler: authenticate(tokens)(createSeckillOrderHandler(service))},
		{Method: http.MethodGet, Path: "/v1/seckill/orders/:orderNo/result", Handler: authenticate(tokens)(getSeckillOrderResultHandler(service))},
	})
}

func createSeckillOrderHandler(service *seckill.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := r.Context().Value(authenticatedUserIDKey{}).(uint64)
		if !ok || userID == 0 {
			writeError(r, w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		var request seckillItemPath
		if err := httpx.ParsePath(r, &request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid seckill item ID")
			return
		}
		if service.AsyncEnabled() {
			result, err := service.Enqueue(r.Context(), userID, request.ItemID)
			if err != nil {
				writeSeckillPurchaseError(r, w, err)
				return
			}
			// 202 只说明任务已经进入持久化 job 表。使用 OkJson 会错误返回 200，也容易让
			// 客户端把 QUEUED 当成订单成功，因此这里显式写 Accepted。
			httpx.WriteJsonCtx(r.Context(), w, http.StatusAccepted, queuedSeckillOrderResponse{
				Code: "OK", Data: queuedSeckillOrderData{OrderNo: result.OrderNo, Status: "QUEUED", Replayed: result.Replayed},
			})
			return
		}
		result, err := service.Purchase(r.Context(), userID, request.ItemID)
		if err != nil {
			writeSeckillPurchaseError(r, w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, seckillOrderResponse{
			Code: "OK",
			Data: seckillOrderData{Order: toOrderPayload(result.Order), Replayed: result.Replayed},
		})
	}
}

func getSeckillOrderResultHandler(service *seckill.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := r.Context().Value(authenticatedUserIDKey{}).(uint64)
		if !ok || userID == 0 {
			writeError(r, w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		var request seckillOrderResultPath
		if err := httpx.ParsePath(r, &request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid seckill order number")
			return
		}
		result, err := service.GetAsyncResult(r.Context(), userID, request.OrderNo)
		if err != nil {
			if errors.Is(err, seckill.ErrJobNotFound) {
				// 不存在与不属于当前用户统一 404，防止攻击者枚举全局 order_no。
				writeError(r, w, http.StatusNotFound, "SECKILL_ORDER_NOT_FOUND", "seckill order request not found")
				return
			}
			writeError(r, w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}
		data := seckillOrderResultData{OrderNo: result.OrderNo, Status: string(result.Status)}
		if result.Status == seckill.AsyncResultSucceeded {
			payload := toOrderPayload(result.Order)
			data.Order = &payload
		}
		httpx.OkJsonCtx(r.Context(), w, seckillOrderResultResponse{Code: "OK", Data: data})
	}
}

func writeSeckillPurchaseError(r *http.Request, w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, seckill.ErrInvalidArgument):
		writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid seckill purchase")
	case errors.Is(err, seckill.ErrItemNotFound):
		writeError(r, w, http.StatusNotFound, "SECKILL_ITEM_NOT_FOUND", "seckill item not found")
	case errors.Is(err, seckill.ErrUnavailable):
		writeError(r, w, http.StatusConflict, "SECKILL_UNAVAILABLE", "seckill is unavailable")
	case errors.Is(err, seckill.ErrOutOfStock):
		writeError(r, w, http.StatusConflict, "OUT_OF_STOCK", "seckill item is out of stock")
	case errors.Is(err, seckill.ErrInventoryBusy):
		// 乐观锁耗尽只表示当前竞争过高，不能伪装成售罄；客户端可在退避后用同一身份安全重试。
		writeError(r, w, http.StatusServiceUnavailable, "INVENTORY_BUSY", "inventory is busy, retry later")
	case errors.Is(err, seckill.ErrCacheNotReady):
		writeError(r, w, http.StatusServiceUnavailable, "SECKILL_CACHE_NOT_READY", "seckill cache is not ready")
	case errors.Is(err, seckill.ErrAdmissionFailure):
		writeError(r, w, http.StatusServiceUnavailable, "SECKILL_TEMPORARILY_UNAVAILABLE", "seckill service is temporarily unavailable")
	case errors.Is(err, seckill.ErrQueueUnavailable):
		writeError(r, w, http.StatusServiceUnavailable, "SECKILL_QUEUE_UNAVAILABLE", "seckill request could not be queued, retry later")
	default:
		writeError(r, w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	}
}
