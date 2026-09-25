package httpapi

import (
	"net/http"
	"time"
)

func NewHandler() http.Handler {
	mux := http.NewServeMux()
	probe := func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}
	mux.HandleFunc("GET /healthz", probe)
	mux.HandleFunc("GET /readyz", probe)
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
