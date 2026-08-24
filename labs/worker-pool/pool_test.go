package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunBoundsConcurrency(t *testing.T) {
	const workerCount = 3
	jobs := make(chan Job, 12)
	var running atomic.Int32
	var maximum atomic.Int32
	for i := 0; i < cap(jobs); i++ {
		jobs <- func(context.Context) (int, error) {
			current := running.Add(1)
			defer running.Add(-1)
			for old := maximum.Load(); current > old && !maximum.CompareAndSwap(old, current); old = maximum.Load() {
			}
			time.Sleep(5 * time.Millisecond)
			return 1, nil
		}
	}
	close(jobs)

	count := 0
	for result := range Run(context.Background(), workerCount, jobs) {
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		count++
	}
	if count != cap(jobs) || maximum.Load() > workerCount {
		t.Fatalf("count=%d maximum=%d", count, maximum.Load())
	}
}

func TestRunRecoversJobPanic(t *testing.T) {
	jobs := make(chan Job, 1)
	jobs <- func(context.Context) (int, error) { panic("boom") }
	close(jobs)

	result := <-Run(context.Background(), 1, jobs)
	if result.Err == nil {
		t.Fatal("expected recovered panic error")
	}
}

func TestRunCancellationUnblocksResultSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	jobs := make(chan Job, 1)
	jobs <- func(context.Context) (int, error) { return 1, nil }
	close(jobs)
	results := Run(ctx, 1, jobs)
	cancel()

	select {
	case <-results:
	case <-time.After(time.Second):
		t.Fatal("pool did not stop after cancellation")
	}
}
