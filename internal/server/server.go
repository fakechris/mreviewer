// Package server provides a production HTTP server with graceful shutdown.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

const (
	defaultReadTimeout     = 10 * time.Second
	defaultWriteTimeout    = 30 * time.Second
	defaultIdleTimeout     = 60 * time.Second
	defaultShutdownTimeout = 15 * time.Second
)

// Server wraps an http.Server with graceful lifecycle management.
type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
}

// New creates a Server that will listen on the given port and serve handler.
func New(port string, handler http.Handler, logger *slog.Logger) *Server {
	return &Server{
		httpServer: &http.Server{
			Addr:         net.JoinHostPort("", port),
			Handler:      handler,
			ReadTimeout:  defaultReadTimeout,
			WriteTimeout: defaultWriteTimeout,
			IdleTimeout:  defaultIdleTimeout,
		},
		logger: logger,
	}
}

// Start begins listening. It blocks until ctx is cancelled, at which point
// it initiates a graceful shutdown. Returns nil on clean shutdown.
func (s *Server) Start(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		s.logger.Info("server starting", "addr", s.httpServer.Addr)
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("server listen: %w", err)
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return err
		}
		return nil
	case <-ctx.Done():
		return s.shutdown()
	}
}

// shutdown performs a graceful shutdown with a timeout.
func (s *Server) shutdown() error {
	s.logger.Info("server shutting down gracefully")
	ctx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
	defer cancel()
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("server shutdown: %w", err)
	}
	s.logger.Info("server stopped cleanly")
	return nil
}
