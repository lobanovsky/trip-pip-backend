package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// maxRequestBody ограничивает разбираемые тела запросов. Сканы документов не
// входят в первый этап, поэтому ничего легитимного не приближается к этому пределу.
const maxRequestBody = 1 << 20

// Коды ошибок, возвращаемые в конверте ниже. Фронтенд переключается по ним,
// поэтому относитесь к ним как к части контракта API.
const (
	codeBadRequest       = "bad_request"
	codeValidation       = "validation_failed"
	codeUnauthenticated  = "unauthenticated"
	codeForbidden        = "forbidden"
	codeEmailNotVerified = "email_not_verified"
	codeNotFound         = "not_found"
	codeConflict         = "conflict"
	codeVersionConflict  = "version_conflict"
	codeRateLimited      = "rate_limited"
	codeUnavailable      = "database_unavailable"
	codeInternal         = "internal_error"
)

type apiError struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Fields    map[string]string `json:"fields,omitempty"`
	RequestID string            `json:"requestId,omitempty"`
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

// listEnvelope — форма каждого ответа-коллекции.
type listEnvelope[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeErrorFields(w, r, status, code, message, nil)
}

func writeErrorFields(w http.ResponseWriter, r *http.Request, status int, code, message string, fields map[string]string) {
	writeJSON(w, status, errorEnvelope{Error: apiError{
		Code:      code,
		Message:   message,
		Fields:    fields,
		RequestID: RequestID(r.Context()),
	}})
}

// decodeJSON читает ровно один JSON-объект из тела запроса.
//
// Неизвестные поля отклоняются, поэтому опечатка на фронтенде проявляется
// как 400 с именем поля, а не молча теряет значение. Возвращаемую ошибку
// безопасно показывать клиенту: она никогда не содержит содержимого тела.
func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		if media, _, _ := strings.Cut(contentType, ";"); strings.TrimSpace(media) != "application/json" {
			return fmt.Errorf("Content-Type должен быть application/json")
		}
	}

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(target); err != nil {
		return decodeError(err)
	}

	// Второе значение в теле означает, что клиент отправил что-то, что мы бы
	// молча проигнорировали, например два склеенных объекта.
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("тело запроса должно содержать один JSON-объект")
	}

	return nil
}

func decodeError(err error) error {
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	var maxBytesErr *http.MaxBytesError

	switch {
	case errors.As(err, &syntaxErr):
		return fmt.Errorf("некорректный JSON в позиции %d", syntaxErr.Offset)
	case errors.As(err, &typeErr):
		return fmt.Errorf("поле %q имеет неверный тип", typeErr.Field)
	case errors.As(err, &maxBytesErr):
		return fmt.Errorf("тело запроса больше допустимых %d байт", maxRequestBody)
	case errors.Is(err, io.EOF):
		return fmt.Errorf("тело запроса пустое")
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		field := strings.TrimPrefix(err.Error(), "json: unknown field ")

		return fmt.Errorf("неизвестное поле %s", field)
	default:
		return fmt.Errorf("не удалось разобрать тело запроса")
	}
}
