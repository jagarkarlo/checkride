package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jagarkarlo/nostekon/internal/attest"
	"github.com/jagarkarlo/nostekon/internal/buildinfo"
	"github.com/jagarkarlo/nostekon/internal/httpapi"
	"github.com/jagarkarlo/nostekon/internal/labjobs"
)

func main() {
	if err := command(os.Args[1:], os.Stdout); err != nil {
		slog.Error("Nostekon API stopped with an error", "error", err)
		os.Exit(1)
	}
}

func command(arguments []string, output io.Writer) error {
	options, err := parseLaunchOptions(arguments, output)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if options.version {
		info := buildinfo.Current()
		_, err := fmt.Fprintf(output, "Nostekon %s (%s; %s; %s)\n", info.Version, info.Revision, info.Platform, info.GoVersion)
		return err
	}
	return runWithOptions(options)
}

func run() error {
	return runWithOptions(launchOptions{})
}

type launchOptions struct {
	address string
	studio  string
	apiOnly bool
	version bool
}

func parseLaunchOptions(arguments []string, output io.Writer) (launchOptions, error) {
	var options launchOptions
	flags := flag.NewFlagSet("nostekon-api", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&options.address, "addr", listenAddress(), "listen address (default loopback)")
	flags.StringVar(&options.studio, "studio-dir", "", "Studio directory; otherwise environment or bundled studio")
	flags.BoolVar(&options.apiOnly, "api-only", false, "disable Studio, including bundled discovery")
	flags.BoolVar(&options.version, "version", false, "print build information without starting")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(output, "Nostekon local evidence app\nUsage: nostekon-api [options]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(arguments); err != nil {
		return options, err
	}
	if flags.NArg() != 0 {
		return options, errors.New("unexpected positional arguments")
	}
	if options.apiOnly && options.studio != "" {
		return options, errors.New("--api-only and --studio-dir cannot be combined")
	}
	return options, nil
}

func runWithOptions(options launchOptions) error {
	address := options.address
	if address == "" {
		address = listenAddress()
	}

	stopSignals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	executablePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate app executable: %w", err)
	}
	directory := options.studio
	if options.apiOnly {
		directory = ""
	} else if directory == "" {
		directory, err = studioDirectory(executablePath)
		if err != nil {
			return err
		}
	}
	var studio fs.FS
	if directory != "" {
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

	var lab *labjobs.Manager
	executable, dataDirectory := os.Getenv("NOSTEKON_LAB_EXECUTABLE"), os.Getenv("NOSTEKON_LAB_DATA_DIR")
	if executable != "" || dataDirectory != "" {
		if executable == "" || dataDirectory == "" {
			return errors.New("lab execution requires both NOSTEKON_LAB_EXECUTABLE and NOSTEKON_LAB_DATA_DIR")
		}
		if !httpapi.LoopbackAddress(address) {
			return errors.New("lab execution requires an explicit loopback NOSTEKON_ADDR")
		}
		var err error
		lab, err = labjobs.New(executable, dataDirectory)
		if err != nil {
			return fmt.Errorf("configure lab execution: %w", err)
		}
		defer lab.Close()
		slog.Info("local lab execution enabled", "directory", dataDirectory)
	}

	var trustedKeys map[string]ed25519.PublicKey
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
	if lab != nil {
		server.Handler = httpapi.NewHandlerWithLab(trustedKeys, studio, lab)
	}
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

func listenAddress() string {
	if address := os.Getenv("NOSTEKON_ADDR"); address != "" {
		return address
	}
	return "127.0.0.1:8080"
}

func studioDirectory(executablePath string) (string, error) {
	if directory := os.Getenv("NOSTEKON_STUDIO_DIR"); directory != "" {
		return directory, nil
	}
	directory := filepath.Join(filepath.Dir(executablePath), "studio")
	info, err := os.Stat(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect bundled Studio: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("bundled Studio must be a directory")
	}
	return directory, nil
}
