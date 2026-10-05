package httpapi

import (
	"crypto/ed25519"
	"net/http"
	"time"

	"github.com/jagarkarlo/nostekon/internal/schema"
)

func NewHandler() http.Handler {
	return NewHandlerWithTrustedKeys(nil)
}

func NewHandlerWithTrustedKeys(configuredKeys map[string]ed25519.PublicKey) http.Handler {
	trustedKeys := make(map[string]ed25519.PublicKey, len(configuredKeys))
	for keyID, key := range configuredKeys {
		trustedKeys[keyID] = append(ed25519.PublicKey(nil), key...)
	}
	mux := http.NewServeMux()
	probe := func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}
	mux.HandleFunc("GET /healthz", probe)
	mux.HandleFunc("GET /readyz", probe)
	mux.HandleFunc("GET /api/v1/schemas/drillrun", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/schema+json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(schema.DrillRunJSONSchema)
	})
	mux.HandleFunc("POST /api/v1/drills/validate", validateDrillHandler)
	mux.HandleFunc("POST /api/v1/runs/report", func(writer http.ResponseWriter, request *http.Request) {
		runReportHandler(writer, request, trustedKeys)
	})
	return mux
}

func NewServer(address string) *http.Server {
	return NewServerWithTrustedKeys(address, nil)
}

func NewServerWithTrustedKeys(address string, trustedKeys map[string]ed25519.PublicKey) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           NewHandlerWithTrustedKeys(trustedKeys),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}
