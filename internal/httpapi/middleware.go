package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"
)

// requestIDHeader переносит идентификатор корреляции между клиентом, прокси и нашими логами.
const requestIDHeader = "X-Request-Id"

type contextKey int

const (
	requestIDKey contextKey = iota
	identityKey
	logFieldsKey
)

// RequestID возвращает идентификатор корреляции запроса или "" вне middleware.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)

	return id
}

// logFields передаёт наружу, в middleware логирования, значения, которые
// становятся известны только глубже в цепочке обработки.
//
// withLogging выполняется снаружи requireAuth, поэтому к моменту записи лога
// аутентифицированный контекст уже потерян. withLogging заранее выделяет
// этот держатель значений, а requireAuth его заполняет. Оба идентификатора —
// непрозрачные UUID, а не персональные данные, и вместе они дают журнал
// доступа, которого требует описание продукта.
type logFields struct {
	userID   string
	agencyID string
}

func logFieldsFrom(ctx context.Context) *logFields {
	fields, _ := ctx.Value(logFieldsKey).(*logFields)

	return fields
}

// withLogging пишет одну запись на запрос после того, как обработчик завершил работу.
func withLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set(requestIDHeader, id)

		recorder := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		started := time.Now()

		ctx := context.WithValue(r.Context(), requestIDKey, id)
		fields := &logFields{}
		ctx = context.WithValue(ctx, logFieldsKey, fields)

		next.ServeHTTP(recorder, r.WithContext(ctx))

		// Query-строка намеренно не включена: в ней будут поисковые запросы
		// по туристам, а значит — фамилии и номера паспортов.
		attrs := []slog.Attr{
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", recorder.status),
			slog.Int64("duration_ms", time.Since(started).Milliseconds()),
			slog.Int("bytes", recorder.bytes),
			slog.String("request_id", id),
		}
		if fields.userID != "" {
			attrs = append(attrs, slog.String("user_id", fields.userID), slog.String("agency_id", fields.agencyID))
		}

		logger.LogAttrs(r.Context(), levelFor(recorder.status, r.URL.Path), "request", attrs...)
	})
}

func levelFor(status int, path string) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	case path == pingPath:
		// Healthcheck контейнера опрашивает этот эндпоинт; держим его вне журнала по умолчанию.
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

func newRequestID() string {
	var buf [16]byte
	// crypto/rand.Read никогда не возвращает ошибку; вместо этого при сбое программа паникует.
	_, _ = rand.Read(buf[:])

	return hex.EncodeToString(buf[:])
}

// responseRecorder фиксирует код статуса и размер ответа для логирования.
type responseRecorder struct {
	http.ResponseWriter
	status  int
	bytes   int
	written bool
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.written {
		return
	}
	r.status = status
	r.written = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	r.written = true
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n

	return n, err
}

// Written сообщает, начал ли отправляться ответ, — по этому withRecovery
// понимает, можно ли ещё заменить тело ответа на 500.
func (r *responseRecorder) Written() bool { return r.written }

// Unwrap позволяет http.ResponseController по-прежнему добираться до Flusher и Hijacker.
func (r *responseRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
