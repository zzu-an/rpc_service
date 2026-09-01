// Package seckillclient 提供 gateway 使用的 seckill-rpc 薄客户端。
package seckillclient

import (
	"context"

	seckillv1 "service_rpc/api/gen/seckill/v1"
	"service_rpc/internal/config"
	platformrpc "service_rpc/internal/platform/rpc"
)

type Client struct {
	service seckillv1.SeckillServiceClient
}

func New(ctx context.Context, c config.RPCClientConfig) (*Client, error) {
	connection, err := platformrpc.NewClient(ctx, c)
	if err != nil {
		return nil, err
	}
	return &Client{service: seckillv1.NewSeckillServiceClient(connection.Conn())}, nil
}

func NewFromService(service seckillv1.SeckillServiceClient) *Client { return &Client{service: service} }

func (c *Client) CreateActivity(ctx context.Context, request *seckillv1.CreateActivityRequest) (*seckillv1.CreateActivityResponse, error) {
	return c.service.CreateActivity(ctx, request)
}

func (c *Client) UpdateActivityStatus(ctx context.Context, request *seckillv1.UpdateActivityStatusRequest) error {
	_, err := c.service.UpdateActivityStatus(ctx, request)
	return err
}

func (c *Client) PreheatActivity(ctx context.Context, activityID uint64) (*seckillv1.PreheatActivityResponse, error) {
	return c.service.PreheatActivity(ctx, &seckillv1.PreheatActivityRequest{ActivityId: activityID})
}

func (c *Client) Enqueue(ctx context.Context, userID, itemID uint64) (*seckillv1.EnqueueResponse, error) {
	// 写调用不在薄 client 内自动重试；超时后 Lua 可能已成功，是否重试必须由拥有
	// user_id 幂等语义的上层决定，而不是由通用 RPC 中间件盲目重放。
	return c.service.Enqueue(ctx, &seckillv1.EnqueueRequest{UserId: userID, ItemId: itemID})
}

func (c *Client) GetResult(ctx context.Context, userID uint64, orderNo string) (*seckillv1.GetResultResponse, error) {
	return c.service.GetResult(ctx, &seckillv1.GetResultRequest{UserId: userID, OrderNo: orderNo})
}

func (c *Client) ListStreamItemIDs(ctx context.Context) ([]uint64, error) {
	response, err := c.service.ListStreamItemIDs(ctx, &seckillv1.ListStreamItemIDsRequest{})
	if err != nil {
		return nil, err
	}
	return response.GetItemIds(), nil
}
