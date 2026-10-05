package httpapi

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jagarkarlo/nostekon/internal/attest"
)

func postRunReport(t *testing.T, body, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runs/report", strings.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	response := httptest.NewRecorder()
	NewHandler().ServeHTTP(response, request)
	return response
}

func TestRunReportBuildsReportForExampleRun(t *testing.T) {
	data, err := os.ReadFile("../../examples/runs/crud-cluster-loss.run.json")
	if err != nil {
		t.Fatal(err)
	}
	response := postRunReport(t, string(data), "application/json; charset=utf-8")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	var report struct {
		Verdict     string `json:"verdict"`
		FirstFailed string `json:"firstFailed"`
		Levels      []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"levels"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Verdict != "failed" || report.FirstFailed != "V3" || len(report.Levels) != 5 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestRunReportVerifiesAgainstServerTrustedKey(t *testing.T) {
	data, err := os.ReadFile("../../examples/runs/mlflow-namespace-loss.run.json")
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sidecar, err := attest.Sign(data, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runs/report", strings.NewReader(string(data)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Nostekon-Attestation", base64.StdEncoding.EncodeToString(encoded))
	response := httptest.NewRecorder()
	NewHandlerWithTrustedKeys(map[string]ed25519.PublicKey{attest.KeyID(publicKey): publicKey}).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var report struct {
		Provenance struct {
			Status         string `json:"status"`
			KeyID          string `json:"keyId"`
			EvidenceSHA256 string `json:"evidenceSHA256"`
		} `json:"provenance"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Provenance.Status != "verified" || report.Provenance.KeyID != sidecar.KeyID || report.Provenance.EvidenceSHA256 != sidecar.EvidenceSHA256 {
		t.Fatalf("provenance=%+v", report.Provenance)
	}
}

func TestRunReportRejectsTamperedAndUntrustedAttestations(t *testing.T) {
	original, err := os.ReadFile("../../examples/runs/mlflow-namespace-loss.run.json")
	if err != nil {
		t.Fatal(err)
	}
	trustedPublic, trustedPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPublic, otherPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trustedKeys := map[string]ed25519.PublicKey{attest.KeyID(trustedPublic): trustedPublic}
	for _, tc := range []struct {
		name      string
		body      []byte
		private   ed25519.PrivateKey
		wantError string
	}{
		{"tampered bytes", append(append([]byte(nil), original...), ' '), trustedPrivate, "digest"},
		{"untrusted signer", original, otherPrivate, "not trusted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sidecar, err := attest.Sign(original, tc.private)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(sidecar)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/runs/report", strings.NewReader(string(tc.body)))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(attestationHeader, base64.StdEncoding.EncodeToString(encoded))
			response := httptest.NewRecorder()
			NewHandlerWithTrustedKeys(trustedKeys).ServeHTTP(response, request)
			if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), tc.wantError) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	_ = otherPublic
}

func TestRunReportAttestationRequiresConfiguredTrustStore(t *testing.T) {
	data, err := os.ReadFile("../../examples/runs/mlflow-namespace-loss.run.json")
	if err != nil {
		t.Fatal(err)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sidecar, err := attest.Sign(data, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runs/report", strings.NewReader(string(data)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(attestationHeader, base64.StdEncoding.EncodeToString(encoded))
	response := httptest.NewRecorder()
	NewHandler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRunReportStatusCodes(t *testing.T) {
	cases := []struct {
		name, body, contentType string
		want                    int
	}{
		{"wrong media type", `{}`, "text/plain", http.StatusUnsupportedMediaType},
		{"malformed JSON", `{"kind":`, "application/json", http.StatusBadRequest},
		{"unknown field", `{"surprise": true}`, "application/json", http.StatusBadRequest},
		{"trailing value", `{} {}`, "application/json", http.StatusBadRequest},
		{"invalid evidence", `{"apiVersion": "nostekon/v1alpha1", "kind": "DrillRun", "metadata": {"name": "x"}}`, "application/json", http.StatusUnprocessableEntity},
		{"too large", `{"metadata": {"name": "` + strings.Repeat("a", maxRunRequestBytes) + `"}}`, "application/json", http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := postRunReport(t, tc.body, tc.contentType)
			if response.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%.200s", response.Code, tc.want, response.Body.String())
			}
			var problem reportProblem
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || len(problem.Errors) == 0 {
				t.Fatalf("expected an errors list, got %.200s", response.Body.String())
			}
		})
	}
}
