package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimiterBurstAndRefill(t *testing.T) {
	start := time.Unix(0, 0)
	limiter := New(10, 2, start)
	if !limiter.Allow(start) || !limiter.Allow(start) || limiter.Allow(start) {
		t.Fatal("initial burst was not enforced")
	}
	if !limiter.Allow(start.Add(100 * time.Millisecond)) {
		t.Fatal("one token should have refilled")
	}
}

func TestLimiterConcurrent(t *testing.T) {
	now := time.Unix(0, 0)
	limiter := New(1, 20, now)
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if limiter.Allow(now) {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := allowed.Load(); got != 20 {
		t.Fatalf("allowed=%d, want 20", got)
	}
}
