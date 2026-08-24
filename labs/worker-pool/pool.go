package main

import (
	"context"
	"fmt"
	"sync"
)

type Job func(context.Context) (int, error)

type Result struct {
	Value int
	Err   error
}

// Run starts a bounded number of workers. The unbuffered result send supplies
// backpressure when the consumer is slower than the workers.
func Run(ctx context.Context, workers int, jobs <-chan Job) <-chan Result {
	results := make(chan Result)
	if workers < 1 {
		close(results)
		return results
	}

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(workerID int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case job, ok := <-jobs:
					if !ok {
						return
					}
					result := safelyRun(ctx, job, workerID)
					select {
					case results <- result:
					case <-ctx.Done():
						return
					}
				}
			}
		}(i)
	}

	go func() {
		wg.Wait()
		close(results)
	}()
	return results
}

func safelyRun(ctx context.Context, job Job, workerID int) (result Result) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result.Err = fmt.Errorf("worker %d recovered panic: %v", workerID, recovered)
		}
	}()
	result.Value, result.Err = job(ctx)
	return result
}
