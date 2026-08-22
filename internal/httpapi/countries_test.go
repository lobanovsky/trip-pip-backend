package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lobanovsky/trip-pip-backend/internal/auth"
	"github.com/lobanovsky/trip-pip-backend/internal/store"
)

func TestListCountriesReturnsAllCountries(t *testing.T) {
	t.Parallel()

	f := setupUsersTest(t)

	response := f.doJSON(t, http.MethodGet, "/api/countries", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body)
	}

	var body listEnvelope[store.Country]
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(body.Items) == 0 {
		t.Fatal("Items is empty, want at least one country")
	}
	if body.Total != len(body.Items) {
		t.Errorf("Total = %d, want %d", body.Total, len(body.Items))
	}

	seen := make(map[string]bool, len(body.Items))
	for _, country := range body.Items {
		if len(country.Code) != 2 || country.Code != strings.ToUpper(country.Code) {
			t.Errorf("Code = %q, want two uppercase letters", country.Code)
		}
		if country.Name == "" {
			t.Errorf("Name is empty for code %q", country.Code)
		}
		if !country.IsActive {
			t.Errorf("IsActive = false for %q, want true by default", country.Code)
		}
		if country.SortOrder != 0 {
			t.Errorf("SortOrder = %d for %q, want 0 by default", country.SortOrder, country.Code)
		}
		if seen[country.Code] {
			t.Errorf("duplicate country code %q", country.Code)
		}
		seen[country.Code] = true
	}
}

func TestListCountriesRequiresAuth(t *testing.T) {
	t.Parallel()

	deps := testDeps(t)
	handler := NewHandler(discardLogger(), testVersion, deps)

	request := httptest.NewRequest(http.MethodGet, "/api/countries", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", response.Code, response.Body)
	}
}

func TestUpdateCountryVisibilityRequiresAuth(t *testing.T) {
	t.Parallel()

	deps := testDeps(t)
	handler := NewHandler(discardLogger(), testVersion, deps)

	request := httptest.NewRequest(http.MethodPatch, "/api/countries/RU", strings.NewReader(`{"isActive":false}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", response.Code, response.Body)
	}
}

func TestUpdateCountryVisibilityChangesListing(t *testing.T) {
	t.Parallel()

	f := setupUsersTest(t)

	response := f.doJSON(t, http.MethodPatch, "/api/countries/RU", map[string]any{
		"isActive": false, "sortOrder": 5,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body)
	}

	var updated store.Country
	if err := json.Unmarshal(response.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if updated.IsActive {
		t.Error("IsActive = true, want false after update")
	}
	if updated.SortOrder != 5 {
		t.Errorf("SortOrder = %d, want 5", updated.SortOrder)
	}

	list := f.doJSON(t, http.MethodGet, "/api/countries", nil)
	var body listEnvelope[store.Country]
	if err := json.Unmarshal(list.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, country := range body.Items {
		if country.Code != "RU" {
			continue
		}
		if country.IsActive {
			t.Error("GET /api/countries still shows RU as active after PATCH")
		}
		if country.SortOrder != 5 {
			t.Errorf("GET /api/countries SortOrder = %d, want 5", country.SortOrder)
		}
	}
}

func TestUpdateCountryVisibilityRejectsUnknownCode(t *testing.T) {
	t.Parallel()

	f := setupUsersTest(t)

	response := f.doJSON(t, http.MethodPatch, "/api/countries/ZZ", map[string]any{"isActive": false})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", response.Code, response.Body)
	}
}

func TestUpdateCountryVisibilityDoesNotAffectOtherAgencies(t *testing.T) {
	t.Parallel()

	f := setupUsersTest(t)

	// Второе агентство в той же тестовой транзакции, чтобы проверить
	// изоляцию: PATCH агентства A не должен быть виден агентству B.
	otherAgency, err := f.deps.Store.CreateAgency(context.Background(), "Другое агентство", nil, "Europe/Moscow")
	if err != nil {
		t.Fatalf("CreateAgency() error = %v", err)
	}
	hash, err := auth.HashPassword("Password1234!")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	otherUser, err := f.deps.Store.CreateUser(context.Background(), otherAgency.ID, "other@example.test", hash, "Другой Пользователь")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	otherToken := auth.NewSessionToken()
	if _, err := f.deps.Store.CreateSession(context.Background(), otherUser.ID, otherAgency.ID,
		auth.HashToken(otherToken), time.Now().Add(time.Hour), "test"); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	otherCookie := &http.Cookie{Name: sessionCookieName, Value: otherToken}

	patchResponse := f.doJSON(t, http.MethodPatch, "/api/countries/RU", map[string]any{"isActive": false})
	if patchResponse.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", patchResponse.Code, patchResponse.Body)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/countries", nil)
	request.AddCookie(otherCookie)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)

	var body listEnvelope[store.Country]
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, country := range body.Items {
		if country.Code == "RU" && !country.IsActive {
			t.Error("other agency sees RU as inactive after agency A's PATCH — tenant isolation broken")
		}
	}
}
