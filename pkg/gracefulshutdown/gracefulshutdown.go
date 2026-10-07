package gracefulshutdown

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
)

// Manager handles graceful shutdown of services
type Manager struct {
	logger    *zap.SugaredLogger
	done      chan struct{}
	closeOnce sync.Once
}

// NewGracefulShutdown creates a new shutdown manager
func NewGracefulShutdown(logger *zap.SugaredLogger) *Manager {
	return &Manager{
		logger: logger,
		done:   make(chan struct{}),
	}
}

// Wait blocks until shutdown is triggered
func (m *Manager) Wait() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)
	<-sigs
	m.logger.Info("Shutdown signal received")
	m.closeDone()
}

// ShutdownContext returns a context that's canceled on shutdown
func (m *Manager) ShutdownContext(timeout time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	go func() {
		select {
		case <-m.done:
			cancel()
		case <-ctx.Done():
			cancel() // release WithTimeout timer when deadline hits first
		}
	}()
	return ctx
}

// Shutdown gracefully shuts down the provided HTTP server
func (m *Manager) Shutdown(server *http.Server) {
	// Set up notification for interrupts
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)

	// Wait for termination signal
	<-sigs

	m.logger.Info("Shutting down HTTP server")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		m.logger.Errorf("Server shutdown error: %v", err)
	}

	m.closeDone()
}

func (m *Manager) closeDone() {
	m.closeOnce.Do(func() {
		close(m.done)
	})
}
