package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lobanovsky/trip-pip-backend/internal/auth"
	"github.com/lobanovsky/trip-pip-backend/internal/store"
)

// usersTestFixture стоит агентство и сотрудника с активной сессией — общий
// набор для тестов /api/users/{id}.
type usersTestFixture struct {
	deps    Deps
	handler http.Handler
	cookie  *http.Cookie
	agency  store.Agency
	user    store.User
}

func setupUsersTest(t *testing.T) usersTestFixture {
	t.Helper()

	deps := testDeps(t)
	handler := NewHandler(discardLogger(), testVersion, deps)

	agency, err := deps.Store.CreateAgency(context.Background(), "Агентство пользователей", nil, "Europe/Moscow")
	if err != nil {
		t.Fatalf("CreateAgency() error = %v", err)
	}

	hash, err := auth.HashPassword("Password1234!")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	user, err := deps.Store.CreateUser(context.Background(), agency.ID, "colleague@example.test", hash, "Исходное Имя")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	token := auth.NewSessionToken()
	if _, err := deps.Store.CreateSession(context.Background(), user.ID, agency.ID,
		auth.HashToken(token), time.Now().Add(time.Hour), "test"); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	return usersTestFixture{deps: deps, handler: handler, cookie: &http.Cookie{Name: sessionCookieName, Value: token}, agency: agency, user: user}
}

func (f usersTestFixture) doJSON(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}

	request := httptest.NewRequest(method, path, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.AddCookie(f.cookie)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)

	return response
}

func TestUpdateUserChangesFullName(t *testing.T) {
	t.Parallel()

	f := setupUsersTest(t)

	response := f.doJSON(t, http.MethodPatch, "/api/users/"+f.user.ID, map[string]string{"fullName": "Новое Имя"})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body)
	}

	var user store.User
	if err := json.Unmarshal(response.Body.Bytes(), &user); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if user.FullName != "Новое Имя" {
		t.Errorf("fullName = %q, want %q", user.FullName, "Новое Имя")
	}

	stored, err := f.deps.Store.User(context.Background(), f.agency.ID, f.user.ID)
	if err != nil {
		t.Fatalf("User() error = %v", err)
	}
	if stored.FullName != "Новое Имя" {
		t.Errorf("stored fullName = %q, want %q", stored.FullName, "Новое Имя")
	}
}

func TestUpdateUserValidatesFullName(t *testing.T) {
	t.Parallel()

	f := setupUsersTest(t)

	response := f.doJSON(t, http.MethodPatch, "/api/users/"+f.user.ID, map[string]string{"fullName": "   "})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", response.Code, response.Body)
	}

	var body struct {
		Error struct {
			Fields map[string]string `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := body.Error.Fields["fullName"]; !ok {
		t.Errorf("fields = %+v, want fullName present", body.Error.Fields)
	}
}

func TestUpdateUserScopedToAgency(t *testing.T) {
	t.Parallel()

	f := setupUsersTest(t)

	otherAgency, err := f.deps.Store.CreateAgency(context.Background(), "Другое агентство", nil, "Europe/Moscow")
	if err != nil {
		t.Fatalf("CreateAgency() error = %v", err)
	}
	hash, err := auth.HashPassword("Password1234!")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	otherUser, err := f.deps.Store.CreateUser(context.Background(), otherAgency.ID, "other@example.test", hash, "Чужой")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	response := f.doJSON(t, http.MethodPatch, "/api/users/"+otherUser.ID, map[string]string{"fullName": "Захват"})
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", response.Code, response.Body)
	}
}
