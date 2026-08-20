package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/lobanovsky/trip-pip-backend/internal/auth"
	"github.com/lobanovsky/trip-pip-backend/internal/mail"
	"github.com/lobanovsky/trip-pip-backend/internal/store"
)

// Deps несёт соратников, необходимых доменным маршрутам.
//
// Нулевое значение обслуживает только /api/ping и /api/version. Это не
// удобство для тестов: без базы данных контейнер всё равно должен отвечать
// на оба этих маршрута, иначе проверка деплоя провалится и релиз откатится.
type Deps struct {
	Store         *store.Store
	Location      *time.Location
	SessionTTL    time.Duration
	SecureCookies bool
	LoginLimiter  *auth.RateLimiter

	// MailSender шлёт письма подтверждения регистрации. nil, если SMTP не
	// настроен — тогда POST /api/auth/register отвечает 503, а не молча
	// заводит аккаунт, который некому подтвердить.
	MailSender      mail.Sender
	RegisterLimiter *auth.RateLimiter

	// PublicBaseURL — адрес фронтенда, используется только чтобы собрать
	// ссылку подтверждения в письме регистрации.
	PublicBaseURL string
}

func (d Deps) withDefaults() Deps {
	if d.Location == nil {
		d.Location = time.UTC
	}
	if d.SessionTTL <= 0 {
		d.SessionTTL = 24 * time.Hour
	}
	if d.LoginLimiter == nil {
		d.LoginLimiter = auth.NewRateLimiter(5, 15*time.Minute)
	}
	if d.RegisterLimiter == nil {
		d.RegisterLimiter = auth.NewRateLimiter(5, time.Hour)
	}

	return d
}

type api struct {
	deps   Deps
	logger *slog.Logger
}

// today — текущая дата в часовом поясе агентства. Истечение срока документа
// считается по календарному дню, поэтому «сегодня» должно быть днём
// агентства, а не UTC.
func (a *api) today() store.Date {
	return store.NewDate(time.Now().In(a.deps.Location))
}

// sessionCookieName — единственная cookie, которую устанавливает этот API.
const sessionCookieName = "trip_pip_session"

// Identity возвращает аутентифицированного вызывающего, или false вне requireAuth.
func Identity(ctx context.Context) (store.Identity, bool) {
	identity, ok := ctx.Value(identityKey).(store.Identity)

	return identity, ok
}

// requireAuth разбирает cookie сессии перед запуском обработчика.
//
// Подключается к каждому маршруту по отдельности, а не ко всему mux:
// у http.ServeMux нет групп маршрутов, а /api/ping и /api/version должны
// оставаться без авторизации.
func (a *api) requireAuth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.deps.Store == nil {
			writeError(w, r, http.StatusServiceUnavailable, codeUnavailable,
				"База данных не настроена: задайте DATABASE_URL")

			return
		}

		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			a.unauthenticated(w, r)

			return
		}

		identity, err := a.deps.Store.IdentityByToken(r.Context(), auth.HashToken(cookie.Value))
		if err != nil {
			// Истёкшая, отозванная, отключённая и поддельная сессии сообщаются
			// одинаково: клиенту нужно знать лишь то, что необходимо войти заново.
			if errors.Is(err, store.ErrNotFound) {
				a.clearSessionCookie(w)
				a.unauthenticated(w, r)

				return
			}

			a.internalError(w, r, "resolve session", err)

			return
		}

		// Запись last_seen_at на каждый запрос превратила бы каждое чтение в
		// запись; раз в пять минут достаточно, чтобы заметить простаивающую сессию.
		if time.Since(identity.LastSeenAt) > 5*time.Minute {
			if err := a.deps.Store.TouchSession(r.Context(), identity.SessionID); err != nil {
				a.logger.LogAttrs(r.Context(), slog.LevelWarn, "touch session failed",
					slog.String("error", err.Error()))
			}
		}

		if fields := logFieldsFrom(r.Context()); fields != nil {
			fields.userID = identity.UserID
			fields.agencyID = identity.AgencyID
		}

		// Межсайтовый POST из формы не может установить этот Content-Type без
		// preflight-запроса, что вместе с SameSite=Lax закрывает CSRF для
		// изменяющих методов без отдельного токена.
		if isMutating(r.Method) && !hasJSONContentType(r) {
			writeError(w, r, http.StatusUnsupportedMediaType, codeBadRequest,
				"Content-Type должен быть application/json")

			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey, identity)))
	})
}

func (a *api) unauthenticated(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusUnauthorized, codeUnauthenticated, "Требуется вход в систему")
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func hasJSONContentType(r *http.Request) bool {
	// Пустое тело не несёт ничего, чем межсайтовая форма могла бы изменить
	// состояние, поэтому оно исключение независимо от метода: logout и
	// подобные действия без тела не должны блокироваться проверкой,
	// нацеленной на содержимое запроса.
	if r.ContentLength == 0 {
		return true
	}

	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		return false
	}

	media, _, _ := strings.Cut(contentType, ";")

	return strings.TrimSpace(media) == "application/json"
}

// internalError логирует причину и возвращает общее сообщение.
//
// Необработанная ошибка PostgreSQL может процитировать строку, нарушившую
// ограничение, а значит — передать паспортные данные в HTTP-ответ, поэтому
// она никогда не доходит до клиента.
func (a *api) internalError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	a.logger.LogAttrs(r.Context(), slog.LevelError, "request failed",
		slog.String("operation", operation),
		slog.String("error", err.Error()),
		slog.String("request_id", RequestID(r.Context())),
	)

	writeError(w, r, http.StatusInternalServerError, codeInternal, "Внутренняя ошибка сервера")
}

// writeStoreError сопоставляет ошибку store с подходящим HTTP-статусом.
func (a *api) writeStoreError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	var validationErr *store.ValidationError
	if errors.As(err, &validationErr) {
		writeErrorFields(w, r, http.StatusBadRequest, codeValidation,
			"Проверьте заполнение полей", validationErr.Fields)

		return
	}

	switch {
	case errors.Is(err, store.ErrNotFound):
		// Запись другого агентства сообщается как отсутствующая, а не как
		// запрещённая: «запрещено» подтвердило бы, что она где-то существует.
		writeError(w, r, http.StatusNotFound, codeNotFound, "Запись не найдена")
	case errors.Is(err, store.ErrVersionConflict):
		writeError(w, r, http.StatusConflict, codeVersionConflict,
			"Запись изменена другим пользователем, обновите страницу")
	case errors.Is(err, store.ErrConflict):
		writeError(w, r, http.StatusConflict, codeConflict, conflictMessage(store.ConstraintName(err)))
	case errors.Is(err, store.ErrInvalidReference):
		writeError(w, r, http.StatusBadRequest, codeValidation,
			"Указана запись, которой нет в вашем агентстве")
	case errors.Is(err, store.ErrInvalidValue):
		writeError(w, r, http.StatusBadRequest, codeValidation, "Недопустимое значение поля")
	default:
		a.internalError(w, r, operation, err)
	}
}

// conflictMessage превращает имя ограничения в сообщение, по которому сотрудник может действовать.
func conflictMessage(constraint string) string {
	switch constraint {
	case "tourists_passport_uk":
		return "Турист с таким паспортом уже есть в базе агентства"
	case "tourists_intl_passport_uk":
		return "Турист с таким загранпаспортом уже есть в базе агентства"
	case "users_email_key":
		return "Пользователь с таким адресом уже зарегистрирован"
	case "tour_operators_name_uk":
		return "Туроператор с таким названием уже есть"
	case "partners_name_uk":
		return "Партнёр с таким названием уже есть"
	case "acquisition_channels_agency_id_code_key":
		return "Канал с таким кодом уже есть"
	default:
		return "Запись с такими данными уже существует"
	}
}
