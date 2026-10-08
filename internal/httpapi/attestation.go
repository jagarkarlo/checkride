package httpapi

import (
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/jagarkarlo/nostekon/internal/attest"
)

type selectedKeyCheck struct {
	APIVersion         string `json:"apiVersion"`
	SignatureValid     bool   `json:"signatureValid"`
	KeyID              string `json:"keyId"`
	EvidenceSHA256     string `json:"evidenceSHA256"`
	AttestationVersion string `json:"attestationVersion"`
	TrustSource        string `json:"trustSource"`
}

func inspectPublicKeyHandler(writer http.ResponseWriter, request *http.Request) {
	data, ok := readAttestationBody(writer, request, 16<<10, "application/x-pem-file")
	if !ok {
		return
	}
	key, err := attest.DecodePublicKey(data)
	if err != nil {
		writeJSON(writer, http.StatusUnprocessableEntity, reportProblem{Errors: []string{err.Error()}})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"keyId": attest.KeyID(key), "algorithm": attest.Algorithm})
}

func selectedKeyVerificationHandler(writer http.ResponseWriter, request *http.Request) {
	select {
	case reportSlots <- struct{}{}:
		defer func() { <-reportSlots }()
	default:
		writer.Header().Set("Retry-After", "1")
		writeJSON(writer, http.StatusServiceUnavailable, reportProblem{Errors: []string{"verification capacity reached; retry shortly"}})
		return
	}
	evidence, ok := readAttestationBody(writer, request, maxRunRequestBytes, "application/json")
	if !ok {
		return
	}
	decodeHeader := func(name string) ([]byte, error) {
		encoded := request.Header.Get(name)
		if encoded == "" || len(encoded) > maxAttestationHeaderBytes {
			return nil, errors.New("public key and attestation headers are required and limited to 32 KiB each")
		}
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, errors.New("public key and attestation headers must contain standard base64")
		}
		return data, nil
	}
	keyBytes, err := decodeHeader("X-Nostekon-Public-Key")
	if err != nil {
		writeJSON(writer, http.StatusUnprocessableEntity, reportProblem{Errors: []string{err.Error()}})
		return
	}
	key, err := attest.DecodePublicKey(keyBytes)
	if err != nil {
		writeJSON(writer, http.StatusUnprocessableEntity, reportProblem{Errors: []string{err.Error()}})
		return
	}
	sidecarBytes, err := decodeHeader(attestationHeader)
	if err != nil {
		writeJSON(writer, http.StatusUnprocessableEntity, reportProblem{Errors: []string{err.Error()}})
		return
	}
	sidecar, err := attest.DecodeSidecar(sidecarBytes)
	if err == nil {
		err = attest.Verify(evidence, sidecar, key)
	}
	if err != nil {
		writeJSON(writer, http.StatusUnprocessableEntity, reportProblem{Errors: []string{err.Error()}})
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, selectedKeyCheck{
		APIVersion: "nostekon/signature-check/v1alpha1", SignatureValid: true,
		KeyID: sidecar.KeyID, EvidenceSHA256: sidecar.EvidenceSHA256,
		AttestationVersion: sidecar.APIVersion, TrustSource: "selected-public-key",
	})
}

func readAttestationBody(writer http.ResponseWriter, request *http.Request, limit int64, contentType string) ([]byte, bool) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != contentType {
		writeJSON(writer, http.StatusUnsupportedMediaType, reportProblem{Errors: []string{"Content-Type must be " + contentType}})
		return nil, false
	}
	data, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, limit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		status := http.StatusBadRequest
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeJSON(writer, status, reportProblem{Errors: []string{"request body is unreadable or exceeds the size limit"}})
		return nil, false
	}
	return data, true
}
