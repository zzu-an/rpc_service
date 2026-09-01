package rpcclient

import (
	"context"

	productv1 "service_rpc/api/gen/product/v1"
	"service_rpc/internal/config"
	platformrpc "service_rpc/internal/platform/rpc"
)

type Client struct {
	service productv1.ProductServiceClient
}

func New(ctx context.Context, c config.RPCClientConfig) (*Client, error) {
	connection, err := platformrpc.NewClient(ctx, c)
	if err != nil {
		return nil, err
	}
	return &Client{service: productv1.NewProductServiceClient(connection.Conn())}, nil
}

func NewFromService(service productv1.ProductServiceClient) *Client { return &Client{service: service} }

func (c *Client) Service() productv1.ProductServiceClient { return c.service }

// GetSkuSnapshot 是只读调用，TASK-057 不在底层偷偷重试；上层必须在 TASK-068 结合总预算统一决定。
func (c *Client) GetSkuSnapshot(ctx context.Context, skuID uint64) (*productv1.SkuSnapshot, error) {
	response, err := c.service.GetSkuSnapshot(ctx, &productv1.GetSkuSnapshotRequest{SkuId: skuID})
	if err != nil {
		return nil, err
	}
	return response.GetSnapshot(), nil
}
