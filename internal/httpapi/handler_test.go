package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testVersion = "test-commit"

// discardLogger keeps handler tests focused on responses; logging has its own tests.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func TestPing(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
	response := httptest.NewRecorder()

	NewHandler(discardLogger(), testVersion).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
	}

	if contentType := response.Header().Get("Content-Type"); contentType != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json; charset=utf-8", contentType)
	}
	if response.Body.String() != "{\"message\":\"pong\"}\n" {
		t.Fatalf("body = %q, want ping response JSON", response.Body.String())
	}

	var body pingResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Message != "pong" {
		t.Errorf("message = %q, want pong", body.Message)
	}
}

func TestVersion(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	response := httptest.NewRecorder()

	NewHandler(discardLogger(), testVersion).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
	}

	if contentType := response.Header().Get("Content-Type"); contentType != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json; charset=utf-8", contentType)
	}

	var body versionResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Commit != testVersion {
		t.Errorf("commit = %q, want %q", body.Commit, testVersion)
	}
}

func TestPingRejectsUnsupportedMethod(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodPost, "/api/ping", strings.NewReader("{}"))
	response := httptest.NewRecorder()

	NewHandler(discardLogger(), testVersion).ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}

func TestUnknownRoute(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodGet, "/api/unknown", nil)
	response := httptest.NewRecorder()

	NewHandler(discardLogger(), testVersion).ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusNotFound)
	}
}
