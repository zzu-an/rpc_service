package mq

import (
	"context"
	"errors"
	"testing"
	"time"

	"service_rpc/internal/order"
	"service_rpc/internal/seckill"
)

type relayJobs struct {
	jobs              []seckill.OrderJob
	marked            []uint64
	scheduled         []uint64
	markErr           error
	scheduleErrorCode string
}

func (*relayJobs) EnsureJob(context.Context, seckill.EnsureJobInput) (seckill.OrderJob, bool, error) {
	return seckill.OrderJob{}, false, nil
}
func (*relayJobs) FindJobOwned(context.Context, uint64, string) (seckill.OrderJob, error) {
	return seckill.OrderJob{}, seckill.ErrJobNotFound
}
func (*relayJobs) FindOrderOwned(context.Context, uint64, string) (order.Order, error) {
	return order.Order{}, seckill.ErrJobNotFound
}
func (j *relayJobs) ListPendingJobs(context.Context, time.Time, int) ([]seckill.OrderJob, error) {
	return append([]seckill.OrderJob(nil), j.jobs...), nil
}
func (j *relayJobs) MarkJobPublished(_ context.Context, id uint64, _ time.Time) (bool, error) {
	if j.markErr != nil {
		return false, j.markErr
	}
	j.marked = append(j.marked, id)
	return true, nil
}
func (j *relayJobs) ScheduleJobPublishRetry(_ context.Context, id uint64, _ time.Time, code string) (bool, error) {
	j.scheduled = append(j.scheduled, id)
	j.scheduleErrorCode = code
	return true, nil
}
func (*relayJobs) MarkJobSucceeded(context.Context, uint64, time.Time) (bool, error) {
	return false, nil
}
func (*relayJobs) MarkJobFailed(context.Context, uint64, time.Time, string) (bool, error) {
	return false, nil
}

type relayProducer struct {
	keys   []string
	err    error
	closed bool
}

func (p *relayProducer) Publish(_ context.Context, key string, _ []byte) error {
	p.keys = append(p.keys, key)
	return p.err
}
func (p *relayProducer) Close() error { p.closed = true; return nil }

func TestRelayPublishesAndMarksAfterAck(t *testing.T) {
	jobs := &relayJobs{jobs: []seckill.OrderJob{{ID: 1, OrderNo: "S-1", Payload: []byte(`{}`)}}}
	producer := &relayProducer{}
	relay, err := NewRelay(jobs, producer, time.Millisecond, 10)
	if err != nil {
		t.Fatal(err)
	}
	count, err := relay.ProcessOnce(context.Background())
	if err != nil || count != 1 || len(producer.keys) != 1 || len(jobs.marked) != 1 {
		t.Fatalf("ProcessOnce() count=%d err=%v keys=%v marked=%v", count, err, producer.keys, jobs.marked)
	}
}

func TestRelaySchedulesPublishFailure(t *testing.T) {
	jobs := &relayJobs{jobs: []seckill.OrderJob{{ID: 2, OrderNo: "S-2", Payload: []byte(`{}`)}}}
	producer := &relayProducer{err: context.DeadlineExceeded}
	relay, _ := NewRelay(jobs, producer, time.Millisecond, 10)
	count, err := relay.ProcessOnce(context.Background())
	if err != nil || count != 0 || len(jobs.marked) != 0 || len(jobs.scheduled) != 1 || jobs.scheduleErrorCode != relayPublishErrorCode {
		t.Fatalf("count=%d err=%v marked=%v scheduled=%v code=%q", count, err, jobs.marked, jobs.scheduled, jobs.scheduleErrorCode)
	}
}

func TestRelayAckThenMarkFailureLeavesJobForRedelivery(t *testing.T) {
	jobs := &relayJobs{jobs: []seckill.OrderJob{{ID: 3, OrderNo: "S-3", Payload: []byte(`{}`)}}, markErr: errors.New("db unavailable")}
	producer := &relayProducer{}
	relay, _ := NewRelay(jobs, producer, time.Millisecond, 10)
	if _, err := relay.ProcessOnce(context.Background()); err == nil || len(producer.keys) != 1 {
		t.Fatalf("ProcessOnce() error=%v publish=%v", err, producer.keys)
	}
	// fake repository 仍返回同一 pending job；下一轮必须再次发布，证明失败方向是重复而非丢失。
	if _, err := relay.ProcessOnce(context.Background()); err == nil || len(producer.keys) != 2 {
		t.Fatalf("second ProcessOnce() error=%v publish=%v", err, producer.keys)
	}
}

func TestRelayHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	relay, _ := NewRelay(&relayJobs{}, &relayProducer{}, time.Millisecond, 1)
	if _, err := relay.ProcessOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ProcessOnce() error = %v", err)
	}
}
