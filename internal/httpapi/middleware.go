package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"
)

// requestIDHeader carries the correlation id between client, proxy and our logs.
const requestIDHeader = "X-Request-Id"

type contextKey int

const requestIDKey contextKey = iota

// RequestID returns the correlation id of the request, or "" outside the middleware.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)

	return id
}

// withLogging writes one record per request once the handler has finished.
func withLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set(requestIDHeader, id)

		recorder := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		started := time.Now()

		next.ServeHTTP(recorder, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))

		// The query string is deliberately left out: it will carry tourist search terms.
		logger.LogAttrs(r.Context(), levelFor(recorder.status, r.URL.Path), "request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", recorder.status),
			slog.Int64("duration_ms", time.Since(started).Milliseconds()),
			slog.Int("bytes", recorder.bytes),
			slog.String("request_id", id),
		)
	})
}

func levelFor(status int, path string) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	case path == pingPath:
		// The container healthcheck polls this endpoint; keep it out of the default log.
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

func newRequestID() string {
	var buf [16]byte
	// crypto/rand.Read never returns an error; it crashes the program instead.
	_, _ = rand.Read(buf[:])

	return hex.EncodeToString(buf[:])
}

// responseRecorder captures the status code and response size for logging.
type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *responseRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n

	return n, err
}

// Unwrap keeps http.ResponseController able to reach Flusher and Hijacker.
func (r *responseRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
