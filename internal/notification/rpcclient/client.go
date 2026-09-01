package rpcclient

import (
	"context"

	notificationv1 "service_rpc/api/gen/notification/v1"
	"service_rpc/internal/config"
	platformrpc "service_rpc/internal/platform/rpc"
)

type Client struct {
	service notificationv1.NotificationServiceClient
}

func New(ctx context.Context, c config.RPCClientConfig) (*Client, error) {
	connection, err := platformrpc.NewClient(ctx, c)
	if err != nil {
		return nil, err
	}
	return &Client{service: notificationv1.NewNotificationServiceClient(connection.Conn())}, nil
}

func NewFromService(service notificationv1.NotificationServiceClient) *Client {
	return &Client{service: service}
}

func (c *Client) ListNotifications(ctx context.Context, request *notificationv1.ListNotificationsRequest) (*notificationv1.ListNotificationsResponse, error) {
	return c.service.ListNotifications(ctx, request)
}

func (c *Client) MarkRead(ctx context.Context, request *notificationv1.MarkReadRequest) error {
	_, err := c.service.MarkRead(ctx, request)
	return err
}
