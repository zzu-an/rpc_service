package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestProcessDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	err := Process(ctx, 100, 10*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrInterrupted) {
		t.Fatalf("expected wrapped deadline error, got %v", err)
	}
}

func TestProcessCancelCause(t *testing.T) {
	cause := errors.New("client disconnected")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)

	err := Process(ctx, 1, time.Hour)
	if !errors.Is(err, cause) {
		t.Fatalf("expected cancellation cause, got %v", err)
	}
}
