// Package rpcserver adapts order use cases to order.v1 gRPC.
package rpcserver

import (
	"context"
	"errors"
	"strings"

	commonv1 "service_rpc/api/gen/common/v1"
	orderv1 "service_rpc/api/gen/order/v1"
	"service_rpc/internal/order"
	platformrpc "service_rpc/internal/platform/rpc"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	orderv1.UnimplementedOrderServiceServer
	service *order.Service
}

func New(service *order.Service) *Server { return &Server{service: service} }

func (s *Server) CreateOrder(ctx context.Context, request *orderv1.CreateOrderRequest) (*orderv1.CreateOrderResponse, error) {
	inputs := make([]order.ItemInput, 0, len(request.GetItems()))
	for _, item := range request.GetItems() {
		inputs = append(inputs, order.ItemInput{SKUID: item.GetSkuId(), Quantity: item.GetQuantity()})
	}
	// 普通订单没有客户端幂等 key：每次调用生成新 order_no，因此默认禁止传输层自动重试。
	// product-rpc 只负责把 SKU 解析成服务端可信快照，客户端价格永不进入订单表。
	created, err := s.service.Create(ctx, request.GetUserId(), inputs)
	if err != nil {
		return nil, mapError(err)
	}
	return &orderv1.CreateOrderResponse{Order: toProtoOrder(created, orderv1.OrderSource_ORDER_SOURCE_NORMAL)}, nil
}

func (s *Server) GetOrder(ctx context.Context, request *orderv1.GetOrderRequest) (*orderv1.GetOrderResponse, error) {
	found, err := s.service.Get(ctx, request.GetUserId(), request.GetOrderId())
	if err != nil {
		return nil, mapError(err)
	}
	return &orderv1.GetOrderResponse{Order: toProtoOrder(found, sourceFromOrderNo(found.OrderNo))}, nil
}

func (s *Server) CreateSeckillOrder(ctx context.Context, request *orderv1.CreateSeckillOrderRequest) (*orderv1.CreateSeckillOrderResponse, error) {
	item := request.GetItem()
	if request.GetActivityId() == 0 || request.GetItemId() == 0 || request.GetReservedAtMs() <= 0 || item == nil {
		return nil, mapError(order.ErrInvalidOrder)
	}
	// 这是 orchestrator 从 inventory-rpc reservation 原样转交的冻结快照。
	// order-rpc 不再查询 product-rpc，否则积压期间变价会让同 order_no 的重试变成另一笔载荷。
	result, err := s.service.CreateSeckill(ctx, request.GetUserId(), request.GetOrderNo(), order.ItemInput{
		SKUID: item.GetSkuId(), ProductName: item.GetProductName(), SKUCode: item.GetSkuCode(),
		SKUName: item.GetSkuName(), UnitPriceCent: item.GetUnitPriceCent(), Quantity: 1,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &orderv1.CreateSeckillOrderResponse{Order: toProtoOrder(result.Order, orderv1.OrderSource_ORDER_SOURCE_SECKILL), Replayed: result.Replayed}, nil
}

func (s *Server) FindSeckillOrder(ctx context.Context, request *orderv1.FindSeckillOrderRequest) (*orderv1.FindSeckillOrderResponse, error) {
	found, err := s.service.FindSeckill(ctx, request.GetUserId(), request.GetOrderNo())
	if err != nil {
		return nil, mapError(err)
	}
	return &orderv1.FindSeckillOrderResponse{Order: toProtoOrder(found, orderv1.OrderSource_ORDER_SOURCE_SECKILL)}, nil
}

func toProtoOrder(value order.Order, source orderv1.OrderSource) *orderv1.Order {
	items := make([]*orderv1.OrderItem, 0, len(value.Items))
	for _, item := range value.Items {
		items = append(items, &orderv1.OrderItem{Id: item.ID, Snapshot: &orderv1.FrozenOrderItem{
			SkuId: item.SKUID, ProductName: item.ProductName, SkuCode: item.SKUCode,
			SkuName: item.SKUName, UnitPriceCent: item.UnitPriceCent, Quantity: item.Quantity,
		}, SubtotalCent: item.SubtotalCent})
	}
	return &orderv1.Order{
		Id: value.ID, OrderNo: value.OrderNo, UserId: value.UserID, Status: orderv1.OrderStatus_ORDER_STATUS_CREATED,
		Source: source, TotalAmountCent: value.TotalAmountCent, Items: items, CreatedAtMs: value.CreatedAt.UTC().UnixMilli(),
	}
}

func sourceFromOrderNo(orderNo string) orderv1.OrderSource {
	if strings.HasPrefix(strings.TrimSpace(orderNo), "T") {
		return orderv1.OrderSource_ORDER_SOURCE_SECKILL
	}
	return orderv1.OrderSource_ORDER_SOURCE_NORMAL
}

func mapError(err error) error {
	grpcCode := status.Code(err)
	if grpcCode == codes.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) {
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.DeadlineExceeded, commonv1.ErrorCode_ERROR_CODE_TEMPORARILY_UNAVAILABLE, "order dependency timeout", true, err))
	}
	if grpcCode == codes.Unavailable {
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.Unavailable, commonv1.ErrorCode_ERROR_CODE_TEMPORARILY_UNAVAILABLE, "order dependency unavailable", true, err))
	}
	switch {
	case errors.Is(err, context.Canceled):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.Canceled, commonv1.ErrorCode_ERROR_CODE_TEMPORARILY_UNAVAILABLE, "request canceled", true, err))
	case errors.Is(err, order.ErrInvalidOrder):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.InvalidArgument, commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid order", false, err))
	case errors.Is(err, order.ErrOrderNotFound):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.NotFound, commonv1.ErrorCode_ERROR_CODE_NOT_FOUND, "order not found", false, err))
	case errors.Is(err, order.ErrOrderConflict):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.AlreadyExists, commonv1.ErrorCode_ERROR_CODE_CONFLICT, "order idempotency conflict", false, err))
	default:
		return platformrpc.StatusError(err)
	}
}
