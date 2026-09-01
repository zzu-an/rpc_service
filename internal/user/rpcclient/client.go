// Package rpcclient 是 gateway 调用 user-rpc 的手写边界，避免 HTTP DTO 直接依赖生成 client 的构造细节。
package rpcclient

import (
	"context"

	userv1 "service_rpc/api/gen/user/v1"
	"service_rpc/internal/config"
	platformrpc "service_rpc/internal/platform/rpc"
)

type Client struct {
	service userv1.UserServiceClient
}

func New(ctx context.Context, c config.RPCClientConfig) (*Client, error) {
	connection, err := platformrpc.NewClient(ctx, c)
	if err != nil {
		return nil, err
	}
	return &Client{service: userv1.NewUserServiceClient(connection.Conn())}, nil
}

func NewFromService(service userv1.UserServiceClient) *Client {
	return &Client{service: service}
}

func (c *Client) Register(ctx context.Context, request *userv1.RegisterRequest) (*userv1.RegisterResponse, error) {
	return c.service.Register(ctx, request)
}

func (c *Client) Authenticate(ctx context.Context, request *userv1.AuthenticateRequest) (*userv1.AuthenticateResponse, error) {
	// 这里必须原样传播调用方 ctx。若换成 Background，HTTP 已取消后 bcrypt/SQL 仍会继续占资源。
	return c.service.Authenticate(ctx, request)
}

func (c *Client) GetUser(ctx context.Context, request *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	return c.service.GetUser(ctx, request)
}

func (c *Client) GetUserRoles(ctx context.Context, request *userv1.GetUserRolesRequest) (*userv1.GetUserRolesResponse, error) {
	return c.service.GetUserRoles(ctx, request)
}

func (c *Client) HasPermission(ctx context.Context, request *userv1.HasPermissionRequest) (*userv1.HasPermissionResponse, error) {
	return c.service.HasPermission(ctx, request)
}

func (c *Client) ReplaceUserRoles(ctx context.Context, request *userv1.ReplaceUserRolesRequest) (*userv1.ReplaceUserRolesResponse, error) {
	return c.service.ReplaceUserRoles(ctx, request)
}
