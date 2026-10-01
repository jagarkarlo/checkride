package attest

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
)

func TestSignAndVerifyEvidence(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	evidence := []byte(`{"kind":"DrillRun","metadata":{"name":"restore"}}`)
	sidecar, err := Sign(evidence, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(evidence, sidecar, publicKey); err != nil {
		t.Fatalf("verify signed evidence: %v", err)
	}
	if sidecar.KeyID != KeyID(publicKey) {
		t.Fatalf("key id = %q, want %q", sidecar.KeyID, KeyID(publicKey))
	}
}

func TestVerifyRejectsChangedEvidence(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sidecar, err := Sign([]byte(`{"result":"original"}`), privateKey)
	if err != nil {
		t.Fatal(err)
	}
	err = Verify([]byte(`{"result":"changed"}`), sidecar, publicKey)
	if err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("verify error = %v, want digest mismatch", err)
	}
}

func TestVerifyRejectsUntrustedKeyAndMalformedSignature(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	evidence := []byte(`{"result":"original"}`)
	sidecar, err := Sign(evidence, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(evidence, sidecar, otherKey); err == nil {
		t.Fatal("verification accepted an untrusted public key")
	}
	sidecar.Signature = "not-base64!"
	if err := Verify(evidence, sidecar, publicKey); err == nil {
		t.Fatal("verification accepted a malformed signature")
	}
}

func TestSignRejectsInvalidPrivateKey(t *testing.T) {
	if _, err := Sign([]byte(`{}`), make([]byte, ed25519.SeedSize)); err == nil {
		t.Fatal("sign accepted a seed instead of a private key")
	}
}
