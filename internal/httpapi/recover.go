package httpapi

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// withRecovery превращает панику в 500, чтобы один сломанный обработчик не
// смог утащить с собой весь процесс.
//
// Располагается внутри withLogging, поэтому запрос, вызвавший панику,
// по-прежнему даёт ровно одну запись "request", и эта запись несёт статус
// 500, а значит и уровень error.
func withRecovery(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}

			// Значение паники может содержать всё, что держал обработчик,
			// включая персональные данные, поэтому оно идёт только в лог и
			// никогда — клиенту. Стек делает запись пригодной для расследования.
			logger.LogAttrs(r.Context(), slog.LevelError, "handler panicked",
				slog.Any("panic", recovered),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("request_id", RequestID(r.Context())),
				slog.String("stack", string(debug.Stack())),
			)

			// Запись тела после того, как обработчик уже отправил своё,
			// испортила бы ответ; статус уже тот, что успел уйти клиенту.
			if written, ok := w.(interface{ Written() bool }); ok && written.Written() {
				return
			}

			writeError(w, r, http.StatusInternalServerError, codeInternal, "Внутренняя ошибка сервера")
		}()

		next.ServeHTTP(w, r)
	})
}
