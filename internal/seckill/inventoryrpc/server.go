package inventoryrpc

import (
	"context"
	"errors"
	"time"

	commonv1 "service_rpc/api/gen/common/v1"
	inventoryv1 "service_rpc/api/gen/inventory/v1"
	platformrpc "service_rpc/internal/platform/rpc"
	"service_rpc/internal/seckill"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	inventoryv1.UnimplementedInventoryServiceServer
	items        *seckill.InventoryItemService
	reservations *seckill.InventoryService
}

func NewServer(items *seckill.InventoryItemService, reservations *seckill.InventoryService) *Server {
	return &Server{items: items, reservations: reservations}
}

func (s *Server) CreateSeckillItem(ctx context.Context, request *inventoryv1.CreateSeckillItemRequest) (*inventoryv1.CreateSeckillItemResponse, error) {
	created, err := s.items.CreateSeckillItem(ctx, seckill.CreateInventoryItemInput{
		ActivityID: request.GetActivityId(), SKUID: request.GetSkuId(), Stock: request.GetStock(),
	})
	if err != nil {
		return nil, mapInventoryError(err)
	}
	return &inventoryv1.CreateSeckillItemResponse{Item: toProtoItem(created)}, nil
}

func (s *Server) ReserveSeckillStock(ctx context.Context, request *inventoryv1.ReserveSeckillStockRequest) (*inventoryv1.ReserveSeckillStockResponse, error) {
	reserved, err := s.reservations.ReserveSeckillStock(ctx, seckill.InventoryReservationInput{
		OrderNo: request.GetOrderNo(), ActivityID: request.GetActivityId(), ItemID: request.GetItemId(),
		UserID: request.GetUserId(), ReservedAt: time.UnixMilli(request.GetReservedAtMs()).UTC(),
	})
	if err != nil {
		return nil, mapInventoryError(err)
	}
	return &inventoryv1.ReserveSeckillStockResponse{
		ReservationId: reserved.ID, OrderNo: reserved.OrderNo, Sku: toProtoSnapshot(reserved.Snapshot), Replayed: reserved.Replayed,
	}, nil
}

func (s *Server) ListActivityItems(ctx context.Context, request *inventoryv1.ListActivityItemsRequest) (*inventoryv1.ListActivityItemsResponse, error) {
	items, err := s.items.ListActivityItems(ctx, request.GetActivityId())
	if err != nil {
		return nil, mapInventoryError(err)
	}
	result := make([]*inventoryv1.SeckillItem, 0, len(items))
	for _, item := range items {
		result = append(result, toProtoItem(item))
	}
	return &inventoryv1.ListActivityItemsResponse{Items: result}, nil
}

func toProtoItem(value seckill.InventoryItem) *inventoryv1.SeckillItem {
	return &inventoryv1.SeckillItem{
		Id: value.ID, ActivityId: value.ActivityID, Sku: toProtoSnapshot(value.Snapshot),
		InitialStock: value.InitialStock, AvailableStock: value.AvailableStock,
		Version: value.Version, CreatedAtMs: value.CreatedAt.UTC().UnixMilli(),
	}
}

func toProtoSnapshot(value seckill.FrozenSKUSnapshot) *inventoryv1.FrozenSkuSnapshot {
	return &inventoryv1.FrozenSkuSnapshot{
		SkuId: value.SKUID, ProductName: value.ProductName, SkuCode: value.SKUCode,
		SkuName: value.SKUName, UnitPriceCent: value.UnitPriceCent,
	}
}

func mapInventoryError(err error) error {
	code := status.Code(err)
	if code == codes.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) {
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.DeadlineExceeded, commonv1.ErrorCode_ERROR_CODE_TEMPORARILY_UNAVAILABLE, "inventory dependency timeout", true, err))
	}
	if code == codes.Unavailable {
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.Unavailable, commonv1.ErrorCode_ERROR_CODE_TEMPORARILY_UNAVAILABLE, "inventory dependency unavailable", true, err))
	}
	switch {
	case errors.Is(err, context.Canceled):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.Canceled, commonv1.ErrorCode_ERROR_CODE_TEMPORARILY_UNAVAILABLE, "request canceled", true, err))
	case errors.Is(err, seckill.ErrInvalidArgument):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.InvalidArgument, commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid inventory request", false, err))
	case errors.Is(err, seckill.ErrItemNotFound):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.NotFound, commonv1.ErrorCode_ERROR_CODE_NOT_FOUND, "inventory item not found", false, err))
	case errors.Is(err, seckill.ErrOutOfStock):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.FailedPrecondition, commonv1.ErrorCode_ERROR_CODE_OUT_OF_STOCK, "inventory out of stock", false, err))
	case errors.Is(err, seckill.ErrConflict):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.AlreadyExists, commonv1.ErrorCode_ERROR_CODE_CONFLICT, "inventory idempotency conflict", false, err))
	default:
		return platformrpc.StatusError(err)
	}
}
