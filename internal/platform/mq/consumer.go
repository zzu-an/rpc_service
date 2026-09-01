package mq

import (
	"context"
	"errors"
	"fmt"
)

type ConsumerConfig struct {
	RetryTopic  string
	DLQTopic    string
	MaxAttempts int
}

func (c ConsumerConfig) Validate() error {
	if c.RetryTopic == "" || c.DLQTopic == "" || c.RetryTopic == c.DLQTopic || c.MaxAttempts <= 0 {
		return fmt.Errorf("Kafka retry topic, DLQ topic, and positive max attempts are required")
	}
	return nil
}

type Consumer struct {
	source    Source
	publisher Publisher
	handler   Handler
	config    ConsumerConfig
}

func NewConsumer(source Source, publisher Publisher, handler Handler, config ConsumerConfig) (*Consumer, error) {
	if source == nil || publisher == nil || handler == nil {
		return nil, fmt.Errorf("Kafka source, publisher, and handler are required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Consumer{source: source, publisher: publisher, handler: handler, config: config}, nil
}

func (c *Consumer) Run(ctx context.Context) error {
	for {
		if err := c.ProcessOne(ctx); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return err
		}
	}
}

func (c *Consumer) ProcessOne(ctx context.Context) error {
	message, err := c.source.Fetch(ctx)
	if err != nil {
		return err
	}
	attempt, attemptErr := message.Attempt()
	handleErr := attemptErr
	if handleErr == nil {
		handleErr = c.handler(ctx, message)
	}
	if handleErr == nil {
		return c.source.Commit(ctx, message)
	}

	// poison/未知版本不会通过重复执行自愈，直接进入 DLQ；临时错误在预算内进入 retry topic。
	// Kafka 没有单消息 NACK：只有目标 topic 得到 broker ack 后，才允许提交源 offset。
	target := c.config.RetryTopic
	nextAttempt := attempt + 1
	if errors.Is(handleErr, ErrPermanent) || nextAttempt >= c.config.MaxAttempts {
		target = c.config.DLQTopic
	}
	if err := c.publisher.Publish(ctx, message.WithAttempt(target, nextAttempt)); err != nil {
		// publish 未确认时保留源 offset，允许 rebalance/重启后重新处理；绝不能先 commit 再 publish。
		return fmt.Errorf("publish Kafka retry/DLQ before source commit: %w", err)
	}
	if err := c.source.Commit(ctx, message); err != nil {
		// 目标已确认但 commit 失败会产生重复 retry/DLQ，这是 at-least-once 的正常窗口；
		// 下游必须使用 event_id ledger，offset 从来不是业务幂等标记。
		return fmt.Errorf("commit Kafka source after retry/DLQ publish: %w", err)
	}
	return nil
}
