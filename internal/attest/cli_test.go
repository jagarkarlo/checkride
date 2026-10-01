package attest

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunKeygenSignAndVerify(t *testing.T) {
	directory := t.TempDir()
	privatePath := filepath.Join(directory, "signing-key.pem")
	publicPath := filepath.Join(directory, "trusted-key.pem")
	evidencePath := filepath.Join(directory, "run.json")
	sidecarPath := filepath.Join(directory, "run.attestation.json")
	evidence, err := os.ReadFile("../../examples/runs/k3d-postgresql.run.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(evidencePath, evidence, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"keygen", "--private", privatePath, "--public", publicPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("keygen exit=%d stderr=%s", code, stderr.String())
	}
	if info, err := os.Stat(privatePath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("private key mode or stat: info=%v err=%v", info, err)
	}
	if code := Run([]string{"sign", "--evidence", evidencePath, "--key", privatePath, "--output", sidecarPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("sign exit=%d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	if code := Run([]string{"verify", "--evidence", evidencePath, "--attestation", sidecarPath, "--trusted-key", publicPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("verify exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"verified":true`) {
		t.Fatalf("verify output = %s", stdout.String())
	}

	if err := os.WriteFile(evidencePath, append(evidence, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if code := Run([]string{"verify", "--evidence", evidencePath, "--attestation", sidecarPath, "--trusted-key", publicPath}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "digest") {
		t.Fatalf("tampered verify exit=%d stderr=%s", code, stderr.String())
	}
}

func TestRunRejectsExistingOutputAndInvalidEvidence(t *testing.T) {
	directory := t.TempDir()
	privatePath := filepath.Join(directory, "private.pem")
	publicPath := filepath.Join(directory, "public.pem")
	if err := WriteKeyPair(privatePath, publicPath); err != nil {
		t.Fatal(err)
	}
	invalidPath := filepath.Join(directory, "invalid.json")
	if err := os.WriteFile(invalidPath, []byte(`{"kind":"not-a-DrillRun"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"sign", "--evidence", invalidPath, "--key", privatePath, "--output", filepath.Join(directory, "bad.attestation.json")}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "kind:") {
		t.Fatalf("invalid evidence exit=%d stderr=%s", code, stderr.String())
	}

	reserved := filepath.Join(directory, "existing.attestation.json")
	if err := os.WriteFile(reserved, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	evidencePath := filepath.Join(directory, "run.json")
	evidence, err := os.ReadFile("../../examples/runs/k3d-postgresql.run.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(evidencePath, evidence, 0o600); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if code := Run([]string{"sign", "--evidence", evidencePath, "--key", privatePath, "--output", reserved}, &stdout, &stderr); code != 1 {
		t.Fatalf("existing output exit=%d stderr=%s", code, stderr.String())
	}
	contents, err := os.ReadFile(reserved)
	if err != nil || string(contents) != "preserve" {
		t.Fatalf("existing output changed to %q; err=%v", contents, err)
	}
}

func TestRunUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("usage exit=%d stderr=%q", code, stderr.String())
	}
}
