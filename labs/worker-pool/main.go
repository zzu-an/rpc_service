package main

import (
	"context"
	"fmt"
	"time"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	jobs := make(chan Job)
	go func() {
		defer close(jobs)
		for i := 1; i <= 8; i++ {
			value := i
			jobs <- func(ctx context.Context) (int, error) {
				select {
				case <-time.After(40 * time.Millisecond):
					return value * value, nil
				case <-ctx.Done():
					return 0, context.Cause(ctx)
				}
			}
		}
	}()

	for result := range Run(ctx, 3, jobs) {
		fmt.Printf("value=%d err=%v\n", result.Value, result.Err)
	}
}
