// Package seckillrpc 把秒杀活动和 Redis 准入用例适配为 seckill.v1 RPC。
package seckillrpc

import (
	"context"
	"errors"
	"time"

	commonv1 "service_rpc/api/gen/common/v1"
	seckillv1 "service_rpc/api/gen/seckill/v1"
	platformrpc "service_rpc/internal/platform/rpc"
	"service_rpc/internal/seckill"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	seckillv1.UnimplementedSeckillServiceServer
	activities *seckill.ActivityService
	queue      *seckill.QueueService
}

func NewServer(activities *seckill.ActivityService, queue *seckill.QueueService) *Server {
	return &Server{activities: activities, queue: queue}
}

func (s *Server) CreateActivity(ctx context.Context, request *seckillv1.CreateActivityRequest) (*seckillv1.CreateActivityResponse, error) {
	created, err := s.activities.CreateActivity(ctx, seckill.CreateActivityInput{
		Name: request.GetName(), StartAt: time.UnixMilli(request.GetStartAtMs()).UTC(), EndAt: time.UnixMilli(request.GetEndAtMs()).UTC(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &seckillv1.CreateActivityResponse{Activity: toProtoActivity(created)}, nil
}

func (s *Server) UpdateActivityStatus(ctx context.Context, request *seckillv1.UpdateActivityStatusRequest) (*seckillv1.UpdateActivityStatusResponse, error) {
	if err := s.activities.SetActivityStatus(ctx, request.GetActivityId(), fromProtoActivityStatus(request.GetStatus())); err != nil {
		return nil, mapError(err)
	}
	return &seckillv1.UpdateActivityStatusResponse{}, nil
}

func (s *Server) PreheatActivity(ctx context.Context, request *seckillv1.PreheatActivityRequest) (*seckillv1.PreheatActivityResponse, error) {
	result, err := s.activities.PreheatActivity(ctx, request.GetActivityId())
	if err != nil {
		return nil, mapError(err)
	}
	return &seckillv1.PreheatActivityResponse{
		ActivityId: result.ActivityID, ItemCount: uint32(result.ItemCount),
		EarliestExpireAtMs: result.EarliestExpireAt.UTC().UnixMilli(), LatestExpireAtMs: result.LatestExpireAt.UTC().UnixMilli(),
	}, nil
}

func (s *Server) Enqueue(ctx context.Context, request *seckillv1.EnqueueRequest) (*seckillv1.EnqueueResponse, error) {
	submission, err := s.queue.Enqueue(ctx, request.GetUserId(), request.GetItemId())
	if err != nil {
		return nil, mapError(err)
	}
	return &seckillv1.EnqueueResponse{
		OrderNo: submission.OrderNo, Status: seckillv1.ResultStatus_RESULT_STATUS_QUEUED, Replayed: submission.Replayed,
	}, nil
}

func (s *Server) GetResult(ctx context.Context, request *seckillv1.GetResultRequest) (*seckillv1.GetResultResponse, error) {
	result, err := s.queue.GetProjectedResult(ctx, request.GetUserId(), request.GetOrderNo())
	if err != nil {
		return nil, mapError(err)
	}
	// order_id 在投影读取中有意为 0。gateway 必须先查 order-rpc，并只在订单不存在时
	// 回退这里；否则 Redis 延迟更新会把已经落库的订单错误显示成 QUEUED。
	return &seckillv1.GetResultResponse{OrderNo: result.OrderNo, Status: toProtoResultStatus(result.Status)}, nil
}

func (s *Server) ListStreamItemIDs(ctx context.Context, _ *seckillv1.ListStreamItemIDsRequest) (*seckillv1.ListStreamItemIDsResponse, error) {
	ids, err := s.activities.ListStreamItemIDs(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	return &seckillv1.ListStreamItemIDsResponse{ItemIds: ids}, nil
}

func toProtoActivity(value seckill.Activity) *seckillv1.Activity {
	return &seckillv1.Activity{
		Id: value.ID, Name: value.Name, StartAtMs: value.StartAt.UTC().UnixMilli(), EndAtMs: value.EndAt.UTC().UnixMilli(),
		Status: toProtoActivityStatus(value.Status), CreatedAtMs: value.CreatedAt.UTC().UnixMilli(),
	}
}

func toProtoActivityStatus(value uint8) seckillv1.ActivityStatus {
	switch value {
	case seckill.StatusEnabled:
		return seckillv1.ActivityStatus_ACTIVITY_STATUS_ENABLED
	case seckill.StatusDisabled:
		return seckillv1.ActivityStatus_ACTIVITY_STATUS_DISABLED
	default:
		return seckillv1.ActivityStatus_ACTIVITY_STATUS_UNSPECIFIED
	}
}

func fromProtoActivityStatus(value seckillv1.ActivityStatus) uint8 {
	switch value {
	case seckillv1.ActivityStatus_ACTIVITY_STATUS_ENABLED:
		return seckill.StatusEnabled
	case seckillv1.ActivityStatus_ACTIVITY_STATUS_DISABLED:
		return seckill.StatusDisabled
	default:
		return 0
	}
}

func toProtoResultStatus(value seckill.AsyncResultStatus) seckillv1.ResultStatus {
	switch value {
	case seckill.AsyncResultQueued:
		return seckillv1.ResultStatus_RESULT_STATUS_QUEUED
	case seckill.AsyncResultSucceeded:
		return seckillv1.ResultStatus_RESULT_STATUS_SUCCEEDED
	case seckill.AsyncResultFailed:
		return seckillv1.ResultStatus_RESULT_STATUS_FAILED
	default:
		return seckillv1.ResultStatus_RESULT_STATUS_UNSPECIFIED
	}
}

func mapError(err error) error {
	grpcCode := status.Code(err)
	if grpcCode == codes.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) {
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.DeadlineExceeded, commonv1.ErrorCode_ERROR_CODE_TEMPORARILY_UNAVAILABLE, "seckill dependency timeout", true, err))
	}
	if grpcCode == codes.Unavailable {
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.Unavailable, commonv1.ErrorCode_ERROR_CODE_TEMPORARILY_UNAVAILABLE, "seckill dependency unavailable", true, err))
	}
	switch {
	case errors.Is(err, context.Canceled):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.Canceled, commonv1.ErrorCode_ERROR_CODE_TEMPORARILY_UNAVAILABLE, "request canceled", true, err))
	case errors.Is(err, seckill.ErrInvalidArgument):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.InvalidArgument, commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid seckill request", false, err))
	case errors.Is(err, seckill.ErrActivityNotFound), errors.Is(err, seckill.ErrAsyncResultNotFound):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.NotFound, commonv1.ErrorCode_ERROR_CODE_NOT_FOUND, "seckill resource not found", false, err))
	case errors.Is(err, seckill.ErrOutOfStock):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.FailedPrecondition, commonv1.ErrorCode_ERROR_CODE_OUT_OF_STOCK, "seckill item out of stock", false, err))
	case errors.Is(err, seckill.ErrUnavailable), errors.Is(err, seckill.ErrCacheNotReady):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.FailedPrecondition, commonv1.ErrorCode_ERROR_CODE_CONFLICT, "seckill activity unavailable", false, err))
	case errors.Is(err, seckill.ErrNoItems):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.FailedPrecondition, commonv1.ErrorCode_ERROR_CODE_CONFLICT, "seckill activity has no items", false, err))
	case errors.Is(err, seckill.ErrAdmissionFailure), errors.Is(err, seckill.ErrQueueUnavailable):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.Unavailable, commonv1.ErrorCode_ERROR_CODE_TEMPORARILY_UNAVAILABLE, "seckill queue unavailable", true, err))
	default:
		return platformrpc.StatusError(err)
	}
}
