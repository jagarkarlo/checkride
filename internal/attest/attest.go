package attest

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	Version          = "nostekon/attestation/v1alpha1"
	LegacyVersion    = "checkride/attestation/v1alpha1"
	Algorithm        = "Ed25519"
	maxEvidenceBytes = 16 << 20
	maxSidecarBytes  = 16 << 10
	maxTrustedKeys   = 128
)

// Each version signs under its own context, so a legacy signature cannot be relabelled.
var signingContexts = map[string][]byte{
	Version:       []byte("nostekon/drillrun-attestation/v1alpha1\x00"),
	LegacyVersion: []byte("checkride/drillrun-attestation/v1alpha1\x00"),
}

type Sidecar struct {
	APIVersion     string `json:"apiVersion"`
	Algorithm      string `json:"algorithm"`
	KeyID          string `json:"keyId"`
	EvidenceSHA256 string `json:"evidenceSHA256"`
	Signature      string `json:"signature"`
}

func KeyID(publicKey ed25519.PublicKey) string {
	digest := sha256.Sum256(publicKey)
	return hex.EncodeToString(digest[:])
}

func Sign(evidence []byte, privateKey ed25519.PrivateKey) (Sidecar, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return Sidecar{}, errors.New("invalid Ed25519 private key")
	}
	digest := sha256.Sum256(evidence)
	message := append(append([]byte(nil), signingContexts[Version]...), digest[:]...)
	signature := ed25519.Sign(privateKey, message)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	return Sidecar{
		APIVersion:     Version,
		Algorithm:      Algorithm,
		KeyID:          KeyID(publicKey),
		EvidenceSHA256: hex.EncodeToString(digest[:]),
		Signature:      base64.StdEncoding.EncodeToString(signature),
	}, nil
}

func Verify(evidence []byte, sidecar Sidecar, trustedKey ed25519.PublicKey) error {
	context, supported := signingContexts[sidecar.APIVersion]
	if !supported {
		return fmt.Errorf("unsupported attestation apiVersion %q", sidecar.APIVersion)
	}
	if sidecar.Algorithm != Algorithm {
		return fmt.Errorf("unsupported attestation algorithm %q", sidecar.Algorithm)
	}
	if len(trustedKey) != ed25519.PublicKeySize {
		return errors.New("invalid Ed25519 public key")
	}
	if sidecar.KeyID != KeyID(trustedKey) {
		return errors.New("attestation keyId does not match the trusted public key")
	}
	digest := sha256.Sum256(evidence)
	wantDigest := hex.EncodeToString(digest[:])
	if len(sidecar.EvidenceSHA256) != len(wantDigest) || subtle.ConstantTimeCompare([]byte(sidecar.EvidenceSHA256), []byte(wantDigest)) != 1 {
		return errors.New("attestation evidence digest does not match the input bytes")
	}
	signature, err := base64.StdEncoding.DecodeString(sidecar.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("attestation signature is malformed")
	}
	message := append(append([]byte(nil), context...), digest[:]...)
	if !ed25519.Verify(trustedKey, message, signature) {
		return errors.New("attestation signature verification failed")
	}
	return nil
}

func GenerateKeyPair() (privatePEM, publicPEM []byte, err error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate Ed25519 key: %w", err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("encode private key: %w", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("encode public key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}), nil
}

func WriteKeyPair(privatePath, publicPath string) error {
	if privatePath == publicPath {
		return errors.New("private and public key paths must differ")
	}
	privatePEM, publicPEM, err := GenerateKeyPair()
	if err != nil {
		return err
	}
	if err := writeNewFile(privatePath, privatePEM, 0o600); err != nil {
		return fmt.Errorf("write private key: %w", err)
	}
	if err := writeNewFile(publicPath, publicPEM, 0o644); err != nil {
		removeErr := os.Remove(privatePath)
		if removeErr != nil {
			return fmt.Errorf("write public key: %w (could not remove private key %q: %v)", err, privatePath, removeErr)
		}
		return fmt.Errorf("write public key: %w", err)
	}
	return nil
}

func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat private key: %w", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("private key permissions must not grant group or other access")
	}
	data, err := readLimited(path, maxSidecarBytes)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" || strings.TrimSpace(string(rest)) != "" {
		return nil, errors.New("private key must be one PKCS#8 PEM block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("private key must use Ed25519")
	}
	return key, nil
}

func LoadPublicKey(path string) (ed25519.PublicKey, error) {
	data, err := readLimited(path, maxSidecarBytes)
	if err != nil {
		return nil, fmt.Errorf("read public key: %w", err)
	}
	return DecodePublicKey(data)
}

func DecodePublicKey(data []byte) (ed25519.PublicKey, error) {
	if len(data) > maxSidecarBytes {
		return nil, errors.New("public key exceeds the 16 KiB limit")
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PUBLIC KEY" || strings.TrimSpace(string(rest)) != "" {
		return nil, errors.New("public key must be one PKIX PEM block")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("public key must use Ed25519")
	}
	return key, nil
}

func LoadTrustedPublicKeys(directory string) (map[string]ed25519.PublicKey, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read trusted key directory: %w", err)
	}
	keys := make(map[string]ed25519.PublicKey)
	for _, entry := range entries {
		if entry.IsDir() || strings.ToLower(filepath.Ext(entry.Name())) != ".pem" {
			continue
		}
		if !entry.Type().IsRegular() {
			return nil, fmt.Errorf("trusted key %q must be a regular file", entry.Name())
		}
		if len(keys) >= maxTrustedKeys {
			return nil, fmt.Errorf("trusted key directory may contain at most %d PEM keys", maxTrustedKeys)
		}
		path := filepath.Join(directory, entry.Name())
		key, err := LoadPublicKey(path)
		if err != nil {
			return nil, fmt.Errorf("load trusted key %q: %w", entry.Name(), err)
		}
		keyID := KeyID(key)
		if _, exists := keys[keyID]; exists {
			return nil, fmt.Errorf("duplicate trusted key %s", keyID)
		}
		keys[keyID] = key
	}
	return keys, nil
}

func ReadEvidence(path string) ([]byte, error) {
	data, err := readLimited(path, maxEvidenceBytes)
	if err != nil {
		return nil, fmt.Errorf("read evidence: %w", err)
	}
	return data, nil
}

func ReadSidecar(path string) (Sidecar, error) {
	data, err := readLimited(path, maxSidecarBytes)
	if err != nil {
		return Sidecar{}, fmt.Errorf("read attestation: %w", err)
	}
	sidecar, err := DecodeSidecar(data)
	if err != nil {
		return Sidecar{}, fmt.Errorf("decode attestation: %w", err)
	}
	return sidecar, nil
}

func DecodeSidecar(data []byte) (Sidecar, error) {
	if len(data) > maxSidecarBytes {
		return Sidecar{}, fmt.Errorf("attestation exceeds %d bytes", maxSidecarBytes)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var sidecar Sidecar
	if err := decoder.Decode(&sidecar); err != nil {
		return Sidecar{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Sidecar{}, errors.New("attestation must contain exactly one JSON value")
	}
	return sidecar, nil
}

func WriteSidecar(path string, sidecar Sidecar) error {
	data, err := json.MarshalIndent(sidecar, "", "  ")
	if err != nil {
		return fmt.Errorf("encode attestation: %w", err)
	}
	return writeNewFile(path, append(data, '\n'), 0o644)
}

func readLimited(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return data, nil
}

func writeNewFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}
