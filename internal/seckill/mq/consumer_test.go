package mq

import (
	"context"
	"errors"
	"testing"
	"time"

	"service_rpc/internal/order"
	"service_rpc/internal/seckill"
)

type consumerJobs struct {
	job        seckill.OrderJob
	err        error
	markFailed bool
}

func (*consumerJobs) EnsureJob(context.Context, seckill.EnsureJobInput) (seckill.OrderJob, bool, error) {
	return seckill.OrderJob{}, false, nil
}
func (j *consumerJobs) FindJobOwned(context.Context, uint64, string) (seckill.OrderJob, error) {
	return j.job, j.err
}
func (*consumerJobs) FindOrderOwned(context.Context, uint64, string) (order.Order, error) {
	return order.Order{}, seckill.ErrJobNotFound
}
func (*consumerJobs) ListPendingJobs(context.Context, time.Time, int) ([]seckill.OrderJob, error) {
	return nil, nil
}
func (*consumerJobs) MarkJobPublished(context.Context, uint64, time.Time) (bool, error) {
	return false, nil
}
func (*consumerJobs) ScheduleJobPublishRetry(context.Context, uint64, time.Time, string) (bool, error) {
	return false, nil
}
func (*consumerJobs) MarkJobSucceeded(context.Context, uint64, time.Time) (bool, error) {
	return false, nil
}
func (j *consumerJobs) MarkJobFailed(context.Context, uint64, time.Time, string) (bool, error) {
	j.markFailed = true
	return true, nil
}

type consumerProcessor struct {
	jobs []seckill.OrderJob
	err  error
}

func (p *consumerProcessor) ProcessQueuedJob(_ context.Context, job seckill.OrderJob) (seckill.PurchaseResult, error) {
	p.jobs = append(p.jobs, job)
	return seckill.PurchaseResult{}, p.err
}

func messageFixture(t *testing.T) (OrderRequestedV1, []byte) {
	t.Helper()
	reserved := time.Date(2026, 8, 29, 3, 0, 0, 0, time.UTC)
	event, err := NewOrderRequestedV1("S-consume", 7, 9, reserved, reserved)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := EncodeOrderRequestedV1(event)
	if err != nil {
		t.Fatal(err)
	}
	return event, payload
}

func TestConsumerHandlerValidatesAndProcesses(t *testing.T) {
	event, payload := messageFixture(t)
	jobs := &consumerJobs{job: seckill.OrderJob{
		ID: 1, EventID: event.EventID, OrderNo: event.OrderNo, UserID: event.UserID,
		ItemID: event.ItemID, ReservedAt: event.ReservedAt(), Status: seckill.JobStatusPublished,
	}}
	processor := &consumerProcessor{}
	handler, _ := NewConsumerHandler(jobs, processor)
	if err := handler.Handle(context.Background(), event.MessageKey(), payload); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(processor.jobs) != 1 || processor.jobs[0].OrderNo != event.OrderNo {
		t.Fatalf("processed jobs = %+v", processor.jobs)
	}
}

func TestConsumerHandlerRejectsIdentityDrift(t *testing.T) {
	event, payload := messageFixture(t)
	jobs := &consumerJobs{job: seckill.OrderJob{ID: 1, EventID: event.EventID, OrderNo: event.OrderNo, UserID: event.UserID, ItemID: 99, ReservedAt: event.ReservedAt()}}
	handler, _ := NewConsumerHandler(jobs, &consumerProcessor{})
	for _, key := range []string{"wrong-key", event.MessageKey()} {
		if err := handler.Handle(context.Background(), key, payload); !errors.Is(err, ErrMessageIdentityConflict) {
			t.Fatalf("Handle(%q) error = %v", key, err)
		}
	}
}

func TestConsumerHandlerReturnsProcessingErrorForRedelivery(t *testing.T) {
	event, payload := messageFixture(t)
	want := errors.New("mysql temporary error")
	jobs := &consumerJobs{job: seckill.OrderJob{ID: 1, EventID: event.EventID, OrderNo: event.OrderNo, UserID: event.UserID, ItemID: event.ItemID, ReservedAt: event.ReservedAt()}}
	handler, _ := NewConsumerHandler(jobs, &consumerProcessor{err: want})
	if err := handler.Handle(context.Background(), event.MessageKey(), payload); !errors.Is(err, want) {
		t.Fatalf("Handle() error = %v", err)
	}
}
