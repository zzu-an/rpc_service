package mq

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	platformmq "service_rpc/internal/platform/mq"
	"service_rpc/internal/seckill"
)

const DeadLetterSchemaV1 = 1

type DeadLetterV1 struct {
	SchemaVersion int    `json:"schema_version"`
	SourceKey     string `json:"source_key"`
	EventID       string `json:"event_id,omitempty"`
	ErrorCode     string `json:"error_code"`
	FailedAtMS    int64  `json:"failed_at_ms"`
	OriginalValue []byte `json:"original_value"`
}

type messageHandler interface {
	Handle(ctx context.Context, key string, value []byte) error
}

// DeliveryHandler 把业务处理错误转成交给 retry/DLQ 的可靠交接。
// kq 只有在本方法返回 nil 后才可提交源 offset；因此目标 topic 发布失败必须返回 error，
// 让源消息重新投递。跨 topic 发布与 offset 提交不是原子事务，崩溃会产生重复 retry/DLQ，
// 但稳定 event_id 和数据库幂等可吸收重复；反过来先提交 offset 会造成不可恢复的丢失。
type DeliveryHandler struct {
	handler     messageHandler
	jobs        seckill.JobRepository
	retry       platformmq.Producer
	dlq         platformmq.Producer
	maxAttempts int
	now         func() time.Time
}

func NewDeliveryHandler(handler messageHandler, jobs seckill.JobRepository, retry, dlq platformmq.Producer, maxAttempts int) (*DeliveryHandler, error) {
	if handler == nil || jobs == nil || retry == nil || dlq == nil || maxAttempts <= 0 {
		return nil, fmt.Errorf("delivery handler, jobs, producers, and max attempts are required")
	}
	return &DeliveryHandler{handler: handler, jobs: jobs, retry: retry, dlq: dlq, maxAttempts: maxAttempts, now: time.Now}, nil
}

func (h *DeliveryHandler) Handle(ctx context.Context, key string, value []byte) error {
	processErr := h.handler.Handle(ctx, key, value)
	if processErr == nil {
		return nil
	}
	event, decodeErr := DecodeOrderRequestedV1(value)
	if decodeErr != nil {
		// poison message 连 event_id 都不可相信，不能无限返回 error 阻塞 partition。
		// 只有 DLQ broker ack 后才返回 nil；DLQ key 由原文 hash 生成，重投仍稳定。
		return h.publishDLQ(ctx, deadLetterKey(key, value), key, "INVALID_MESSAGE", "", value)
	}

	errorCode, retryable := classifyConsumeError(processErr)
	if retryable && event.Attempt+1 < h.maxAttempts {
		event.Attempt++
		payload, err := EncodeOrderRequestedV1(event)
		if err != nil {
			return err
		}
		if err := h.retry.Publish(ctx, event.MessageKey(), payload); err != nil {
			return fmt.Errorf("publish retry message: %w", err)
		}
		return nil
	}

	if err := h.publishDLQ(ctx, event.MessageKey(), key, errorCode, event.EventID, value); err != nil {
		return err
	}
	// 只在消息身份与 job 完全一致时写 FAILED。攻击者即使能向 topic 写一条伪造 item_id，
	// 也不能借“身份冲突”把合法用户的 job 标成失败。坏消息已经进入 DLQ，可离线审查。
	job, err := h.jobs.FindJobOwned(ctx, event.UserID, event.OrderNo)
	if err != nil {
		if errors.Is(err, seckill.ErrJobNotFound) {
			return nil
		}
		return err
	}
	if job.EventID != event.EventID || job.ItemID != event.ItemID || !job.ReservedAt.Equal(event.ReservedAt()) {
		return nil
	}
	updated, err := h.jobs.MarkJobFailed(ctx, job.ID, h.now().UTC(), errorCode)
	if err != nil {
		// DLQ 已成功但状态写失败时返回 error，会产生重复 DLQ；这比先确认源消息、留下
		// 永久 QUEUED 状态更容易恢复。重复 DLQ 由 event_id/error_code 聚合即可。
		return fmt.Errorf("mark dead-lettered job failed: %w", err)
	}
	if !updated && job.Status != seckill.JobStatusSucceeded && job.Status != seckill.JobStatusFailed {
		return fmt.Errorf("mark dead-lettered job failed: state changed")
	}
	return nil
}

func (h *DeliveryHandler) publishDLQ(ctx context.Context, dlqKey, sourceKey, code, eventID string, original []byte) error {
	envelope := DeadLetterV1{
		SchemaVersion: DeadLetterSchemaV1, SourceKey: sourceKey, EventID: eventID,
		ErrorCode: code, FailedAtMS: h.now().UTC().UnixMilli(), OriginalValue: original,
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("encode dead letter: %w", err)
	}
	if err := h.dlq.Publish(ctx, dlqKey, payload); err != nil {
		return fmt.Errorf("publish dead letter: %w", err)
	}
	return nil
}

func classifyConsumeError(err error) (code string, retryable bool) {
	switch {
	case errors.Is(err, ErrInvalidMessage), errors.Is(err, ErrUnsupportedMessage):
		return "INVALID_MESSAGE", false
	case errors.Is(err, ErrMessageIdentityConflict), errors.Is(err, seckill.ErrJobConflict):
		return "MESSAGE_IDENTITY_CONFLICT", false
	case errors.Is(err, seckill.ErrInvalidArgument), errors.Is(err, seckill.ErrJobNotFound):
		return "INVALID_JOB", false
	case errors.Is(err, seckill.ErrUnavailable), errors.Is(err, seckill.ErrItemNotFound), errors.Is(err, seckill.ErrOutOfStock):
		return "BUSINESS_REJECTED", false
	case errors.Is(err, context.Canceled):
		return "CONSUMER_CANCELED", true
	case errors.Is(err, context.DeadlineExceeded):
		return "MYSQL_TIMEOUT", true
	default:
		return "CONSUME_TEMPORARY_FAILURE", true
	}
}

func deadLetterKey(sourceKey string, value []byte) string {
	if sourceKey != "" {
		return sourceKey
	}
	digest := sha256.Sum256(value)
	return "invalid-" + hex.EncodeToString(digest[:8])
}
