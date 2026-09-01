// Package rpcserver exposes notification ownership operations over notification.v1.
package rpcserver

import (
	"context"
	"errors"

	commonv1 "service_rpc/api/gen/common/v1"
	notificationv1 "service_rpc/api/gen/notification/v1"
	"service_rpc/internal/notification"
	platformrpc "service_rpc/internal/platform/rpc"

	"google.golang.org/grpc/codes"
)

type Server struct {
	notificationv1.UnimplementedNotificationServiceServer
	service *notification.Service
}

func New(service *notification.Service) *Server { return &Server{service: service} }

func (s *Server) ListNotifications(ctx context.Context, request *notificationv1.ListNotificationsRequest) (*notificationv1.ListNotificationsResponse, error) {
	page, pageSize := 1, 20
	if request.GetPage() != nil {
		page, pageSize = int(request.GetPage().GetPage()), int(request.GetPage().GetPageSize())
	}
	found, err := s.service.List(ctx, request.GetUserId(), page, pageSize)
	if err != nil {
		return nil, mapError(err)
	}
	items := make([]*notificationv1.Notification, 0, len(found.Items))
	for _, item := range found.Items {
		items = append(items, toProto(item))
	}
	return &notificationv1.ListNotificationsResponse{
		Notifications: items,
		Page:          &commonv1.PageResponse{Page: uint32(found.Page), PageSize: uint32(found.PageSize), Total: uint64(found.Total)},
	}, nil
}

func (s *Server) MarkRead(ctx context.Context, request *notificationv1.MarkReadRequest) (*notificationv1.MarkReadResponse, error) {
	if err := s.service.MarkRead(ctx, request.GetUserId(), request.GetNotificationId()); err != nil {
		return nil, mapError(err)
	}
	return &notificationv1.MarkReadResponse{}, nil
}

func toProto(value notification.Notification) *notificationv1.Notification {
	result := &notificationv1.Notification{
		Id: value.ID, UserId: value.UserID, BusinessType: value.BusinessType,
		Title: value.Title, Body: value.Body, OrderNo: value.OrderNo,
		CreatedAtMs: value.CreatedAt.UTC().UnixMilli(),
	}
	if value.ReadAt != nil {
		result.ReadAtMs = value.ReadAt.UTC().UnixMilli()
	}
	return result
}

func mapError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.Canceled, commonv1.ErrorCode_ERROR_CODE_TEMPORARILY_UNAVAILABLE, "request canceled", true, err))
	case errors.Is(err, context.DeadlineExceeded):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.DeadlineExceeded, commonv1.ErrorCode_ERROR_CODE_TEMPORARILY_UNAVAILABLE, "notification timeout", true, err))
	case errors.Is(err, notification.ErrInvalidArgument):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.InvalidArgument, commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid notification request", false, err))
	case errors.Is(err, notification.ErrNotificationNotFound):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.NotFound, commonv1.ErrorCode_ERROR_CODE_NOT_FOUND, "notification not found", false, err))
	default:
		return platformrpc.StatusError(err)
	}
}
