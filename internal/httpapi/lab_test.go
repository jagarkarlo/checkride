package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jagarkarlo/nostekon/internal/labjobs"
)

func labRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("X-Nostekon-Lab", "true")
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestLabDisabledByDefault(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler().ServeHTTP(response, labRequest(http.MethodGet, "/api/v1/lab", ""))
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"enabled":false`) {
		t.Fatalf("default capabilities = %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	NewHandler().ServeHTTP(response, labRequest(http.MethodPost, "/api/v1/lab/jobs", `{"writes":10,"rpoSeconds":60}`))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled start = %d", response.Code)
	}
}

func TestLabRequestBoundary(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("lab execution is Linux-only")
	}
	manager, err := labjobs.New("/bin/true", filepath.Join(t.TempDir(), "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	handler := NewHandlerWithLab(nil, nil, manager)
	for _, scenario := range []string{"foreign-origin", "foreign-host", "foreign-peer", "missing-header", "cross-site"} {
		t.Run(scenario, func(t *testing.T) {
			request := labRequest(http.MethodPost, "/api/v1/lab/jobs", `{"writes":10,"rpoSeconds":60}`)
			switch scenario {
			case "foreign-origin":
				request.Header.Set("Origin", "https://evil.example")
			case "foreign-host":
				request.Host = "evil.example:8080"
			case "foreign-peer":
				request.RemoteAddr = "192.0.2.1:12345"
			case "missing-header":
				request.Header.Del("X-Nostekon-Lab")
			case "cross-site":
				request.Header.Set("Sec-Fetch-Site", "cross-site")
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("%s start = %d", scenario, response.Code)
			}
		})
	}
	for _, body := range []string{`{"writes":0,"rpoSeconds":60}`, `{"writes":10,"rpoSeconds":60,"command":"evil"}`, `{"writes":10,"rpoSeconds":60} {}`, `{"writes":1.5,"rpoSeconds":60}`, `null`} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, labRequest(http.MethodPost, "/api/v1/lab/jobs", body))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid %s = %d", body, response.Code)
		}
	}
	request := labRequest(http.MethodPost, "/api/v1/lab/jobs", `{"writes":10,"rpoSeconds":60}`)
	request.Header.Set("Origin", "http://127.0.0.1:8080")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var job labjobs.Job
	if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusAccepted || job.ID == "" {
		t.Fatalf("valid start = %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, labRequest(http.MethodGet, "/api/v1/lab/jobs/"+job.ID, ""))
	if response.Code != http.StatusOK {
		t.Fatalf("poll = %d", response.Code)
	}
}

func TestLabEvidenceBundleDownload(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("lab execution is Linux-only")
	}
	directory := filepath.Join(t.TempDir(), "jobs")
	id := strings.Repeat("a", 24)
	suite := filepath.Join(directory, id, "suite")
	if err := os.MkdirAll(suite, 0700); err != nil {
		t.Fatal(err)
	}
	record := `{"version":1,"job":{"id":"aaaaaaaaaaaaaaaaaaaaaaaa","status":"completed","options":{"writes":10,"rpoSeconds":60},"startedAt":"2026-01-01T00:00:00Z","completedAt":"2026-01-01T00:01:00Z","exitCode":0,"log":"private output","logTruncated":false,"artifacts":[]}}`
	if err := os.WriteFile(filepath.Join(directory, id, "job.json"), []byte(record), 0600); err != nil {
		t.Fatal(err)
	}
	original := []byte("{\n \"status\": \"passed\"\n}\n")
	if err := os.WriteFile(filepath.Join(suite, "suite.json"), original, 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := labjobs.New("/bin/true", directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	handler := NewHandlerWithLab(nil, nil, manager)
	path := "/api/v1/lab/jobs/" + id + "/export"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, labRequest(http.MethodGet, path, ""))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/zip" || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Content-Disposition") != `attachment; filename="nostekon-lab-`+id+`.zip"` {
		t.Fatalf("export response = %d %v %s", response.Code, response.Header(), response.Body.String())
	}
	archive, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	if err != nil || len(archive.File) != 2 {
		t.Fatalf("export ZIP = %v, %v", archive, err)
	}
	file, err := archive.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	file.Close()
	if err != nil || archive.File[0].Name != "suite.json" || !bytes.Equal(data, original) {
		t.Fatalf("export changed original: %s, %v", data, err)
	}
	for _, scenario := range []string{"foreign-origin", "missing-header", "cross-site"} {
		request := labRequest(http.MethodGet, path, "")
		switch scenario {
		case "foreign-origin":
			request.Header.Set("Origin", "https://evil.example")
		case "missing-header":
			request.Header.Del("X-Nostekon-Lab")
		case "cross-site":
			request.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s export = %d", scenario, response.Code)
		}
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, labRequest(http.MethodGet, "/api/v1/lab/jobs/missing/export", ""))
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown export = %d", response.Code)
	}
	response = httptest.NewRecorder()
	NewHandler().ServeHTTP(response, labRequest(http.MethodGet, path, ""))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled export = %d", response.Code)
	}
}

func TestLabRecoveryRequiresExplicitLocalAcknowledgement(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("lab execution is Linux-only")
	}
	directory := filepath.Join(t.TempDir(), "jobs")
	id := strings.Repeat("a", 24)
	if err := os.MkdirAll(filepath.Join(directory, id), 0700); err != nil {
		t.Fatal(err)
	}
	record := `{"version":1,"job":{"id":"aaaaaaaaaaaaaaaaaaaaaaaa","status":"running","options":{"writes":10,"rpoSeconds":60},"startedAt":"2026-01-01T00:00:00Z","log":"","logTruncated":false,"artifacts":[]}}`
	if err := os.WriteFile(filepath.Join(directory, id, "job.json"), []byte(record), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := labjobs.New("/bin/true", directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	handler := NewHandlerWithLab(nil, nil, manager)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, labRequest(http.MethodPost, "/api/v1/lab/jobs", `{"writes":10,"rpoSeconds":60}`))
	if response.Code != http.StatusConflict {
		t.Fatalf("start before recovery = %d", response.Code)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, labRequest(http.MethodGet, "/api/v1/lab/jobs/"+id+"/export", ""))
	if response.Code != http.StatusConflict {
		t.Fatalf("export before recovery = %d", response.Code)
	}
	path := "/api/v1/lab/jobs/" + id + "/acknowledge-recovery"
	request := labRequest(http.MethodPost, path, `{"cleanupConfirmed":true}`)
	request.Header.Set("Origin", "https://evil.example")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("foreign recovery = %d", response.Code)
	}
	for _, body := range []string{`{}`, `{"cleanupConfirmed":false}`, `{"cleanupConfirmed":true,"resume":true}`, `{"cleanupConfirmed":true} {}`} {
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, labRequest(http.MethodPost, path, body))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid acknowledgement %s = %d", body, response.Code)
		}
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, labRequest(http.MethodPost, path, `{"cleanupConfirmed":true}`))
	var job labjobs.Job
	if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || job.RecoveryRequired || job.Status != "interrupted" {
		t.Fatalf("acknowledged job = %d %+v", response.Code, job)
	}
}
