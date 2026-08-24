package main

import (
	"context"
	"testing"
	"time"
)

func TestGeneratorStopsWhenConsumerLeaves(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	values, done := Generator(ctx)
	if got := <-values; got != 0 {
		t.Fatalf("first value=%d", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("generator goroutine leaked")
	}
}

func TestGeneratorStopsBeforeFirstConsumer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	_, done := Generator(ctx)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked send did not observe cancellation")
	}
}
