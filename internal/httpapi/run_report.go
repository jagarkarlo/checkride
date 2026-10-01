package httpapi

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/jagarkarlo/checkride/internal/attest"
	"github.com/jagarkarlo/checkride/internal/verify"
)

// Ledgers of a few hundred thousand writes fit; larger drills should sample.
const maxRunRequestBytes = 16 << 20
const maxAttestationHeaderBytes = 32 << 10
const maxConcurrentReports = 4

const attestationHeader = "X-Checkride-Attestation"

var reportSlots = make(chan struct{}, maxConcurrentReports)

type reportProblem struct {
	Errors []string `json:"errors"`
}

func runReportHandler(writer http.ResponseWriter, request *http.Request, trustedKeys map[string]ed25519.PublicKey) {
	select {
	case reportSlots <- struct{}{}:
		defer func() { <-reportSlots }()
	default:
		writer.Header().Set("Retry-After", "1")
		writeJSON(writer, http.StatusServiceUnavailable, reportProblem{Errors: []string{"report capacity reached; retry shortly"}})
		return
	}

	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeJSON(writer, http.StatusUnsupportedMediaType, reportProblem{Errors: []string{"Content-Type must be application/json"}})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, maxRunRequestBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(writer, http.StatusRequestEntityTooLarge, reportProblem{Errors: []string{"request body exceeds 16 MiB"}})
			return
		}
		writeJSON(writer, http.StatusBadRequest, reportProblem{Errors: []string{"could not read request body"}})
		return
	}

	provenance, err := verifyRequestAttestation(body, request.Header.Get(attestationHeader), trustedKeys)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, errAttestationNotConfigured) {
			status = http.StatusServiceUnavailable
		}
		writeJSON(writer, status, reportProblem{Errors: []string{err.Error()}})
		return
	}

	evidence, err := verify.ParseEvidence(body)
	var decodeErr *verify.DecodeError
	var validationErr *verify.ValidationError
	switch {
	case errors.As(err, &decodeErr):
		writeJSON(writer, http.StatusBadRequest, reportProblem{Errors: []string{decodeErr.Error()}})
	case errors.As(err, &validationErr):
		writeJSON(writer, http.StatusUnprocessableEntity, reportProblem{Errors: validationErr.Problems})
	case err != nil:
		writeJSON(writer, http.StatusBadRequest, reportProblem{Errors: []string{err.Error()}})
	default:
		report := verify.Build(evidence)
		if provenance != nil {
			report.Provenance = *provenance
		}
		writeJSON(writer, http.StatusOK, report)
	}
}

var errAttestationNotConfigured = errors.New("attestation verification is not configured on this API")

func verifyRequestAttestation(evidence []byte, encoded string, trustedKeys map[string]ed25519.PublicKey) (*verify.Provenance, error) {
	if encoded == "" {
		return nil, nil
	}
	if len(encoded) > maxAttestationHeaderBytes {
		return nil, errors.New("attestation header exceeds 32 KiB")
	}
	if len(trustedKeys) == 0 {
		return nil, errAttestationNotConfigured
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("attestation header must contain standard base64 JSON")
	}
	sidecar, err := attest.DecodeSidecar(data)
	if err != nil {
		return nil, err
	}
	trustedKey, ok := trustedKeys[sidecar.KeyID]
	if !ok {
		return nil, errors.New("attestation key is not trusted by this API")
	}
	if err := attest.Verify(evidence, sidecar, trustedKey); err != nil {
		return nil, err
	}
	return &verify.Provenance{
		Status: "verified", Algorithm: sidecar.Algorithm,
		KeyID: sidecar.KeyID, EvidenceSHA256: sidecar.EvidenceSHA256,
	}, nil
}
