package httpapi

import (
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/lobanovsky/trip-pip-backend/internal/store"
)

const (
	defaultLimit = 25
	maxLimit     = 100
	maxOffset    = 100_000
	maxSearchLen = 100
)

// uuidRe проверяет идентификатор из пути или query-параметра до того, как
// он попадёт в SQL. Без этого некорректный id даёт 500 из-за SQLSTATE
// 22P02 вместо 404.
var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isUUID(value string) bool { return uuidRe.MatchString(value) }

// pathID читает параметр пути, отвечая 404, если он не может быть идентификатором.
func (a *api) pathID(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	value := r.PathValue(name)
	if !isUUID(value) {
		writeError(w, r, http.StatusNotFound, codeNotFound, "Запись не найдена")

		return "", false
	}

	return value, true
}

// paging читает limit и offset, ограничивая их разумными пределами.
func paging(r *http.Request) (limit, offset int) {
	limit = defaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = min(parsed, maxLimit)
		}
	}

	if raw := r.URL.Query().Get("offset"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			offset = min(parsed, maxOffset)
		}
	}

	return limit, offset
}

// searchQuery читает произвольную поисковую строку, ограниченную по длине,
// чтобы огромная строка не превратилась в дорогое сканирование.
func searchQuery(r *http.Request) string {
	value := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(value)) > maxSearchLen {
		value = string([]rune(value)[:maxSearchLen])
	}

	return value
}

// uuidQuery читает необязательный фильтр по id, игнорируя всё некорректное.
func uuidQuery(r *http.Request, name string) string {
	value := r.URL.Query().Get(name)
	if !isUUID(value) {
		return ""
	}

	return value
}

// dateQuery читает необязательный фильтр в формате ГГГГ-ММ-ДД.
func dateQuery(r *http.Request, name string) (*store.Date, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, nil
	}

	parsed, err := store.ParseDate(raw)
	if err != nil {
		return nil, err
	}

	return &parsed, nil
}

// intQuery читает необязательный фильтр с целым неотрицательным числом.
func intQuery(r *http.Request, name string, fallback, maximum int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 0 {
		return fallback
	}

	return min(parsed, maximum)
}

// boolQuery читает необязательный логический фильтр.
func boolQuery(r *http.Request, name string) bool {
	parsed, err := strconv.ParseBool(r.URL.Query().Get(name))

	return err == nil && parsed
}

// transactionKindQuery читает необязательный фильтр по виду транзакции,
// отбрасывая неизвестное значение — так опечатка возвращает всё, а не ошибку.
func transactionKindQuery(r *http.Request) string {
	value := r.URL.Query().Get("kind")
	if !slices.Contains(store.AllTransactionKinds, value) {
		return ""
	}

	return value
}

// paymentStatusQuery читает необязательный фильтр заявок по статусу оплаты,
// отбрасывая неизвестное значение — так опечатка возвращает всё, а не ошибку.
func paymentStatusQuery(r *http.Request) string {
	value := r.URL.Query().Get("paymentStatus")
	if !slices.Contains(store.AllPaymentStatuses, value) {
		return ""
	}

	return value
}

// statusQuery читает повторяющиеся фильтры по статусу, отбрасывая
// неизвестные значения — так опечатка возвращает всё, а не ошибку.
func statusQuery(r *http.Request) []string {
	values := r.URL.Query()["status"]
	statuses := make([]string, 0, len(values))
	for _, value := range values {
		for _, candidate := range strings.Split(value, ",") {
			candidate = strings.TrimSpace(candidate)
			for _, known := range store.AllStatuses {
				if candidate == known {
					statuses = append(statuses, candidate)
				}
			}
		}
	}

	return statuses
}
