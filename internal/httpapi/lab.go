package httpapi

import (
	"errors"
	"io"
	"mime"
	"net"
	"net/http"

	"github.com/jagarkarlo/nostekon/internal/labjobs"
)

func LoopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	return err == nil && (host == "localhost" || net.ParseIP(host).IsLoopback())
}

func registerLabRoutes(mux *http.ServeMux, manager *labjobs.Manager) {
	guard := func(next http.HandlerFunc) http.HandlerFunc {
		return func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Cache-Control", "no-store")
			writer.Header().Set("X-Content-Type-Options", "nosniff")
			if manager == nil {
				writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "Lab execution is disabled"})
				return
			}
			origin := request.Header.Get("Origin")
			if !LoopbackAddress(request.Host) || !LoopbackAddress(request.RemoteAddr) || request.Header.Get("X-Nostekon-Lab") != "true" || request.Header.Get("Sec-Fetch-Site") == "cross-site" || (origin != "" && origin != "http://"+request.Host) {
				writeJSON(writer, http.StatusForbidden, map[string]string{"error": "Lab requests require a same-origin loopback connection"})
				return
			}
			next(writer, request)
		}
	}
	mux.HandleFunc("GET /api/v1/lab", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		if manager == nil {
			writeJSON(writer, http.StatusOK, map[string]any{"enabled": false})
			return
		}
		guard(func(writer http.ResponseWriter, request *http.Request) {
			writeJSON(writer, http.StatusOK, map[string]any{"enabled": true, "maxWrites": 98, "maxRPOSeconds": 86400, "timeoutSeconds": 900})
		})(writer, request)
	})
	mux.HandleFunc("GET /api/v1/lab/jobs", guard(func(writer http.ResponseWriter, request *http.Request) {
		jobs := manager.List()
		for index := range jobs {
			jobs[index].Log = ""
			jobs[index].Summary = nil
		}
		writeJSON(writer, http.StatusOK, jobs)
	}))
	mux.HandleFunc("POST /api/v1/lab/jobs", guard(func(writer http.ResponseWriter, request *http.Request) {
		mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeJSON(writer, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, 4096))
		if err != nil {
			writeJSON(writer, http.StatusRequestEntityTooLarge, map[string]string{"error": "Lab request exceeds 4 KiB"})
			return
		}
		var options labjobs.Options
		if err := decodeStrictJSON(data, &options); err != nil || options.Validate() != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "Expected writes 1..98 and rpoSeconds 1..86400 only"})
			return
		}
		job, err := manager.Start(options)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, labjobs.ErrBusy) || errors.Is(err, labjobs.ErrCapacity) {
				status = http.StatusConflict
			}
			writeJSON(writer, status, map[string]string{"error": err.Error()})
			return
		}
		writer.Header().Set("Location", "/api/v1/lab/jobs/"+job.ID)
		writeJSON(writer, http.StatusAccepted, job)
	}))
	mux.HandleFunc("GET /api/v1/lab/jobs/{id}", guard(func(writer http.ResponseWriter, request *http.Request) {
		job, err := manager.Get(request.PathValue("id"))
		if err != nil {
			writeJSON(writer, http.StatusNotFound, map[string]string{"error": "Job not found"})
			return
		}
		writeJSON(writer, http.StatusOK, job)
	}))
	mux.HandleFunc("POST /api/v1/lab/jobs/{id}/cancel", guard(func(writer http.ResponseWriter, request *http.Request) {
		if err := manager.Cancel(request.PathValue("id")); err != nil {
			writeJSON(writer, http.StatusNotFound, map[string]string{"error": "Job not found"})
			return
		}
		writeJSON(writer, http.StatusAccepted, map[string]string{"status": "cancellation requested"})
	}))
	mux.HandleFunc("GET /api/v1/lab/jobs/{id}/artifacts/{name}", guard(func(writer http.ResponseWriter, request *http.Request) {
		name := request.PathValue("name")
		data, err := manager.Artifact(request.PathValue("id"), name)
		if err != nil {
			writeJSON(writer, http.StatusNotFound, map[string]string{"error": "Artifact unavailable"})
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(data)
	}))
}
