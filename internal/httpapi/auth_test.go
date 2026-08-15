package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lobanovsky/trip-pip-backend/internal/auth"
	"github.com/lobanovsky/trip-pip-backend/internal/store"
)

// protectedRoutes — представительная выборка из таблицы защищённых
// маршрутов: достаточная, чтобы поймать маршрут, зарегистрированный без
// protect(), но не механическое перечисление всех путей из handler.go.
var protectedRoutes = []struct {
	method, path string
}{
	{http.MethodGet, "/api/auth/session"},
	{http.MethodPost, "/api/auth/logout"},
	{http.MethodGet, "/api/tourists"},
	{http.MethodPost, "/api/tourists"},
	{http.MethodGet, "/api/tourists/00000000-0000-0000-0000-000000000000"},
	{http.MethodGet, "/api/applications"},
	{http.MethodPost, "/api/applications"},
	{http.MethodGet, "/api/partners"},
	{http.MethodGet, "/api/tour-operators"},
	{http.MethodGet, "/api/payers"},
	{http.MethodGet, "/api/transactions"},
	{http.MethodGet, "/api/reports/revenue"},
	{http.MethodGet, "/api/reminders"},
	{http.MethodGet, "/api/users"},
	{http.MethodGet, "/api/references"},
}

// TestProtectedRoutesRequireDatabase — регрессионный тест на режим
// деградации без базы данных: без него деплой до того, как база данных
// развёрнута, падал бы, вместо того чтобы обслуживать /api/ping и /api/version.
func TestProtectedRoutesRequireDatabase(t *testing.T) {
	t.Parallel()

	handler := NewHandler(discardLogger(), testVersion, Deps{})

	for _, route := range protectedRoutes {
		request := httptest.NewRequest(route.method, route.path, nil)
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)

		if response.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s with no database: status = %d, want %d",
				route.method, route.path, response.Code, http.StatusServiceUnavailable)
		}
	}
}

// TestPublicRoutesStayOpenWithoutAuth защищает проверку деплоя: HEALTHCHECK
// в Dockerfile и curl-проверки скрипта деплоя обращаются к этим двум путям
// без cookie и всегда должны получать 200, независимо от базы данных.
func TestPublicRoutesStayOpenWithoutAuth(t *testing.T) {
	t.Parallel()

	for _, deps := range []Deps{{}, testDeps(t)} {
		for _, path := range []string{pingPath, versionPath} {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			response := httptest.NewRecorder()

			NewHandler(discardLogger(), testVersion, deps).ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Errorf("GET %s = %d, want 200", path, response.Code)
			}
		}
	}
}

func TestProtectedRoutesRequireAuth(t *testing.T) {
	handler := NewHandler(discardLogger(), testVersion, testDeps(t))

	for _, route := range protectedRoutes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			request := httptest.NewRequest(route.method, route.path, nil)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d; body = %s", response.Code, http.StatusUnauthorized, response.Body)
			}
		})
	}
}

func TestProtectedRoutesRejectExpiredAndRevokedSessions(t *testing.T) {
	deps, tx := testDepsWithTx(t)
	handler := NewHandler(discardLogger(), testVersion, deps)

	agency, err := deps.Store.CreateAgency(context.Background(), "Агентство сессий", nil, "Europe/Moscow")
	if err != nil {
		t.Fatalf("CreateAgency() error = %v", err)
	}
	hash, err := auth.HashPassword("Password1234!")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	user, err := deps.Store.CreateUser(context.Background(), agency.ID, "session@example.test", hash, "Тест")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	tests := []struct {
		name    string
		expires string
		revoke  bool
	}{
		{name: "expired", expires: "now() - interval '1 hour'"},
		{name: "revoked", expires: "now() + interval '1 hour'", revoke: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := auth.NewSessionToken()

			var sessionID string
			err := tx.QueryRow(context.Background(),
				`INSERT INTO sessions (user_id, agency_id, token_hash, expires_at)
				 VALUES ($1, $2, $3, `+tt.expires+`) RETURNING id`,
				user.ID, agency.ID, auth.HashToken(token)).Scan(&sessionID)
			if err != nil {
				t.Fatalf("insert session: %v", err)
			}

			if tt.revoke {
				if err := deps.Store.RevokeSession(context.Background(), sessionID); err != nil {
					t.Fatalf("RevokeSession() error = %v", err)
				}
			}

			request := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401; body = %s", response.Code, response.Body)
			}
		})
	}
}

func TestLoginRejectsUnknownEmailAndWrongPassword(t *testing.T) {
	deps := testDeps(t)
	handler := NewHandler(discardLogger(), testVersion, deps)

	agency, err := deps.Store.CreateAgency(context.Background(), "Агентство логина", nil, "Europe/Moscow")
	if err != nil {
		t.Fatalf("CreateAgency() error = %v", err)
	}
	hash, err := auth.HashPassword("CorrectPassword1234!")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if _, err := deps.Store.CreateUser(context.Background(), agency.ID, "known@example.test", hash, "Тест"); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	tests := []struct {
		name, email, password string
	}{
		{"unknown email", "nobody@example.test", "whatever"},
		{"wrong password", "known@example.test", "WrongPassword"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"email": tt.email, "password": tt.password})
			request := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401; body = %s", response.Code, response.Body)
			}

			// Оба случая должны отвечать одинаково: клиент не должен уметь
			// отличить «нет такой учётной записи» от «неверный пароль».
			var envelope errorEnvelope
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if envelope.Error.Code != codeUnauthenticated {
				t.Errorf("error code = %q, want %q", envelope.Error.Code, codeUnauthenticated)
			}
		})
	}
}

func TestLoginSucceedsAndSessionWorks(t *testing.T) {
	deps := testDeps(t)
	handler := NewHandler(discardLogger(), testVersion, deps)

	agency, err := deps.Store.CreateAgency(context.Background(), "Агентство успешного входа", nil, "Europe/Moscow")
	if err != nil {
		t.Fatalf("CreateAgency() error = %v", err)
	}
	hash, err := auth.HashPassword("CorrectPassword1234!")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if _, err := deps.Store.CreateUser(context.Background(), agency.ID, "success@example.test", hash, "Успешный Вход"); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	body, _ := json.Marshal(map[string]string{"email": "success@example.test", "password": "CorrectPassword1234!"})
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	loginRequest.Header.Set("Content-Type", "application/json")
	loginResponse := httptest.NewRecorder()

	handler.ServeHTTP(loginResponse, loginRequest)

	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body = %s", loginResponse.Code, loginResponse.Body)
	}

	cookies := loginResponse.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login response set no cookies")
	}

	sessionRequest := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	for _, cookie := range cookies {
		sessionRequest.AddCookie(cookie)
	}
	sessionResponse := httptest.NewRecorder()

	handler.ServeHTTP(sessionResponse, sessionRequest)

	if sessionResponse.Code != http.StatusOK {
		t.Fatalf("session status = %d, want 200; body = %s", sessionResponse.Code, sessionResponse.Body)
	}
}

// TestCrossTenantTouristReturns404 — HTTP-часть изоляции арендаторов:
// набор тестов store доказывает, что запрос ограничен по агентству, этот
// тест доказывает, что обработчик отвечает 404, а не каким-то другим
// статусом, который выдал бы информацию.
func TestCrossTenantTouristReturns404(t *testing.T) {
	deps := testDeps(t)
	handler := NewHandler(discardLogger(), testVersion, deps)

	agencyA, err := deps.Store.CreateAgency(context.Background(), "Агентство А (HTTP)", nil, "Europe/Moscow")
	if err != nil {
		t.Fatalf("CreateAgency() error = %v", err)
	}
	agencyB, err := deps.Store.CreateAgency(context.Background(), "Агентство Б (HTTP)", nil, "Europe/Moscow")
	if err != nil {
		t.Fatalf("CreateAgency() error = %v", err)
	}

	tourist, err := deps.Store.CreateTourist(context.Background(), agencyA.ID, store.Actor{Label: "test"},
		store.TouristInput{LastName: "Секретов", FirstName: "Клиент"})
	if err != nil {
		t.Fatalf("CreateTourist() error = %v", err)
	}

	hashB, err := auth.HashPassword("PasswordB1234!")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	userB, err := deps.Store.CreateUser(context.Background(), agencyB.ID, "b@example.test", hashB, "Пользователь Б")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	token := auth.NewSessionToken()
	if _, err := deps.Store.CreateSession(context.Background(), userB.ID, agencyB.ID,
		auth.HashToken(token), time.Now().Add(time.Hour), "test"); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/tourists/"+tourist.ID, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Errorf("cross-tenant GET status = %d, want 404 (never 403: that would confirm the record exists); body = %s",
			response.Code, response.Body)
	}
}

// TestLoggingNeverRecordsPassportNumbers расширяет существующую гарантию
// TestLoggingOmitsQueryString на тела POST-запросов: номер паспорта,
// отправленный в карточке туриста, никогда не должен попасть в структурированный лог.
func TestLoggingNeverRecordsPassportNumbers(t *testing.T) {
	deps := testDeps(t)

	agency, err := deps.Store.CreateAgency(context.Background(), "Агентство логов", nil, "Europe/Moscow")
	if err != nil {
		t.Fatalf("CreateAgency() error = %v", err)
	}
	hash, err := auth.HashPassword("LogTestPassword1234!")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	user, err := deps.Store.CreateUser(context.Background(), agency.ID, "logs@example.test", hash, "Тест Логов")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	token := auth.NewSessionToken()
	if _, err := deps.Store.CreateSession(context.Background(), user.ID, agency.ID,
		auth.HashToken(token), time.Now().Add(time.Hour), "test"); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	handler := NewHandler(logger, testVersion, deps)

	const passportNumber = "778899"
	body, _ := json.Marshal(map[string]string{
		"lastName": "Фамильный", "firstName": "Имя",
		"passportSeries": "4509", "passportNumber": passportNumber,
	})

	request := httptest.NewRequest(http.MethodPost, "/api/tourists?q="+passportNumber, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("create tourist status = %d, want 201; body = %s", response.Code, response.Body)
	}

	if strings.Contains(buf.String(), passportNumber) {
		t.Errorf("log output contains the passport number: %s", buf.String())
	}
}
