package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestAppGracefulShutdown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var resourceClosed atomic.Bool
	app := NewApp(Routes(), time.Second, func() error {
		resourceClosed.Store(true)
		return nil
	})
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx, listener) }()

	response, err := http.Get("http://" + listener.Addr().String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if string(body) != "ok" {
		t.Fatalf("body=%q", body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not shut down")
	}
	if !resourceClosed.Load() {
		t.Fatal("resource closer was not called")
	}
}
