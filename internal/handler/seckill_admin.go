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

type seckillPreheatPayload struct {
	ActivityID       uint64 `json:"activity_id"`
	ItemCount        int    `json:"item_count"`
	EarliestExpireAt string `json:"earliest_expire_at"`
	LatestExpireAt   string `json:"latest_expire_at"`
}

type seckillPreheatResponse struct {
	Code      string                `json:"code"`
	Message   string                `json:"message"`
	Data      seckillPreheatPayload `json:"data"`
	RequestID string                `json:"request_id"`
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
		// 预热属于管理面而不是用户请求的 cache-miss 回调。若用户流量能触发预热，
		// 活动开始瞬间所有 miss 会一起查询 MySQL，形成经典缓存击穿。
		/*
			这个接口是管理员使用的 预热必须存在
			否则活动刚开始 直接缓存击穿了（所有请求同时涌入 缓存不命中 查mysql）
		*/
		{Method: http.MethodPost, Path: "/v1/admin/seckill/activities/:activityId/preheat", Handler: protectWrite(preheatSeckillActivityHandler(service))},
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

func preheatSeckillActivityHandler(service *seckill.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request seckillActivityPath
		if err := httpx.ParsePath(r, &request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid seckill activity ID")
			return
		}
		result, err := service.PreheatActivity(r.Context(), request.ActivityID)
		if err != nil {
			writeSeckillAdminError(r, w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, seckillPreheatResponse{
			Code: "OK",
			Data: seckillPreheatPayload{
				ActivityID:       result.ActivityID,
				ItemCount:        result.ItemCount,
				EarliestExpireAt: result.EarliestExpireAt.UTC().Format(time.RFC3339Nano),
				LatestExpireAt:   result.LatestExpireAt.UTC().Format(time.RFC3339Nano),
			},
		})
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
	case errors.Is(err, seckill.ErrNoItems):
		writeError(r, w, http.StatusConflict, "SECKILL_ACTIVITY_EMPTY", "seckill activity has no items")
	case errors.Is(err, seckill.ErrUnavailable):
		writeError(r, w, http.StatusConflict, "SECKILL_UNAVAILABLE", "seckill activity cannot be preheated")
	case errors.Is(err, seckill.ErrAdmissionFailure):
		// 基础设施细节只进入服务端日志（后续可观测阶段），HTTP 不返回 Redis 地址或错误文本。
		writeError(r, w, http.StatusServiceUnavailable, "SECKILL_TEMPORARILY_UNAVAILABLE", "seckill service is temporarily unavailable")
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
