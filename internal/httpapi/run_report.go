package httpapi

import (
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/jagarkarlo/checkride/internal/verify"
)

// Ledgers of a few hundred thousand writes fit; larger drills should sample.
const maxRunRequestBytes = 16 << 20
const maxConcurrentReports = 4

var reportSlots = make(chan struct{}, maxConcurrentReports)

type reportProblem struct {
	Errors []string `json:"errors"`
}

func runReportHandler(writer http.ResponseWriter, request *http.Request) {
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
		writeJSON(writer, http.StatusOK, verify.Build(evidence))
	}
}
