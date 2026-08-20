package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/lobanovsky/trip-pip-backend/internal/auth"
	"github.com/lobanovsky/trip-pip-backend/internal/store"
)

// verificationTokenTTL — на сколько действительна ссылка подтверждения из
// письма. Того же порядка, что и типичный SESSION_TTL, но не настраивается
// отдельной переменной окружения — универсального суток достаточно, а
// делать это конфигурируемым сейчас незачем.
const verificationTokenTTL = 24 * time.Hour

type registerRequest struct {
	AgencyName string `json:"agencyName"`
	FullName   string `json:"fullName"`
	Email      string `json:"email"`
	Password   string `json:"password"`
}

// handleRegister заводит новое агентство самостоятельно, без участия команды
// Trip-Pip. Аккаунт создаётся неактивным для входа (email_verified_at NULL)
// до перехода по ссылке из письма — см. handleVerifyEmail.
func (a *api) handleRegister(w http.ResponseWriter, r *http.Request) {
	if a.deps.Store == nil {
		writeError(w, r, http.StatusServiceUnavailable, codeUnavailable,
			"База данных не настроена: задайте DATABASE_URL")

		return
	}
	if a.deps.MailSender == nil {
		writeError(w, r, http.StatusServiceUnavailable, codeUnavailable,
			"Регистрация временно недоступна: не настроена отправка почты")

		return
	}

	if !a.deps.RegisterLimiter.Allow("register:" + clientIP(r)) {
		writeError(w, r, http.StatusTooManyRequests, codeRateLimited,
			"Слишком много попыток регистрации, повторите позже")

		return
	}

	var request registerRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	agencyName := strings.TrimSpace(request.AgencyName)
	email := strings.ToLower(strings.TrimSpace(request.Email))
	fullName := strings.TrimSpace(request.FullName)

	fields := map[string]string{}
	if agencyName == "" {
		fields["agencyName"] = "обязательное поле"
	} else if len([]rune(agencyName)) > 200 {
		fields["agencyName"] = "не длиннее 200 символов"
	}
	if email == "" || !strings.Contains(email, "@") {
		fields["email"] = "укажите корректный адрес электронной почты"
	}
	if fullName == "" {
		fields["fullName"] = "обязательное поле"
	}
	if len([]rune(request.Password)) < auth.MinPasswordLength {
		fields["password"] = "пароль должен быть не короче 12 символов"
	}
	if len(fields) > 0 {
		writeErrorFields(w, r, http.StatusBadRequest, codeValidation, "Проверьте заполнение полей", fields)

		return
	}

	hash, err := auth.HashPassword(request.Password)
	if err != nil {
		a.internalError(w, r, "hash password", err)

		return
	}

	rawToken := auth.NewSessionToken()

	_, _, err = a.deps.Store.RegisterAgency(r.Context(), agencyName, email, hash, fullName,
		auth.HashToken(rawToken), verificationTokenTTL)
	if err != nil {
		a.writeStoreError(w, r, "register agency", err)

		return
	}

	if err := a.sendVerificationEmail(email, rawToken); err != nil {
		// Аккаунт уже создан: пользователь может получить новую ссылку через
		// handleResendVerification, поэтому сбой отправки не откатывает
		// регистрацию — теряется удобство, а не данные.
		a.logger.LogAttrs(r.Context(), slog.LevelError, "send verification email failed",
			slog.String("error", err.Error()))
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"message": "Проверьте почту, чтобы подтвердить регистрацию и войти",
	})
}

type verifyEmailRequest struct {
	Token string `json:"token"`
}

// handleVerifyEmail подтверждает адрес по ссылке из письма и сразу открывает
// сессию — так регистрация заканчивается прямо во вкладке с письмом, без
// отдельного захода на страницу входа.
func (a *api) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	var request verifyEmailRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	if strings.TrimSpace(request.Token) == "" {
		writeErrorFields(w, r, http.StatusBadRequest, codeValidation, "Проверьте заполнение полей",
			map[string]string{"token": "обязательное поле"})

		return
	}

	user, _, err := a.deps.Store.VerifyEmailToken(r.Context(), auth.HashToken(request.Token))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, http.StatusBadRequest, codeValidation, "Ссылка недействительна или устарела")

			return
		}

		a.internalError(w, r, "verify email", err)

		return
	}

	a.startSession(w, r, user, store.ActionUpdate, "Email подтверждён, вход в систему")
}

type resendVerificationRequest struct {
	Email string `json:"email"`
}

// handleResendVerification отвечает одинаково независимо от того,
// существует ли адрес и подтверждён ли он уже — иначе эндпоинт стал бы
// способом проверить, кто уже зарегистрирован в системе.
func (a *api) handleResendVerification(w http.ResponseWriter, r *http.Request) {
	if a.deps.Store == nil || a.deps.MailSender == nil {
		writeError(w, r, http.StatusServiceUnavailable, codeUnavailable, "Регистрация временно недоступна")

		return
	}

	var request resendVerificationRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	email := strings.ToLower(strings.TrimSpace(request.Email))

	const genericResponse = "Если такой адрес зарегистрирован и не подтверждён, письмо отправлено повторно"

	if email == "" {
		writeJSON(w, http.StatusOK, map[string]string{"message": genericResponse})

		return
	}

	if !a.deps.RegisterLimiter.Allow("resend:" + email + "|" + clientIP(r)) {
		writeError(w, r, http.StatusTooManyRequests, codeRateLimited,
			"Слишком много попыток, повторите позже")

		return
	}

	rawToken := auth.NewSessionToken()
	err := a.deps.Store.ReissueVerificationToken(r.Context(), email, auth.HashToken(rawToken), verificationTokenTTL)
	switch {
	case err == nil:
		if sendErr := a.sendVerificationEmail(email, rawToken); sendErr != nil {
			a.logger.LogAttrs(r.Context(), slog.LevelError, "resend verification email failed",
				slog.String("error", sendErr.Error()))
		}
	case errors.Is(err, store.ErrNotFound):
		// Неизвестный или уже подтверждённый адрес — намеренно молчим.
	default:
		a.internalError(w, r, "reissue verification token", err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": genericResponse})
}

func (a *api) sendVerificationEmail(email, rawToken string) error {
	link := a.deps.PublicBaseURL + "/verify-email?token=" + rawToken

	body := "Подтвердите регистрацию в Trip-Pip, перейдя по ссылке:\n\n" + link +
		"\n\nСсылка действительна 24 часа. Если вы не регистрировались в Trip-Pip, просто проигнорируйте это письмо."

	return a.deps.MailSender.Send(email, "Подтверждение регистрации в Trip-Pip", body)
}
