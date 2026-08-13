package httpapi

import (
	"net/http"
	"strings"

	"github.com/lobanovsky/trip-pip-backend/internal/auth"
	"github.com/lobanovsky/trip-pip-backend/internal/store"
)

type createUserRequest struct {
	Email    string `json:"email"`
	FullName string `json:"fullName"`
	Password string `json:"password"`
}

func (a *api) handleListUsers(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	limit, offset := paging(r)

	users, total, err := a.deps.Store.ListUsers(r.Context(), identity.AgencyID, limit, offset)
	if err != nil {
		a.writeStoreError(w, r, "list users", err)

		return
	}

	writeJSON(w, http.StatusOK, listEnvelope[store.User]{Items: users, Total: total, Limit: limit, Offset: offset})
}

// handleCreateUser добавляет коллегу. На первом этапе у всех сотрудников
// агентства одинаковый доступ, поэтому это может сделать любой вошедший в
// систему пользователь; агентство берётся из сессии и никогда — из тела запроса.
func (a *api) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())

	var request createUserRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	fields := map[string]string{}
	email := strings.ToLower(strings.TrimSpace(request.Email))
	fullName := strings.TrimSpace(request.FullName)

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

	user, err := a.deps.Store.CreateUser(r.Context(), identity.AgencyID, email, hash, fullName)
	if err != nil {
		a.writeStoreError(w, r, "create user", err)

		return
	}

	a.journal(r, identity.AgencyID, identity.Actor(RequestID(r.Context())),
		store.EntityUser, user.ID, store.ActionCreate, user.FullName)

	writeJSON(w, http.StatusCreated, user)
}
