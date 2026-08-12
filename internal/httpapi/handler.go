package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

const (
	pingPath    = "/api/v1/ping"
	versionPath = "/api/v1/version"
)

type pingResponse struct {
	Message string `json:"message"`
}

type versionResponse struct {
	Commit string `json:"commit"`
}

// NewHandler returns the HTTP API handler reporting the given build version.
// Requests are logged through logger; see withLogging for the levels used.
func NewHandler(logger *slog.Logger, version string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+pingPath, handlePing)
	mux.HandleFunc("GET "+versionPath, handleVersion(version))

	return withLogging(logger, mux)
}

func handlePing(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, pingResponse{Message: "pong"})
}

func handleVersion(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, versionResponse{Commit: version})
	}
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	_ = json.NewEncoder(w).Encode(payload)
}
