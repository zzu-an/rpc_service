package inventoryclient

import (
	"context"

	inventoryv1 "service_rpc/api/gen/inventory/v1"
	"service_rpc/internal/config"
	platformrpc "service_rpc/internal/platform/rpc"
)

type Client struct {
	service inventoryv1.InventoryServiceClient
}

func New(ctx context.Context, c config.RPCClientConfig) (*Client, error) {
	connection, err := platformrpc.NewClient(ctx, c)
	if err != nil {
		return nil, err
	}
	return &Client{service: inventoryv1.NewInventoryServiceClient(connection.Conn())}, nil
}

func NewFromService(service inventoryv1.InventoryServiceClient) *Client {
	return &Client{service: service}
}

func (c *Client) CreateSeckillItem(ctx context.Context, request *inventoryv1.CreateSeckillItemRequest) (*inventoryv1.CreateSeckillItemResponse, error) {
	return c.service.CreateSeckillItem(ctx, request)
}

func (c *Client) ListActivityItems(ctx context.Context, activityID uint64) ([]*inventoryv1.SeckillItem, error) {
	response, err := c.service.ListActivityItems(ctx, &inventoryv1.ListActivityItemsRequest{ActivityId: activityID})
	if err != nil {
		return nil, err
	}
	return response.GetItems(), nil
}

func (c *Client) ReserveSeckillStock(ctx context.Context, request *inventoryv1.ReserveSeckillStockRequest) (*inventoryv1.ReserveSeckillStockResponse, error) {
	// 写 RPC 只有依赖 order_no + 载荷冲突校验才可由上层重试；client 本身不叠加默认重试。
	return c.service.ReserveSeckillStock(ctx, request)
}
