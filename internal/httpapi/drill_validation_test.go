package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const validDrillJSON = `{
	"apiVersion": "nostekon/v1alpha1",
	"kind": "Drill",
	"metadata": {"name": "demo"},
	"spec": {
		"scenario": "namespace-loss",
		"target": {"namespace": "shop", "cnpgCluster": "shop-db"},
		"restore": {"into": "separate-cluster"},
		"verify": {"upTo": "V4", "ledger": true},
		"objectives": {"rto": "15m", "rpo": "5m"}
	}
}`

type validationResponse struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

func postDrillValidation(body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/drills/validate", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	NewHandler().ServeHTTP(response, request)
	return response
}

func decodeValidationResponse(t *testing.T, response *httptest.ResponseRecorder) validationResponse {
	t.Helper()
	var result validationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, response.Body.String())
	}
	return result
}

func TestValidateDrillAcceptsValidSpec(t *testing.T) {
	response := postDrillValidation(validDrillJSON)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	result := decodeValidationResponse(t, response)
	if !result.Valid || len(result.Errors) != 0 || len(result.Warnings) != 0 {
		t.Fatalf("unexpected validation result: %+v", result)
	}
}

func TestValidateDrillAcceptsLegacyAPIVersion(t *testing.T) {
	legacy := strings.Replace(validDrillJSON, "nostekon/v1alpha1", "checkride/v1alpha1", 1)
	if result := decodeValidationResponse(t, postDrillValidation(legacy)); !result.Valid {
		t.Fatalf("legacy drill rejected: %+v", result)
	}
	unknown := strings.Replace(validDrillJSON, "nostekon/v1alpha1", "nostekon/v2", 1)
	if result := decodeValidationResponse(t, postDrillValidation(unknown)); result.Valid {
		t.Fatal("unknown apiVersion accepted")
	}
}

func TestValidateDrillRejectsV4WithoutEvidence(t *testing.T) {
	body := strings.Replace(validDrillJSON, `"ledger": true`, `"ledger": false`, 1)
	response := postDrillValidation(body)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusUnprocessableEntity, response.Body.String())
	}
	result := decodeValidationResponse(t, response)
	if result.Valid || len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "V4 verification needs") {
		t.Fatalf("unexpected validation result: %+v", result)
	}
}

func TestValidateDrillReportsWeakEvidenceWarnings(t *testing.T) {
	body := strings.Replace(validDrillJSON, `"into": "separate-cluster"`, `"into": "namespace"`, 1)
	body = strings.Replace(body, `"rto": "15m", "rpo": "5m"`, ``, 1)
	response := postDrillValidation(body)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	result := decodeValidationResponse(t, response)
	if !result.Valid || len(result.Warnings) != 2 {
		t.Fatalf("unexpected validation result: %+v", result)
	}
}

func TestValidateDrillRejectsMalformedAndUnknownJSON(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed", body: `{"apiVersion":`},
		{name: "unknown field", body: strings.Replace(validDrillJSON, `"scenario":`, `"unexpected": true, "scenario":`, 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := postDrillValidation(test.body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
			}
		})
	}
}

func TestValidateDrillRejectsMultipleJSONValues(t *testing.T) {
	response := postDrillValidation(validDrillJSON + ` {}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}

func TestValidateDrillRejectsNullRequiredObjects(t *testing.T) {
	cases := []struct {
		name string
		from string
		to   string
	}{
		{
			name: "restore",
			from: `"restore": {"into": "separate-cluster"},`,
			to:   `"restore": null,`,
		},
		{
			name: "objectives",
			from: `"objectives": {"rto": "15m", "rpo": "5m"}`,
			to:   `"objectives": null`,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			body := strings.Replace(validDrillJSON, test.from, test.to, 1)
			response := postDrillValidation(body)
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusUnprocessableEntity, response.Body.String())
			}
			result := decodeValidationResponse(t, response)
			if result.Valid || len(result.Errors) == 0 {
				t.Fatalf("unexpected validation result: %+v", result)
			}
		})
	}
}

func TestValidateDrillRejectsNaivePointInTime(t *testing.T) {
	body := strings.Replace(validDrillJSON, `"into": "separate-cluster"`, `"into": "separate-cluster", "pointInTime": "2026-10-01T12:00:00"`, 1)
	response := postDrillValidation(body)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusUnprocessableEntity, response.Body.String())
	}
	result := decodeValidationResponse(t, response)
	if result.Valid || !strings.Contains(strings.Join(result.Errors, " "), "UTC offset") {
		t.Fatalf("unexpected validation result: %+v", result)
	}
}

func TestValidateDrillLimitsRequestBody(t *testing.T) {
	response := postDrillValidation(strings.Repeat(" ", maxDrillRequestBytes+1))
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusRequestEntityTooLarge, response.Body.String())
	}
}

func TestValidateDrillTimeoutObjective(t *testing.T) {
	validBody := strings.Replace(validDrillJSON, `"rpo": "5m"`, `"rpo": "5m", "timeout": "45m"`, 1)
	response := postDrillValidation(validBody)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	result := decodeValidationResponse(t, response)
	if !result.Valid || len(result.Errors) != 0 {
		t.Fatalf("expected valid timeout, got: %+v", result)
	}

	invalidBody := strings.Replace(validDrillJSON, `"rpo": "5m"`, `"rpo": "5m", "timeout": "invalid-time"`, 1)
	response = postDrillValidation(invalidBody)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusUnprocessableEntity, response.Body.String())
	}
	result = decodeValidationResponse(t, response)
	if result.Valid || !strings.Contains(strings.Join(result.Errors, " "), "spec.objectives.timeout") {
		t.Fatalf("expected timeout error, got: %+v", result)
	}
}
