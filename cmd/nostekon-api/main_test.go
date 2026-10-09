package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
