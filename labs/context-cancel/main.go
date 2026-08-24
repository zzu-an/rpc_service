package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Millisecond)
	defer cancel()

	started := time.Now()
	err := Process(ctx, 10, 50*time.Millisecond)
	fmt.Printf("elapsed=%s err=%v deadline=%v\n", time.Since(started).Round(time.Millisecond), err, errors.Is(err, context.DeadlineExceeded))
}
