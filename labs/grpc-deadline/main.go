package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	address := flag.String("listen", ":50051", "gRPC listen address")
	flag.Parse()

	listener, err := net.Listen("tcp", *address)
	if err != nil {
		log.Fatal(err)
	}
	stats := &CallStats{}
	server := NewServer(stats)
	serveDone := Serve(server, listener)
	log.Printf("gRPC lab listening on %s", listener.Addr())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-serveDone:
		log.Fatal(err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := GracefulStop(shutdownCtx, server); err != nil {
		log.Printf("forced stop: %v", err)
	}
	fmt.Printf("unary_calls=%d stream_calls=%d\n", stats.UnaryCalls.Load(), stats.StreamCalls.Load())
}
