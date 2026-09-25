package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jagarkarlo/checkride/internal/httpapi"
)

func main() {
	if err := run(); err != nil {
		slog.Error("Checkride API stopped with an error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	address := os.Getenv("CHECKRIDE_ADDR")
	if address == "" {
		address = ":8080"
	}

	stopSignals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", address, err)
	}
	server := httpapi.NewServer(address)
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()

	slog.Info("Checkride API listening", "address", listener.Addr().String())

	select {
	case err := <-serveErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	case <-stopSignals.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownContext); err != nil {
			_ = server.Close()
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		if err := <-serveErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve after shutdown: %w", err)
		}
		slog.Info("Checkride API stopped")
		return nil
	}
}
