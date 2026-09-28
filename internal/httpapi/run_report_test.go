package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
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

func TestRunReportStatusCodes(t *testing.T) {
	cases := []struct {
		name, body, contentType string
		want                    int
	}{
		{"wrong media type", `{}`, "text/plain", http.StatusUnsupportedMediaType},
		{"malformed JSON", `{"kind":`, "application/json", http.StatusBadRequest},
		{"unknown field", `{"surprise": true}`, "application/json", http.StatusBadRequest},
		{"trailing value", `{} {}`, "application/json", http.StatusBadRequest},
		{"invalid evidence", `{"apiVersion": "checkride/v1alpha1", "kind": "DrillRun", "metadata": {"name": "x"}}`, "application/json", http.StatusUnprocessableEntity},
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
