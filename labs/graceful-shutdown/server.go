package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

type App struct {
	server          *http.Server
	shutdownTimeout time.Duration
	closeResources  func() error
}

func NewApp(handler http.Handler, shutdownTimeout time.Duration, closeResources func() error) *App {
	return &App{
		server: &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 2 * time.Second,
			IdleTimeout:       30 * time.Second,
		},
		shutdownTimeout: shutdownTimeout,
		closeResources:  closeResources,
	}
}

// Run stops accepting new connections when ctx is canceled, waits for in-flight
// handlers, and only then closes process resources.
func (a *App) Run(ctx context.Context, listener net.Listener) error {
	serveErr := make(chan error, 1)
	go func() { serveErr <- a.server.Serve(listener) }()

	select {
	case err := <-serveErr:
		return ignoreServerClosed(err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.shutdownTimeout)
	defer cancel()
	shutdownErr := a.server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		_ = a.server.Close()
	}
	serveResult := ignoreServerClosed(<-serveErr)

	var resourceErr error
	if a.closeResources != nil {
		resourceErr = a.closeResources()
	}
	return errors.Join(shutdownErr, serveResult, resourceErr)
}

func ignoreServerClosed(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(250 * time.Millisecond):
			_, _ = fmt.Fprintln(w, "completed")
		case <-r.Context().Done():
			http.Error(w, r.Context().Err().Error(), http.StatusRequestTimeout)
		}
	})
	return mux
}
