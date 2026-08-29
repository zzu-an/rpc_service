package mq

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"service_rpc/internal/seckill"
)

type fixedMessageHandler struct{ err error }

func (h fixedMessageHandler) Handle(context.Context, string, []byte) error { return h.err }

type deliveryProducer struct {
	keys   []string
	values [][]byte
	err    error
}

func (p *deliveryProducer) Publish(_ context.Context, key string, value []byte) error {
	p.keys = append(p.keys, key)
	p.values = append(p.values, append([]byte(nil), value...))
	return p.err
}
func (*deliveryProducer) Close() error { return nil }

func newDeliveryFixture(t *testing.T, processErr error, attempt int) (*DeliveryHandler, OrderRequestedV1, []byte, *deliveryProducer, *deliveryProducer, *consumerJobs) {
	t.Helper()
	event, payload := messageFixture(t)
	event.Attempt = attempt
	payload, _ = EncodeOrderRequestedV1(event)
	jobs := &consumerJobs{job: seckill.OrderJob{
		ID: 1, EventID: event.EventID, OrderNo: event.OrderNo, UserID: event.UserID,
		ItemID: event.ItemID, ReservedAt: event.ReservedAt(), Status: seckill.JobStatusPublished,
	}}
	retry, dlq := &deliveryProducer{}, &deliveryProducer{}
	delivery, err := NewDeliveryHandler(fixedMessageHandler{err: processErr}, jobs, retry, dlq, 3)
	if err != nil {
		t.Fatal(err)
	}
	return delivery, event, payload, retry, dlq, jobs
}

func TestDeliveryHandlerPublishesRetryBeforeAck(t *testing.T) {
	delivery, event, payload, retry, dlq, _ := newDeliveryFixture(t, context.DeadlineExceeded, 0)
	if err := delivery.Handle(context.Background(), event.MessageKey(), payload); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(retry.values) != 1 || len(dlq.values) != 0 {
		t.Fatalf("retry=%d dlq=%d", len(retry.values), len(dlq.values))
	}
	retried, err := DecodeOrderRequestedV1(retry.values[0])
	if err != nil || retried.Attempt != 1 || retried.EventID != event.EventID {
		t.Fatalf("retried=%+v error=%v", retried, err)
	}
}

func TestDeliveryHandlerRetryPublishFailureReturnsError(t *testing.T) {
	delivery, event, payload, retry, _, _ := newDeliveryFixture(t, errors.New("mysql"), 0)
	retry.err = errors.New("kafka unavailable")
	if err := delivery.Handle(context.Background(), event.MessageKey(), payload); err == nil {
		t.Fatal("Handle() error = nil")
	}
}

func TestDeliveryHandlerExhaustionPublishesDLQAndFailsJob(t *testing.T) {
	delivery, event, payload, retry, dlq, jobs := newDeliveryFixture(t, context.DeadlineExceeded, 2)
	if err := delivery.Handle(context.Background(), event.MessageKey(), payload); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(retry.values) != 0 || len(dlq.values) != 1 {
		t.Fatalf("retry=%d dlq=%d", len(retry.values), len(dlq.values))
	}
	var dead DeadLetterV1
	if err := json.Unmarshal(dlq.values[0], &dead); err != nil || dead.ErrorCode != "MYSQL_TIMEOUT" || dead.EventID != event.EventID {
		t.Fatalf("dead=%+v error=%v", dead, err)
	}
	if jobs.job.ID != 1 || !jobs.markFailed {
		t.Fatalf("job failure was not recorded: %+v", jobs)
	}
}

func TestDeliveryHandlerPoisonMessageGoesDirectlyToDLQ(t *testing.T) {
	jobs := &consumerJobs{}
	retry, dlq := &deliveryProducer{}, &deliveryProducer{}
	delivery, _ := NewDeliveryHandler(fixedMessageHandler{err: ErrInvalidMessage}, jobs, retry, dlq, 3)
	if err := delivery.Handle(context.Background(), "", []byte("not-json")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(dlq.values) != 1 || len(retry.values) != 0 || dlq.keys[0] == "" {
		t.Fatalf("retry=%d dlq=%d keys=%v", len(retry.values), len(dlq.values), dlq.keys)
	}
}

func TestClassifyConsumeError(t *testing.T) {
	if code, retryable := classifyConsumeError(seckill.ErrOutOfStock); retryable || code != "BUSINESS_REJECTED" {
		t.Fatalf("out of stock classification = %s/%t", code, retryable)
	}
	if code, retryable := classifyConsumeError(errors.New("temporary")); !retryable || code == "" {
		t.Fatalf("temporary classification = %s/%t", code, retryable)
	}
	if got := deadLetterKey("", []byte("same")); got != deadLetterKey("", []byte("same")) || got == "" {
		t.Fatalf("deadLetterKey() = %q", got)
	}
}
