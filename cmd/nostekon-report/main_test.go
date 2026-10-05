package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/jagarkarlo/nostekon/internal/cli"
)

func TestRunReportCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"../../examples/runs/mlflow-namespace-loss.run.json"}, &stdout, &stderr)
	var report struct {
		Verdict string `json:"verdict"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if code != 0 || report.Verdict != "verified" {
		t.Fatalf("exit=%d verdict=%s stderr=%s", code, report.Verdict, stderr.String())
	}
}

func TestRunReportUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := cli.Run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d, want usage code 2", code)
	}
	stderr.Reset()
	if code := cli.Run([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("help exit = %d, want 0", code)
	}
}
