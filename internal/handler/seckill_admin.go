package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/httpx"

	"service_rpc/internal/auth"
	"service_rpc/internal/rbac"
	"service_rpc/internal/seckill"
)

type createSeckillActivityRequest struct {
	Name    string `json:"name"`
	StartAt string `json:"start_at"`
	EndAt   string `json:"end_at"`
}

type seckillActivityPath struct {
	ActivityID uint64 `path:"activityId"`
}

type addSeckillItemRequest struct {
	ActivityID uint64 `path:"activityId"`
	SKUID      uint64 `json:"sku_id"`
	Stock      int64  `json:"stock"`
}

type updateSeckillActivityStatusRequest struct {
	ActivityID uint64 `path:"activityId"`
	Status     uint8  `json:"status"`
}

type seckillActivityPayload struct {
	ID      uint64 `json:"id"`
	Name    string `json:"name"`
	StartAt string `json:"start_at"`
	EndAt   string `json:"end_at"`
	Status  uint8  `json:"status"`
}

type seckillItemPayload struct {
	ID             uint64 `json:"id"`
	ActivityID     uint64 `json:"activity_id"`
	SKUID          uint64 `json:"sku_id"`
	InitialStock   int64  `json:"initial_stock"`
	AvailableStock int64  `json:"available_stock"`
}

type seckillActivityResponse struct {
	Code      string                 `json:"code"`
	Message   string                 `json:"message"`
	Data      seckillActivityPayload `json:"data"`
	RequestID string                 `json:"request_id"`
}

type seckillItemResponse struct {
	Code      string             `json:"code"`
	Message   string             `json:"message"`
	Data      seckillItemPayload `json:"data"`
	RequestID string             `json:"request_id"`
}

// RegisterSeckillAdminRoutes 复用已有认证与实时 RBAC 校验，不增加新的中间件。
// 权限每次从 Repository 判断，管理员权限被撤销后无需等待 Token 过期，这是“认证”和“授权”分离的常见面试点。
func RegisterSeckillAdminRoutes(server *rest.Server, tokens *auth.TokenManager, rbacService *rbac.Service, service *seckill.Service) {
	protectWrite := func(next http.HandlerFunc) http.HandlerFunc {
		return authenticate(tokens)(requirePermission(rbacService, "seckill:write")(next))
	}
	server.AddRoutes([]rest.Route{
		{Method: http.MethodPost, Path: "/v1/admin/seckill/activities", Handler: protectWrite(createSeckillActivityHandler(service))},
		{Method: http.MethodPost, Path: "/v1/admin/seckill/activities/:activityId/items", Handler: protectWrite(addSeckillItemHandler(service))},
		{Method: http.MethodPut, Path: "/v1/admin/seckill/activities/:activityId/status", Handler: protectWrite(updateSeckillActivityStatusHandler(service))},
	})
}

func createSeckillActivityHandler(service *seckill.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request createSeckillActivityRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid seckill activity")
			return
		}
		startAt, startErr := time.Parse(time.RFC3339Nano, request.StartAt)
		endAt, endErr := time.Parse(time.RFC3339Nano, request.EndAt)
		if startErr != nil || endErr != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid seckill activity time")
			return
		}
		created, err := service.CreateActivity(r.Context(), seckill.CreateActivityInput{Name: request.Name, StartAt: startAt, EndAt: endAt})
		if err != nil {
			writeSeckillAdminError(r, w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, seckillActivityResponse{Code: "OK", Data: toSeckillActivityPayload(created)})
	}
}

func addSeckillItemHandler(service *seckill.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request addSeckillItemRequest
		if err := httpx.ParsePath(r, &request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid seckill activity ID")
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid seckill item")
			return
		}
		created, err := service.AddItem(r.Context(), seckill.AddItemInput{ActivityID: request.ActivityID, SKUID: request.SKUID, Stock: request.Stock})
		if err != nil {
			writeSeckillAdminError(r, w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, seckillItemResponse{Code: "OK", Data: toSeckillItemPayload(created)})
	}
}

func updateSeckillActivityStatusHandler(service *seckill.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request updateSeckillActivityStatusRequest
		if err := httpx.ParsePath(r, &request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid seckill activity ID")
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid seckill activity status")
			return
		}
		if err := service.SetActivityStatus(r.Context(), request.ActivityID, request.Status); err != nil {
			writeSeckillAdminError(r, w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, seckillActivityResponse{Code: "OK", Data: seckillActivityPayload{ID: request.ActivityID, Status: request.Status}})
	}
}

func writeSeckillAdminError(r *http.Request, w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, seckill.ErrInvalidArgument):
		writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid seckill configuration")
	case errors.Is(err, seckill.ErrActivityNotFound):
		writeError(r, w, http.StatusNotFound, "SECKILL_ACTIVITY_NOT_FOUND", "seckill activity not found")
	case errors.Is(err, seckill.ErrConflict):
		writeError(r, w, http.StatusConflict, "SECKILL_CONFLICT", "seckill configuration conflict")
	default:
		writeError(r, w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	}
}

func toSeckillActivityPayload(value seckill.Activity) seckillActivityPayload {
	return seckillActivityPayload{ID: value.ID, Name: value.Name, StartAt: value.StartAt.UTC().Format(time.RFC3339Nano), EndAt: value.EndAt.UTC().Format(time.RFC3339Nano), Status: value.Status}
}

func toSeckillItemPayload(value seckill.Item) seckillItemPayload {
	return seckillItemPayload{ID: value.ID, ActivityID: value.ActivityID, SKUID: value.SKUID, InitialStock: value.InitialStock, AvailableStock: value.AvailableStock}
}
