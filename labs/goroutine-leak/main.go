package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"time"
)

func main() {
	mode := flag.String("mode", "fixed", "fixed or leak")
	count := flag.Int("count", 100, "number of generators")
	flag.Parse()

	baseline := runtime.NumGoroutine()
	for i := 0; i < *count; i++ {
		if *mode == "leak" {
			values := LeakyGenerator()
			<-values
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		values, done := Generator(ctx)
		<-values
		cancel()
		<-done
	}
	time.Sleep(20 * time.Millisecond)
	fmt.Printf("mode=%s baseline=%d current=%d\n", *mode, baseline, runtime.NumGoroutine())
	if *mode == "leak" {
		_ = pprof.Lookup("goroutine").WriteTo(os.Stdout, 1)
	}
}
