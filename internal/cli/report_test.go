package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestRunWritesVerifiedReport(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"../../examples/runs/mlflow-namespace-loss.run.json"}, &stdout, &stderr)
	var report struct {
		Verdict string `json:"verdict"`
		RPO     struct {
			Seconds float64 `json:"seconds"`
			Lost    int     `json:"lost"`
		} `json:"rpo"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report %s: %v", stdout.String(), err)
	}
	if code != 0 || report.Verdict != "verified" || report.RPO.Seconds != 38 || report.RPO.Lost != 75 || stderr.Len() > 0 {
		t.Fatalf("exit=%d report=%+v stderr=%s", code, report, stderr.String())
	}
}

func TestRunExitCodesForFailedAndVerified(t *testing.T) {
	for _, tc := range []struct {
		file string
		want int
	}{
		{"../../examples/runs/crud-cluster-loss.run.json", 1},
		{"../../examples/runs/mlflow-namespace-loss.run.json", 0},
	} {
		t.Run(tc.file, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run([]string{tc.file}, &stdout, &stderr)
			if code != tc.want {
				t.Fatalf("exit = %d, want %d; report=%s stderr=%s", code, tc.want, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunExitsTwoForIncompleteReport(t *testing.T) {
	const evidence = `{"apiVersion":"checkride/v1alpha1","kind":"DrillRun","metadata":{"name":"incomplete"},"spec":{"upTo":"V2"},"status":{"failureAt":"2026-10-01T10:00:00Z","completedAt":"2026-10-01T10:00:30Z","checks":[{"level":"V0","name":"backup","passed":true},{"level":"V2","name":"health","passed":true}]}}`
	file := t.TempDir() + "/incomplete.json"
	if err := os.WriteFile(file, []byte(evidence), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run([]string{file}, &stdout, &stderr)
	var report struct {
		Verdict string `json:"verdict"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report %s: %v", stdout.String(), err)
	}
	if code != 2 || report.Verdict != "incomplete" {
		t.Fatalf("exit=%d verdict=%s report=%s stderr=%s", code, report.Verdict, stdout.String(), stderr.String())
	}
}

func TestRunValidatesEvidenceAndUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("usage exit=%d stderr=%q", code, stderr.String())
	}
	stderr.Reset()
	file := t.TempDir() + "/invalid.json"
	if err := os.WriteFile(file, []byte(`{"kind":"DrillRun"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{file}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "apiVersion:") {
		t.Fatalf("invalid evidence exit=%d stderr=%q", code, stderr.String())
	}
}
