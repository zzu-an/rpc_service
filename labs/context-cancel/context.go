package main

import (
	"context"
	"errors"
	"time"
)

// ErrInterrupted distinguishes cancellation from a business failure while
// retaining context.Canceled/context.DeadlineExceeded in the error chain.
var ErrInterrupted = errors.New("work interrupted")

// Process simulates interruptible work. Every blocking point observes ctx.
func Process(ctx context.Context, steps int, stepDelay time.Duration) error {
	for i := 0; i < steps; i++ {
		select {
		case <-ctx.Done():
			return errors.Join(ErrInterrupted, context.Cause(ctx))
		case <-time.After(stepDelay):
		}
	}
	return nil
}

// WrongProcess demonstrates the bug: cancellation cannot interrupt Sleep.
func WrongProcess(_ context.Context, steps int, stepDelay time.Duration) {
	for i := 0; i < steps; i++ {
		time.Sleep(stepDelay)
	}
}
