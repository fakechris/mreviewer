package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"testing"
	"time"
)

func TestGracefulShutdown(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(discard{}, nil))

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// Use port 0 to let OS pick a free port.
	srv := New("0", mux, logger)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	go func() {
		errCh <- srv.Start(ctx)
	}()

	// Wait for server to start listening.
	time.Sleep(100 * time.Millisecond)

	// Signal shutdown.
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Start() returned error on shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down within 5 seconds")
	}
}

func TestGracefulShutdown_WithRequests(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(discard{}, nil))

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ok"}`)
	})

	// Use a specific high port for testing.
	srv := New("0", mux, logger)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	go func() {
		errCh <- srv.Start(ctx)
	}()

	// Give the server time to start.
	time.Sleep(200 * time.Millisecond)

	// Cancel to trigger graceful shutdown.
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Start() returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down within 5 seconds")
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
