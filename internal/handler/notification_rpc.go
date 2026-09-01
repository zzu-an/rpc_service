package handler

import (
	"net/http"
	"time"

	"service_rpc/internal/notification"

	"github.com/zeromicro/go-zero/rest/httpx"
)

type notificationListRequest struct {
	Page     int `form:"page"`
	PageSize int `form:"page_size"`
}

type notificationPath struct {
	NotificationID uint64 `path:"notificationId"`
}

type notificationPayload struct {
	ID           uint64  `json:"id"`
	BusinessType string  `json:"business_type"`
	Title        string  `json:"title"`
	Body         string  `json:"body"`
	OrderNo      string  `json:"order_no"`
	CreatedAt    string  `json:"created_at"`
	ReadAt       *string `json:"read_at,omitempty"`
}

type notificationListResponse struct {
	Code string `json:"code"`
	Data struct {
		Items    []notificationPayload `json:"items"`
		Page     int                   `json:"page"`
		PageSize int                   `json:"page_size"`
		Total    int64                 `json:"total"`
	} `json:"data"`
}

type notificationReadResponse struct {
	Code string `json:"code"`
	Data struct {
		ID     uint64 `json:"id"`
		Status string `json:"status"`
	} `json:"data"`
}

func gatewayListNotifications(client GatewayNotificationClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := gatewayUserID(r)
		if !ok {
			writeError(r, w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		var request notificationListRequest
		if err := httpx.ParseForm(r, &request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid pagination")
			return
		}
		if request.Page <= 0 {
			request.Page = 1
		}
		if request.PageSize <= 0 {
			request.PageSize = 20
		}
		if request.PageSize > 100 {
			request.PageSize = 100
		}
		page, err := client.List(r.Context(), userID, request.Page, request.PageSize)
		if err != nil {
			writeRPCError(r, w, err, "invalid notification query")
			return
		}
		response := notificationListResponse{Code: "OK"}
		response.Data.Page, response.Data.PageSize, response.Data.Total = page.Page, page.PageSize, page.Total
		response.Data.Items = make([]notificationPayload, 0, len(page.Items))
		for _, item := range page.Items {
			response.Data.Items = append(response.Data.Items, toNotificationPayload(item))
		}
		httpx.OkJsonCtx(r.Context(), w, response)
	}
}

func gatewayMarkNotificationRead(client GatewayNotificationClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := gatewayUserID(r)
		if !ok {
			writeError(r, w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		var request notificationPath
		if err := httpx.ParsePath(r, &request); err != nil || request.NotificationID == 0 {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid notification ID")
			return
		}
		if err := client.MarkRead(r.Context(), userID, request.NotificationID); err != nil {
			writeRPCError(r, w, err, "invalid notification ID")
			return
		}
		response := notificationReadResponse{Code: "OK"}
		response.Data.ID, response.Data.Status = request.NotificationID, "READ"
		httpx.OkJsonCtx(r.Context(), w, response)
	}
}

func toNotificationPayload(value notification.Notification) notificationPayload {
	result := notificationPayload{
		ID: value.ID, BusinessType: value.BusinessType, Title: value.Title, Body: value.Body,
		OrderNo: value.OrderNo, CreatedAt: value.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if value.ReadAt != nil {
		readAt := value.ReadAt.UTC().Format(time.RFC3339Nano)
		result.ReadAt = &readAt
	}
	return result
}
