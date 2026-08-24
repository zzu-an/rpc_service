package main

import (
	"fmt"
	"time"
)

func main() {
	limiter := New(5, 3, time.Now())
	for i := 1; i <= 10; i++ {
		fmt.Printf("request=%02d allowed=%v\n", i, limiter.Allow(time.Now()))
		time.Sleep(100 * time.Millisecond)
	}
}
