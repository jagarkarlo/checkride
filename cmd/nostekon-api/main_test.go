package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
