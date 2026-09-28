package httpapi

import (
	"net/http"
	"time"

	"github.com/jagarkarlo/checkride/internal/schema"
)

func NewHandler() http.Handler {
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
	mux.HandleFunc("POST /api/v1/runs/report", runReportHandler)
	return mux
}

func NewServer(address string) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           NewHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}
