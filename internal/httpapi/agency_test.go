package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetAgencyReturnsAgencyData(t *testing.T) {
	t.Parallel()

	f := setupUsersTest(t)

	response := f.doJSON(t, http.MethodGet, "/api/agency", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body)
	}

	var body struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Timezone string `json:"timezone"`
		IsActive bool   `json:"isActive"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body.ID != f.agency.ID {
		t.Errorf("id = %q, want %q", body.ID, f.agency.ID)
	}
	if body.Name != f.agency.Name {
		t.Errorf("name = %q, want %q", body.Name, f.agency.Name)
	}
	if body.Timezone != f.agency.Timezone {
		t.Errorf("timezone = %q, want %q", body.Timezone, f.agency.Timezone)
	}
	if body.IsActive != f.agency.IsActive {
		t.Errorf("isActive = %v, want %v", body.IsActive, f.agency.IsActive)
	}
}

func TestGetAgencyRequiresAuth(t *testing.T) {
	t.Parallel()

	deps := testDeps(t)
	handler := NewHandler(discardLogger(), testVersion, deps)

	request := httptest.NewRequest(http.MethodGet, "/api/agency", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", response.Code, response.Body)
	}
}
