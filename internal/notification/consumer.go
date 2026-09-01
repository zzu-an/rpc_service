package notification

import (
	"context"
	"errors"

	"service_rpc/internal/order/events"
	"service_rpc/internal/platform/mq"
)

func NewOrderCreatedHandler(service *Service) mq.Handler {
	return func(ctx context.Context, message mq.Message) error {
		event, err := events.DecodeOrderCreatedV1(message.Value)
		if err != nil {
			// JSON 损坏/未知版本是 poison event，重试不会自愈；先发布 DLQ 成功后，
			// 通用 consumer 才提交源 offset，防止“先跳过、后丢失”。
			if errors.Is(err, events.ErrInvalidEvent) || errors.Is(err, events.ErrUnsupportedVersion) {
				return mq.Permanent(err)
			}
			return err
		}
		_, err = service.ConsumeOrderCreated(ctx, event)
		return err
	}
}
