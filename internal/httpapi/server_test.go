package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jagarkarlo/nostekon/internal/buildinfo"
)

func TestAppInfoReportsBuildAndActualCapabilities(t *testing.T) {
	for _, withStudio := range []bool{false, true} {
		var studio fstest.MapFS
		if withStudio {
			studio = fstest.MapFS{"index.html": {Data: []byte("Nostekon")}}
		}
		var handler http.Handler = NewHandler()
		if withStudio {
			handler = NewHandlerWithStudio(nil, studio)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/info", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("info status = %d", response.Code)
		}
		var info struct {
			Build        buildinfo.Info `json:"build"`
			Capabilities struct {
				Studio                bool `json:"studio"`
				LabExecution          bool `json:"labExecution"`
				SignatureVerification bool `json:"signatureVerification"`
			} `json:"capabilities"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &info); err != nil {
			t.Fatal(err)
		}
		if info.Build != buildinfo.Current() || info.Capabilities.Studio != withStudio || info.Capabilities.LabExecution || !info.Capabilities.SignatureVerification {
			t.Fatalf("incorrect app info: %+v", info)
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("capability snapshot must not be cached")
		}
		wrongMethod := httptest.NewRecorder()
		handler.ServeHTTP(wrongMethod, httptest.NewRequest(http.MethodPost, "/api/v1/info", nil))
		if wrongMethod.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST info status = %d", wrongMethod.Code)
		}
	}
}

func TestStudioAndAPIServeFromOneOrigin(t *testing.T) {
	studio := fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>Nostekon</title>")},
		"assets/app.js": {Data: []byte("window.nostekon = true;")},
	}
	handler := NewHandlerWithStudio(nil, studio)
	for _, test := range []struct {
		path   string
		status int
		body   string
	}{
		{"/", http.StatusOK, "<!doctype html><title>Nostekon</title>"},
		{"/assets/app.js", http.StatusOK, "window.nostekon = true;"},
		{"/healthz", http.StatusNoContent, ""},
		{"/readyz", http.StatusNoContent, ""},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != test.status || response.Body.String() != test.body {
				t.Fatalf("GET %s = %d %q, want %d %q", test.path, response.Code, response.Body.String(), test.status, test.body)
			}
		})
	}
}

func TestHealthEndpoints(t *testing.T) {
	handler := NewHandler()

	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
			}
		})
	}
}

func TestStudioDoesNotExposeDirectoriesHiddenFilesOrAPIFallback(t *testing.T) {
	studio := fstest.MapFS{
		"index.html":           {Data: []byte("Nostekon")},
		"assets/app.js":        {Data: []byte("app")},
		".env":                 {Data: []byte("private")},
		"assets/.private/file": {Data: []byte("private")},
		"api/unknown":          {Data: []byte("not an API response")},
	}
	handler := NewHandlerWithStudio(nil, studio)
	for _, path := range []string{"/missing.js", "/assets/", "/.env", "/assets/.private/file", "/api/unknown"} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusNotFound {
				t.Fatalf("GET %s = %d, want 404", path, response.Code)
			}
		})
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST / = %d, want 405", response.Code)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("Studio security/cache headers = %v", response.Header())
	}
}

func TestStudioPreservesAPIMethodHandling(t *testing.T) {
	handler := NewHandlerWithStudio(nil, fstest.MapFS{"index.html": {Data: []byte("Nostekon")}})
	for _, test := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/runs/report"},
		{http.MethodGet, "/api/v1/drills/validate"},
		{http.MethodPost, "/api/v1/schemas/drillrun"},
		{http.MethodPost, "/healthz"},
	} {
		t.Run(test.method+test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
			if response.Code != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s = %d, want 405", test.method, test.path, response.Code)
			}
		})
	}
}

func TestHealthEndpointsRejectUnsupportedMethods(t *testing.T) {
	handler := NewHandler()

	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, path, nil)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

func TestDrillRunSchemaEndpoint(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/schemas/drillrun", nil)
	response := httptest.NewRecorder()
	NewHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "application/schema+json" {
		t.Fatalf("Content-Type = %q", got)
	}
	var document map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	if document["title"] != "Nostekon DrillRun" {
		t.Fatalf("schema title = %v", document["title"])
	}
}

func TestNewServerUsesBoundedTimeouts(t *testing.T) {
	server := NewServer(":8080")

	if server.ReadHeaderTimeout != 5*time.Second {
		t.Errorf("ReadHeaderTimeout = %s, want 5s", server.ReadHeaderTimeout)
	}
	if server.ReadTimeout != 15*time.Second {
		t.Errorf("ReadTimeout = %s, want 15s", server.ReadTimeout)
	}
	if server.WriteTimeout != 30*time.Second {
		t.Errorf("WriteTimeout = %s, want 30s", server.WriteTimeout)
	}
	if server.IdleTimeout != 60*time.Second {
		t.Errorf("IdleTimeout = %s, want 60s", server.IdleTimeout)
	}
}

func TestServerServesReadinessAndShutsDown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	server := NewServer(listener.Addr().String())
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()
	t.Cleanup(func() { _ = server.Close() })

	response, err := http.Get("http://" + listener.Addr().String() + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("GET /readyz status = %d, want %d", response.StatusCode, http.StatusNoContent)
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		t.Fatalf("server shutdown: %v", err)
	}
	if err := <-serveErrors; !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("server.Serve() error = %v, want %v", err, http.ErrServerClosed)
	}
}
