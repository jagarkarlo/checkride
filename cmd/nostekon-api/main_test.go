package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPortableAppStartsOutsideItsDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix interrupt lifecycle; Windows binaries are cross-built separately")
	}
	context, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	root := t.TempDir()
	binary := filepath.Join(root, "nostekon-api")
	build := exec.CommandContext(context, "go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build portable binary: %v\n%s", err, output)
	}
	if err := os.Mkdir(filepath.Join(root, "studio"), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("<!doctype html><title>Portable Nostekon</title>")
	if err := os.WriteFile(filepath.Join(root, "studio", "index.html"), original, 0600); err != nil {
		t.Fatal(err)
	}
	process := exec.CommandContext(context, binary, "--addr", "127.0.0.1:0")
	process.Dir = t.TempDir()
	process.Env = append(os.Environ(), "NOSTEKON_ADDR=invalid", "NOSTEKON_STUDIO_DIR=", "NOSTEKON_LAB_EXECUTABLE=", "NOSTEKON_LAB_DATA_DIR=", "NOSTEKON_TRUSTED_KEYS_DIR=")
	logs, err := process.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Process.Kill() })
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(logs)
		for scanner.Scan() {
			if _, address, found := strings.Cut(scanner.Text(), "address="); found {
				select {
				case ready <- strings.Fields(address)[0]:
				default:
				}
			}
		}
	}()
	finished := make(chan error, 1)
	go func() { finished <- process.Wait() }()
	var address string
	select {
	case address = <-ready:
	case err := <-finished:
		t.Fatalf("app exited before readiness: %v", err)
	case <-context.Done():
		t.Fatal("app did not become ready")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	for _, path := range []string{"/", "/api/v1/info", "/healthz"} {
		response, err := client.Get("http://" + address + path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
		_ = response.Body.Close()
		if err != nil || response.StatusCode >= 300 {
			t.Fatalf("GET %s = %d, %v", path, response.StatusCode, err)
		}
		if path == "/" && !bytes.Equal(body, original) {
			t.Fatalf("Studio bytes changed: %q", body)
		}
		if path == "/api/v1/info" {
			var info struct {
				Capabilities struct {
					Studio       bool
					LabExecution bool
				}
			}
			if err := json.Unmarshal(body, &info); err != nil || !info.Capabilities.Studio || info.Capabilities.LabExecution {
				t.Fatalf("portable capabilities = %s, %v", body, err)
			}
		}
	}
	if err := process.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("unclean shutdown: %v", err)
		}
	case <-context.Done():
		t.Fatal("app did not shut down")
	}
}

func TestVersionAndHelpDoNotStartServer(t *testing.T) {
	t.Setenv("NOSTEKON_ADDR", "invalid-address")
	t.Setenv("NOSTEKON_STUDIO_DIR", "/missing/studio")
	for _, argument := range []string{"--version", "--help"} {
		var output bytes.Buffer
		if err := command([]string{argument}, &output); err != nil {
			t.Fatalf("%s starts server or fails: %v", argument, err)
		}
		if !strings.Contains(output.String(), "Nostekon") {
			t.Fatalf("%s output = %q", argument, output.String())
		}
	}
}

func TestLaunchOptionsValidateOverrides(t *testing.T) {
	t.Setenv("NOSTEKON_ADDR", "127.0.0.1:8180")
	options, err := parseLaunchOptions([]string{"--addr", "127.0.0.1:8181", "--api-only"}, io.Discard)
	if err != nil || options.address != "127.0.0.1:8181" || !options.apiOnly {
		t.Fatalf("launch flags = %+v, %v", options, err)
	}
	for _, arguments := range [][]string{{"unexpected"}, {"--unknown"}, {"--api-only", "--studio-dir", "studio"}} {
		if _, err := parseLaunchOptions(arguments, io.Discard); err == nil {
			t.Fatalf("invalid arguments accepted: %v", arguments)
		}
	}
	t.Setenv("NOSTEKON_STUDIO_DIR", "/missing/studio")
	t.Setenv("NOSTEKON_LAB_EXECUTABLE", "")
	t.Setenv("NOSTEKON_LAB_DATA_DIR", "")
	err = runWithOptions(launchOptions{address: "invalid-address", apiOnly: true})
	if err == nil || !strings.Contains(err.Error(), "listen on") {
		t.Fatalf("API-only did not bypass invalid Studio: %v", err)
	}
}

func TestDefaultAddressIsLoopback(t *testing.T) {
	t.Setenv("NOSTEKON_ADDR", "")
	if got := listenAddress(); got != "127.0.0.1:8080" {
		t.Fatalf("default address = %q, want explicit loopback", got)
	}
	t.Setenv("NOSTEKON_ADDR", ":8180")
	if got := listenAddress(); got != ":8180" {
		t.Fatalf("configured address = %q, want unchanged container override", got)
	}
}

func TestStudioDirectoryBesideExecutable(t *testing.T) {
	t.Setenv("NOSTEKON_STUDIO_DIR", "")
	root := t.TempDir()
	executable := filepath.Join(root, "nostekon-api")
	if directory, err := studioDirectory(executable); err != nil || directory != "" {
		t.Fatalf("API-only selection = %q, %v", directory, err)
	}
	bundled := filepath.Join(root, "studio")
	if err := os.Mkdir(bundled, 0700); err != nil {
		t.Fatal(err)
	}
	if directory, err := studioDirectory(executable); err != nil || directory != bundled {
		t.Fatalf("bundled Studio selection = %q, %v; want %q", directory, err, bundled)
	}
	t.Setenv("NOSTEKON_STUDIO_DIR", "explicit/studio")
	if directory, err := studioDirectory(executable); err != nil || directory != "explicit/studio" {
		t.Fatalf("explicit Studio selection = %q, %v", directory, err)
	}
	t.Setenv("NOSTEKON_STUDIO_DIR", "")
	if err := os.Remove(bundled); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundled, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := studioDirectory(executable); err == nil {
		t.Fatal("invalid bundled Studio silently accepted")
	}
}

func TestRunReturnsBindError(t *testing.T) {
	t.Setenv("NOSTEKON_STUDIO_DIR", "")
	t.Setenv("NOSTEKON_LAB_EXECUTABLE", "")
	t.Setenv("NOSTEKON_LAB_DATA_DIR", "")
	t.Setenv("NOSTEKON_ADDR", "invalid-address")

	err := run()
	if err == nil || !strings.Contains(err.Error(), "listen on \"invalid-address\"") {
		t.Fatalf("run() error = %v, want a bind error", err)
	}
}

func TestRunRejectsUnsafeLabConfiguration(t *testing.T) {
	for _, scenario := range []string{"wildcard", "missing-data", "missing-executable", "invalid-executable"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("NOSTEKON_STUDIO_DIR", "")
			t.Setenv("NOSTEKON_ADDR", "127.0.0.1:invalid-port")
			t.Setenv("NOSTEKON_LAB_EXECUTABLE", "/missing/nostekon")
			t.Setenv("NOSTEKON_LAB_DATA_DIR", filepath.Join(t.TempDir(), "jobs"))
			switch scenario {
			case "wildcard":
				t.Setenv("NOSTEKON_ADDR", ":invalid-port")
			case "missing-data":
				t.Setenv("NOSTEKON_LAB_DATA_DIR", "")
			case "missing-executable":
				t.Setenv("NOSTEKON_LAB_EXECUTABLE", "")
			}
			err := run()
			if err == nil || !strings.Contains(err.Error(), "lab") {
				t.Fatalf("%s startup = %v", scenario, err)
			}
		})
	}
}

func TestRunRejectsInvalidStudioDirectory(t *testing.T) {
	for _, scenario := range []string{"missing-directory", "missing-index", "directory-index", "outside-symlink"} {
		t.Run(scenario, func(t *testing.T) {
			directory := t.TempDir()
			switch scenario {
			case "missing-directory":
				directory = filepath.Join(directory, "missing")
			case "directory-index":
				if err := os.Mkdir(filepath.Join(directory, "index.html"), 0700); err != nil {
					t.Fatal(err)
				}
			case "outside-symlink":
				outside := filepath.Join(t.TempDir(), "index.html")
				if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(directory, "index.html")); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("NOSTEKON_STUDIO_DIR", directory)
			t.Setenv("NOSTEKON_ADDR", "invalid-address")
			err := run()
			if err == nil || !strings.Contains(err.Error(), "Studio") {
				t.Fatalf("run() error = %v, want Studio startup error before binding", err)
			}
		})
	}
}
