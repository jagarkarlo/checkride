package metrics

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jagarkarlo/nostekon/internal/verify"
)

func loadRun(t *testing.T, name string) *verify.Evidence {
	t.Helper()
	data, err := os.ReadFile("../../examples/runs/" + name)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := verify.ParseEvidence(data)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return evidence
}

func sampleMap(samples []Sample) map[string]float64 {
	out := make(map[string]float64, len(samples))
	for _, sample := range samples {
		out[sample.Name] = sample.Value
	}
	return out
}

func TestFromReportOfVerifiedRun(t *testing.T) {
	report := verify.Build(loadRun(t, "mlflow-namespace-loss.run.json"))
	samples := sampleMap(FromReport(report))

	want := map[string]float64{
		"nostekon_drill_verified":           1,
		"nostekon_drill_requested_level":    4,
		"nostekon_drill_deepest_level":      4,
		"nostekon_recovery_time_seconds":    703,
		"nostekon_recovery_time_met":        1,
		"nostekon_data_loss_seconds":        38,
		"nostekon_data_loss_met":            1,
		"nostekon_acknowledged_writes_lost": 75,
		"nostekon_evidence_verified":        0,
	}
	for name, value := range want {
		got, ok := samples[name]
		if !ok {
			t.Fatalf("missing sample %s", name)
		}
		if got != value {
			t.Errorf("%s = %v, want %v", name, got, value)
		}
	}
}

func TestFromReportWithoutLedgerReportsUnmeasuredNotZero(t *testing.T) {
	evidence := loadRun(t, "mlflow-namespace-loss.run.json")
	evidence.Status.Ledger = nil
	report := verify.Build(evidence)
	samples := sampleMap(FromReport(report))

	if samples["nostekon_data_loss_seconds"] != -1 {
		t.Errorf("data loss seconds = %v, want -1 (unmeasured)", samples["nostekon_data_loss_seconds"])
	}
	if samples["nostekon_acknowledged_writes_lost"] != -1 {
		t.Errorf("writes lost = %v, want -1 (unmeasured)", samples["nostekon_acknowledged_writes_lost"])
	}
}

func TestFromReportDeepestLevelReflectsAFailedDrill(t *testing.T) {
	report := verify.Build(loadRun(t, "crud-cluster-loss.run.json"))
	samples := sampleMap(FromReport(report))
	if samples["nostekon_drill_deepest_level"] != 2 {
		t.Errorf("deepest level = %v, want 2 (V2)", samples["nostekon_drill_deepest_level"])
	}
	if samples["nostekon_drill_verified"] != 0 {
		t.Errorf("verified = %v, want 0", samples["nostekon_drill_verified"])
	}
}

func TestFormatIsValidExpositionText(t *testing.T) {
	text := Format([]Sample{{"nostekon_drill_verified", "help text", 1}, {"nostekon_recovery_time_seconds", "help", 16.5}})
	for _, want := range []string{
		"# HELP nostekon_drill_verified help text\n",
		"# TYPE nostekon_drill_verified gauge\n",
		"nostekon_drill_verified 1\n",
		"nostekon_recovery_time_seconds 16.5\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q; got:\n%s", want, text)
		}
	}
}

func TestFormatValueTrimsTrailingZerosWithoutTruncatingIntegers(t *testing.T) {
	for value, want := range map[float64]string{0: "0", -1: "-1", 38: "38", 16.5: "16.5", 100: "100", -0.5: "-0.5"} {
		if got := formatValue(value); got != want {
			t.Errorf("formatValue(%v) = %q, want %q", value, got, want)
		}
	}
}

func TestPushSendsExpositionTextToTheGroupingKeyPath(t *testing.T) {
	var gotMethod, gotPath, gotContentType, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotMethod = request.Method
		gotPath = request.URL.Path
		gotContentType = request.Header.Get("Content-Type")
		body := make([]byte, 4096)
		n, _ := request.Body.Read(body)
		gotBody = string(body[:n])
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	err := Push(server.URL, "nostekon", map[string]string{"instance": "mlflow-namespace-loss"}, []Sample{{"nostekon_drill_verified", "h", 1}})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %s, want PUT", gotMethod)
	}
	if gotPath != "/metrics/job/nostekon/instance/mlflow-namespace-loss" {
		t.Errorf("path = %s", gotPath)
	}
	if !strings.HasPrefix(gotContentType, "text/plain") {
		t.Errorf("content-type = %s", gotContentType)
	}
	if !strings.Contains(gotBody, "nostekon_drill_verified 1") {
		t.Errorf("body = %q", gotBody)
	}
}

func TestPushSortsMultipleLabelsIntoTheGroupingKey(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotPath = request.URL.Path
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := Push(server.URL, "nostekon", map[string]string{"scenario": "namespace-loss", "instance": "run-1"}, nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if gotPath != "/metrics/job/nostekon/instance/run-1/scenario/namespace-loss" {
		t.Errorf("path = %s, want labels sorted by key", gotPath)
	}
}

func TestPushReturnsErrorOnNonSuccessStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
		writer.Write([]byte("boom"))
	}))
	defer server.Close()

	err := Push(server.URL, "nostekon", map[string]string{"instance": "x"}, nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want it to mention the gateway's response", err)
	}
}

func TestPushRejectsEmptyJobAndSlashInLabels(t *testing.T) {
	if err := Push("http://example.invalid", "", map[string]string{"instance": "x"}, nil); err == nil {
		t.Fatal("expected an error for an empty job name")
	}
	if err := Push("http://example.invalid", "nostekon", map[string]string{"instance": "a/b"}, nil); err == nil {
		t.Fatal("expected an error for a label value containing '/'")
	}
	if err := Push("http://example.invalid", "nostekon", map[string]string{"instance": ""}, nil); err == nil {
		t.Fatal("expected an error for an empty label value")
	}
}
