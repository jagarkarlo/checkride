package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jagarkarlo/nostekon/internal/attest"
	"github.com/jagarkarlo/nostekon/internal/httpapi"
)

func main() {
	if err := run(); err != nil {
		slog.Error("Nostekon API stopped with an error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	address := os.Getenv("NOSTEKON_ADDR")
	if address == "" {
		address = ":8080"
	}

	stopSignals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var studio fs.FS
	if directory := os.Getenv("NOSTEKON_STUDIO_DIR"); directory != "" {
		root, err := os.OpenRoot(directory)
		if err != nil {
			return fmt.Errorf("open Studio directory: %w", err)
		}
		defer root.Close()
		studio = root.FS()
		index, err := fs.Stat(studio, "index.html")
		if err != nil {
			return fmt.Errorf("read Studio index: %w", err)
		}
		if !index.Mode().IsRegular() {
			return fmt.Errorf("Studio index.html must be a regular file")
		}
		slog.Info("serving Studio", "directory", directory)
	}

	var trustedKeys map[string]ed25519.PublicKey
	var err error
	if directory := os.Getenv("NOSTEKON_TRUSTED_KEYS_DIR"); directory != "" {
		trustedKeys, err = attest.LoadTrustedPublicKeys(directory)
		if err != nil {
			return fmt.Errorf("load trusted evidence keys: %w", err)
		}
		if len(trustedKeys) == 0 {
			return fmt.Errorf("load trusted evidence keys: directory %q contains no PEM public keys", directory)
		}
		slog.Info("loaded trusted evidence keys", "count", len(trustedKeys), "directory", directory)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", address, err)
	}
	server := httpapi.NewServerWithStudio(address, trustedKeys, studio)
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()

	slog.Info("Nostekon API listening", "address", listener.Addr().String())

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
		slog.Info("Nostekon API stopped")
		return nil
	}
}
