package main

import "context"

// LeakyGenerator cannot stop when its consumer leaves: the send eventually
// blocks forever. It intentionally models a common production bug.
func LeakyGenerator() <-chan int {
	values := make(chan int)
	go func() {
		for i := 0; ; i++ {
			values <- i
		}
	}()
	return values
}

// Generator observes cancellation both while computing the next value and
// while blocked on delivery. done exists only to make lifecycle testable.
func Generator(ctx context.Context) (<-chan int, <-chan struct{}) {
	values := make(chan int)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(values)
		for i := 0; ; i++ {
			select {
			case values <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	return values, done
}
