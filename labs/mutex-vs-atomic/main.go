package main

import (
	"flag"
	"fmt"
	"sync"
)

type counter interface {
	Inc()
	Value() int64
}

func incrementConcurrently(c counter, goroutines, perGoroutine int) int64 {
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				c.Inc()
			}
		}()
	}
	wg.Wait()
	return c.Value()
}

func main() {
	unsafeMode := flag.Bool("unsafe", false, "run the deliberately racy counter")
	flag.Parse()

	fmt.Printf("mutex=%d\n", incrementConcurrently(&MutexCounter{}, 20, 10_000))
	fmt.Printf("atomic=%d\n", incrementConcurrently(&AtomicCounter{}, 20, 10_000))
	if *unsafeMode {
		fmt.Printf("unsafe=%d\n", incrementConcurrently(&UnsafeCounter{}, 20, 10_000))
	}
}
