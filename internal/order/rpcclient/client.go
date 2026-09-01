// Package rpcclient provides thin order-rpc calls for gateway and orchestrator.
package rpcclient

import (
	"context"

	orderv1 "service_rpc/api/gen/order/v1"
	"service_rpc/internal/config"
	platformrpc "service_rpc/internal/platform/rpc"
)

type Client struct{ service orderv1.OrderServiceClient }

func New(ctx context.Context, c config.RPCClientConfig) (*Client, error) {
	connection, err := platformrpc.NewClient(ctx, c)
	if err != nil {
		return nil, err
	}
	return &Client{service: orderv1.NewOrderServiceClient(connection.Conn())}, nil
}

func NewFromService(service orderv1.OrderServiceClient) *Client { return &Client{service: service} }

func (c *Client) CreateOrder(ctx context.Context, request *orderv1.CreateOrderRequest) (*orderv1.CreateOrderResponse, error) {
	// 普通创建没有外部幂等 key，底层绝不能自动重试；超时由调用方按“结果未知”处理。
	return c.service.CreateOrder(ctx, request)
}

func (c *Client) GetOrder(ctx context.Context, userID, orderID uint64) (*orderv1.GetOrderResponse, error) {
	return c.service.GetOrder(ctx, &orderv1.GetOrderRequest{UserId: userID, OrderId: orderID})
}

func (c *Client) CreateSeckillOrder(ctx context.Context, request *orderv1.CreateSeckillOrderRequest) (*orderv1.CreateSeckillOrderResponse, error) {
	// 该调用具备稳定 order_no 和全载荷冲突检查，允许 orchestrator 在自己的预算内重试；
	// client 仍不私自重试，避免与外层退避相乘形成 retry storm。
	return c.service.CreateSeckillOrder(ctx, request)
}

func (c *Client) FindSeckillOrder(ctx context.Context, userID uint64, orderNo string) (*orderv1.FindSeckillOrderResponse, error) {
	return c.service.FindSeckillOrder(ctx, &orderv1.FindSeckillOrderRequest{UserId: userID, OrderNo: orderNo})
}
