package main

import (
	"context"
	"errors"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	listener, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("listening on http://localhost%s; send SIGINT to stop", listener.Addr())

	app := NewApp(Routes(), 5*time.Second, func() error {
		log.Print("database/MQ resources closed")
		return nil
	})
	if err := app.Run(ctx, listener); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
