package httpapi

import (
	"encoding/json"
	"net/http"
)

type pingResponse struct {
	Message string `json:"message"`
}

type versionResponse struct {
	Commit string `json:"commit"`
}

// NewHandler returns the HTTP API handler reporting the given build version.
func NewHandler(version string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/ping", handlePing)
	mux.HandleFunc("GET /api/v1/version", handleVersion(version))

	return mux
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
