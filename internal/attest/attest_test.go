package attest

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
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
	if sidecar.APIVersion != "nostekon/attestation/v1alpha1" {
		t.Fatalf("apiVersion = %q", sidecar.APIVersion)
	}
}

func TestVerifyAcceptsLegacyCheckrideSignatures(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	evidence := []byte(`{"kind":"DrillRun"}`)
	digest := sha256.Sum256(evidence)
	message := append([]byte("checkride/drillrun-attestation/v1alpha1\x00"), digest[:]...)
	legacy := Sidecar{
		APIVersion:     "checkride/attestation/v1alpha1",
		Algorithm:      Algorithm,
		KeyID:          KeyID(publicKey),
		EvidenceSHA256: hex.EncodeToString(digest[:]),
		Signature:      base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, message)),
	}
	if err := Verify(evidence, legacy, publicKey); err != nil {
		t.Fatalf("legacy signature: %v", err)
	}
	relabelled := legacy
	relabelled.APIVersion = Version
	if err := Verify(evidence, relabelled, publicKey); err == nil {
		t.Fatal("a legacy signature must not verify under the current version")
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

func TestLoadTrustedPublicKeysUsesPEMKeyIDsAndIgnoresOtherFiles(t *testing.T) {
	directory := t.TempDir()
	keysDirectory := filepath.Join(directory, "keys")
	if err := os.Mkdir(keysDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(keysDirectory, "private.pem")
	publicPath := filepath.Join(keysDirectory, "team.pem")
	if err := WriteKeyPair(privatePath, publicPath); err != nil {
		t.Fatal(err)
	}
	publicPEM, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "team.pem"), publicPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "README.txt"), []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	keys, err := LoadTrustedPublicKeys(directory)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := LoadPublicKey(filepath.Join(directory, "team.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || len(keys[KeyID(publicKey)]) != ed25519.PublicKeySize {
		t.Fatalf("loaded trusted keys = %v", keys)
	}
}

func TestLoadTrustedPublicKeysRejectsBadPEM(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "bad.pem"), []byte("not a PEM key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTrustedPublicKeys(directory); err == nil || !strings.Contains(err.Error(), "bad.pem") {
		t.Fatalf("err = %v, want malformed trusted key error", err)
	}
}

func TestDecodeSidecarRejectsUnknownFieldsAndOversize(t *testing.T) {
	for _, data := range [][]byte{
		[]byte(`{"apiVersion":"v1","unknown":true}`),
		[]byte(`{} {}`),
		make([]byte, maxSidecarBytes+1),
	} {
		if _, err := DecodeSidecar(data); err == nil {
			t.Fatalf("DecodeSidecar accepted invalid input of %d bytes", len(data))
		}
	}
}
