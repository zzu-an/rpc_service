// Package outboxrelay publishes committed order events without coupling order-rpc to Kafka availability.
package outboxrelay

import (
	"context"
	"fmt"
	"strings"
	"time"

	"service_rpc/internal/order"
	"service_rpc/internal/platform/mq"
)

type Config struct {
	WorkerID  string
	Topic     string
	BatchSize int
	Lease     time.Duration
	Poll      time.Duration
	RetryBase time.Duration
	RetryMax  time.Duration
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.WorkerID) == "" || strings.TrimSpace(c.Topic) == "" || c.BatchSize <= 0 || c.BatchSize > 1000 || c.Lease <= 0 || c.Poll <= 0 || c.RetryBase <= 0 || c.RetryMax < c.RetryBase {
		return fmt.Errorf("invalid order outbox relay config")
	}
	return nil
}

type Relay struct {
	repository order.OutboxRepository
	publisher  mq.Publisher
	config     Config
	now        func() time.Time
	// afterPublish 只供故障注入测试：模拟 broker ack 后、DB 标记前崩溃。
	afterPublish func(order.OutboxEvent) error
}

func New(repository order.OutboxRepository, publisher mq.Publisher, config Config) (*Relay, error) {
	if repository == nil || publisher == nil {
		return nil, fmt.Errorf("order outbox repository and Kafka publisher are required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Relay{repository: repository, publisher: publisher, config: config, now: time.Now}, nil
}

func (r *Relay) SetAfterPublishHook(hook func(order.OutboxEvent) error) { r.afterPublish = hook }

func (r *Relay) Run(ctx context.Context) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			// 未标记 published 的事件保持 pending/lease，租约到期后由其他实例恢复；
			// shutdown timeout 不能把“还在发”伪装成成功。
			return ctx.Err()
		case <-timer.C:
			processed, err := r.ProcessBatch(ctx)
			if err != nil {
				return err
			}
			if processed == 0 {
				timer.Reset(r.config.Poll)
			} else {
				timer.Reset(0)
			}
		}
	}
}

func (r *Relay) ProcessBatch(ctx context.Context) (int, error) {
	now := r.now().UTC()
	events, err := r.repository.ClaimOutbox(ctx, r.config.WorkerID, now, r.config.Lease, r.config.BatchSize)
	if err != nil {
		return 0, err
	}
	for _, event := range events {
		err := r.publisher.Publish(ctx, mq.Message{Topic: r.config.Topic, Key: []byte(event.AggregateKey), Value: event.Payload})
		if err != nil {
			next := now.Add(r.retryDelay(event.Attempts + 1))
			updated, markErr := r.repository.MarkOutboxFailed(ctx, event.ID, r.config.WorkerID, next, classifyPublishError(err))
			if markErr != nil {
				return 0, markErr
			}
			if !updated {
				return 0, fmt.Errorf("order outbox failure lease lost for event %d", event.ID)
			}
			continue
		}
		if r.afterPublish != nil {
			if err := r.afterPublish(event); err != nil {
				// broker 已 ack，但故意不标 published；租约过期后会再次发送同 event_id。
				return 0, err
			}
		}
		updated, err := r.repository.MarkOutboxPublished(ctx, event.ID, r.config.WorkerID, r.now().UTC())
		if err != nil {
			return 0, err
		}
		if !updated {
			return 0, fmt.Errorf("order outbox publish lease lost for event %d", event.ID)
		}
	}
	return len(events), nil
}

func (r *Relay) retryDelay(attempt int) time.Duration {
	delay := r.config.RetryBase
	for index := 1; index < attempt && delay < r.config.RetryMax; index++ {
		if delay > r.config.RetryMax/2 {
			return r.config.RetryMax
		}
		delay *= 2
	}
	if delay > r.config.RetryMax {
		return r.config.RetryMax
	}
	return delay
}

func classifyPublishError(error) string {
	// 不按 broker 文本分支，也不把地址/SASL 原因写入 DB。更细分类应来自可观测指标，
	// Outbox 只需要稳定、低基数的恢复状态。
	return "KAFKA_PUBLISH_FAILED"
}
