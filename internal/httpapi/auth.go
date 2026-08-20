package httpapi

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/lobanovsky/trip-pip-backend/internal/auth"
	"github.com/lobanovsky/trip-pip-backend/internal/store"
)

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type sessionResponse struct {
	User   sessionUser   `json:"user"`
	Agency sessionAgency `json:"agency"`
}

type sessionUser struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	FullName string `json:"fullName"`
}

type sessionAgency struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Timezone string `json:"timezone,omitempty"`
}

func (a *api) handleLogin(w http.ResponseWriter, r *http.Request) {
	if a.deps.Store == nil {
		writeError(w, r, http.StatusServiceUnavailable, codeUnavailable,
			"База данных не настроена: задайте DATABASE_URL")

		return
	}

	var request loginRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	email := strings.ToLower(strings.TrimSpace(request.Email))
	if email == "" || request.Password == "" {
		writeError(w, r, http.StatusBadRequest, codeValidation, "Укажите адрес электронной почты и пароль")

		return
	}

	// Ограничение по паре, а не по одному значению: ключ только по адресу
	// позволил бы кому угодно заблокировать чужую учётную запись.
	limiterKey := email + "|" + clientIP(r)
	if !a.deps.LoginLimiter.Allow(limiterKey) {
		writeError(w, r, http.StatusTooManyRequests, codeRateLimited,
			"Слишком много попыток входа, повторите позже")

		return
	}

	user, err := a.deps.Store.UserByEmail(r.Context(), email)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			a.internalError(w, r, "load user", err)

			return
		}

		// Хешируем в любом случае. Более быстрый ответ на неизвестный адрес,
		// чем на неверный пароль, превратил бы эту форму в справочник сотрудников.
		auth.VerifyDummy(request.Password)
		a.rejectLogin(w, r, email)

		return
	}

	valid, needsRehash, err := auth.VerifyPassword(user.PasswordHash, request.Password)
	if err != nil {
		a.internalError(w, r, "verify password", err)

		return
	}
	if !valid || !user.IsActive {
		a.rejectLogin(w, r, email)

		return
	}

	// Разглашается только после того, как пароль уже подтверждён верным:
	// до этой строки нельзя отличить «неверный пароль» от «email не
	// подтверждён», как и раньше нельзя отличить «неверный пароль» от
	// «неизвестный адрес» — та же логика, что у VerifyDummy выше.
	if user.EmailVerifiedAt == nil {
		writeError(w, r, http.StatusForbidden, codeEmailNotVerified,
			"Подтвердите email — мы отправили письмо со ссылкой при регистрации")

		return
	}

	if needsRehash {
		if rehashed, err := auth.HashPassword(request.Password); err == nil {
			if err := a.deps.Store.SetUserPassword(r.Context(), user.AgencyID, user.ID, rehashed); err != nil {
				a.logger.LogAttrs(r.Context(), slog.LevelWarn, "password rehash failed",
					slog.String("error", err.Error()))
			}
		}
	}

	a.deps.LoginLimiter.Reset(limiterKey)

	if err := a.deps.Store.MarkLogin(r.Context(), user.ID); err != nil {
		a.logger.LogAttrs(r.Context(), slog.LevelWarn, "mark login failed", slog.String("error", err.Error()))
	}

	a.startSession(w, r, user, store.ActionLogin, "Вход в систему")
}

// startSession открывает сессию для уже проверенного пользователя — общий
// хвост handleLogin и handleVerifyEmail (вход по паролю и вход сразу после
// подтверждения email оканчиваются одинаково: cookie сессии и sessionResponse).
func (a *api) startSession(w http.ResponseWriter, r *http.Request, user store.User, action, summary string) {
	token := auth.NewSessionToken()
	expiresAt := time.Now().Add(a.deps.SessionTTL)

	if _, err := a.deps.Store.CreateSession(r.Context(), user.ID, user.AgencyID,
		auth.HashToken(token), expiresAt, r.UserAgent()); err != nil {
		a.internalError(w, r, "create session", err)

		return
	}

	a.setSessionCookie(w, token, expiresAt)

	agency, err := a.deps.Store.Agency(r.Context(), user.AgencyID)
	if err != nil {
		a.internalError(w, r, "load agency", err)

		return
	}

	a.journal(r, user.AgencyID, store.Actor{UserID: user.ID, Label: user.FullName, RequestID: RequestID(r.Context())},
		store.EntityUser, user.ID, action, summary)

	writeJSON(w, http.StatusOK, sessionResponse{
		User:   sessionUser{ID: user.ID, Email: user.Email, FullName: user.FullName},
		Agency: sessionAgency{ID: agency.ID, Name: agency.Name, Timezone: agency.Timezone},
	})
}

// rejectLogin отвечает одинаково и на неизвестный адрес, и на неверный пароль.
func (a *api) rejectLogin(w http.ResponseWriter, r *http.Request, email string) {
	// Адрес логируется, потому что серия неудач по одной учётной записи — это
	// именно то, для чего существует журнал доступа. Пароль — никогда.
	a.logger.LogAttrs(r.Context(), slog.LevelWarn, "login rejected",
		slog.String("email", email),
		slog.String("request_id", RequestID(r.Context())),
	)

	writeError(w, r, http.StatusUnauthorized, codeUnauthenticated, "Неверный адрес или пароль")
}

func (a *api) handleLogout(w http.ResponseWriter, r *http.Request) {
	identity, ok := Identity(r.Context())
	if !ok {
		a.unauthenticated(w, r)

		return
	}

	if err := a.deps.Store.RevokeSession(r.Context(), identity.SessionID); err != nil {
		a.internalError(w, r, "revoke session", err)

		return
	}

	a.journal(r, identity.AgencyID, identity.Actor(RequestID(r.Context())),
		store.EntityUser, identity.UserID, store.ActionLogout, "Выход из системы")

	a.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) handleSession(w http.ResponseWriter, r *http.Request) {
	identity, ok := Identity(r.Context())
	if !ok {
		a.unauthenticated(w, r)

		return
	}

	agency, err := a.deps.Store.Agency(r.Context(), identity.AgencyID)
	if err != nil {
		a.internalError(w, r, "load agency", err)

		return
	}

	writeJSON(w, http.StatusOK, sessionResponse{
		User:   sessionUser{ID: identity.UserID, Email: identity.Email, FullName: identity.FullName},
		Agency: sessionAgency{ID: agency.ID, Name: agency.Name, Timezone: agency.Timezone},
	})
}

func (a *api) setSessionCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:  sessionCookieName,
		Value: token,
		Path:  "/",
		// HttpOnly не даёт скриптам на странице дотянуться до токена, поэтому
		// XSS-уязвимость не может унести с собой рабочую сессию.
		HttpOnly: true,
		Secure:   a.deps.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
	})
}

func (a *api) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   a.deps.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// journal записывает запись доступа, при неудаче логируя её, а не роняя
// запрос: потеря строки аудита не должна означать потерю входа в систему.
func (a *api) journal(r *http.Request, agencyID string, actor store.Actor, entityType, entityID, action, summary string) {
	if err := a.deps.Store.RecordAccess(r.Context(), agencyID, actor, entityType, entityID, action, summary); err != nil {
		a.logger.LogAttrs(r.Context(), slog.LevelWarn, "journal write failed",
			slog.String("action", action),
			slog.String("error", err.Error()),
		)
	}
}

// clientIP — компонент ключа ограничителя частоты. Доверяет только
// последней записи X-Forwarded-For — именно её добавляет reverse proxy
// перед этим сервисом, а клиент подделать её не может.
func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		if candidate := strings.TrimSpace(parts[len(parts)-1]); candidate != "" {
			return candidate
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}
