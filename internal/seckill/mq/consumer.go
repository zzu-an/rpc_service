package mq

import (
	"context"
	"errors"
	"fmt"

	"service_rpc/internal/seckill"
)

var ErrMessageIdentityConflict = errors.New("seckill message identity conflict")

type queuedJobProcessor interface {
	ProcessQueuedJob(ctx context.Context, job seckill.OrderJob) (seckill.PurchaseResult, error)
}

// ConsumerHandler 完成“消息→持久化 job→领域落单”的单次处理。
// Kafka offset 的提交由 runtime 控制；Handle 返回 nil 之前，订单和 SUCCEEDED 状态必须可恢复。
type ConsumerHandler struct {
	jobs      seckill.JobRepository
	processor queuedJobProcessor
}

func NewConsumerHandler(jobs seckill.JobRepository, processor queuedJobProcessor) (*ConsumerHandler, error) {
	if jobs == nil || processor == nil {
		return nil, fmt.Errorf("consumer job repository and processor are required")
	}
	return &ConsumerHandler{jobs: jobs, processor: processor}, nil
}

func (h *ConsumerHandler) Handle(ctx context.Context, key string, value []byte) error {
	event, err := DecodeOrderRequestedV1(value)
	if err != nil {
		return err
	}
	if key != event.MessageKey() {
		return ErrMessageIdentityConflict
	}
	job, err := h.jobs.FindJobOwned(ctx, event.UserID, event.OrderNo)
	if err != nil {
		return err
	}
	// Kafka topic 不是授权边界。即使生产 topic 应受 ACL 保护，消费者仍要把消息身份与
	// 已持久化 job 对齐，防止伪造 user/item/orderNo 触发越权扣库存。event_id 是确定性的，
	// reserved_at 必须完全等于 HTTP 接受时冻结的值。
	if job.EventID != event.EventID || job.ItemID != event.ItemID || !job.ReservedAt.Equal(event.ReservedAt()) {
		return ErrMessageIdentityConflict
	}
	if job.Status == seckill.JobStatusFailed {
		return ErrMessageIdentityConflict
	}
	_, err = h.processor.ProcessQueuedJob(ctx, job)
	return err
}
