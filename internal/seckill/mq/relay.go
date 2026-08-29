package mq

import (
	"context"
	"errors"
	"fmt"
	"time"

	platformmq "service_rpc/internal/platform/mq"
	"service_rpc/internal/seckill"
)

const relayPublishErrorCode = "KAFKA_PUBLISH_FAILED"

type Relay struct {
	jobs      seckill.JobRepository
	producer  platformmq.Producer
	interval  time.Duration
	batchSize int
	now       func() time.Time
}

func NewRelay(jobs seckill.JobRepository, producer platformmq.Producer, interval time.Duration, batchSize int) (*Relay, error) {
	if jobs == nil || producer == nil || interval <= 0 || batchSize <= 0 || batchSize > 1000 {
		return nil, fmt.Errorf("valid relay repository, producer, interval, and batch size are required")
	}
	return &Relay{jobs: jobs, producer: producer, interval: interval, batchSize: batchSize, now: time.Now}, nil
}

func (r *Relay) Run(ctx context.Context) error {
	if r == nil {
		return fmt.Errorf("relay is nil")
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		if _, err := r.ProcessOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			// Run 不因单轮外部故障退出。pending job 已持久化，下一轮会继续；worker 的
			// 结构化日志在 TASK-043 记录错误分类，Relay 本身不打印 payload/DSN。
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (r *Relay) ProcessOnce(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	now := r.now().UTC()
	jobs, err := r.jobs.ListPendingJobs(ctx, now, r.batchSize)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		if err := r.producer.Publish(ctx, job.OrderNo, job.Payload); err != nil {
			next := now.Add(relayBackoff(job.PublishAttempts + 1))
			if _, scheduleErr := r.jobs.ScheduleJobPublishRetry(ctx, job.ID, next, relayPublishErrorCode); scheduleErr != nil {
				return processed, fmt.Errorf("publish job %d and schedule retry: %w", job.ID, errors.Join(err, scheduleErr))
			}
			continue
		}
		// broker ack 只证明消息已写入 Kafka，不证明 job 状态已更新。若这里超时/宕机，
		// 行仍是 PENDING，下一轮会重发同一 event/orderNo。这是“至少一次”的正常窗口：
		// 宁可重复也不能先标 published 再发送，否则进程崩溃会永久丢消息。
		updated, err := r.jobs.MarkJobPublished(ctx, job.ID, r.now().UTC())
		if err != nil {
			return processed, fmt.Errorf("mark published job %d: %w", job.ID, err)
		}
		if updated {
			processed++
		}
	}
	return processed, nil
}

func relayBackoff(attempt uint32) time.Duration {
	// 指数退避有 30 秒上限，防止 Kafka 故障时 pending 扫描变成对 broker/DB 的紧循环。
	// 没有随机数依赖，测试可重复；不同 job 的毫秒 jitter 由 ID 不在这里处理，v0.4
	// 首先验证有界恢复，不把完整调度系统提前引入。
	shift := attempt
	if shift > 5 {
		shift = 5
	}
	return time.Duration(1<<shift) * time.Second
}
