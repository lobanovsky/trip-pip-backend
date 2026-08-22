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

// applicationsTestFixture стоит агентство, сотрудника с активной сессией и
// туриста-заказчика — общий набор для тестов /api/applications.
type applicationsTestFixture struct {
	handler    http.Handler
	cookie     *http.Cookie
	customerID string
}

func setupApplicationsTest(t *testing.T) applicationsTestFixture {
	t.Helper()

	deps := testDeps(t)
	handler := NewHandler(discardLogger(), testVersion, deps)

	agency, err := deps.Store.CreateAgency(context.Background(), "Агентство заявок (HTTP)", nil, "Europe/Moscow")
	if err != nil {
		t.Fatalf("CreateAgency() error = %v", err)
	}

	hash, err := auth.HashPassword("Password1234!")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	user, err := deps.Store.CreateUser(context.Background(), agency.ID, "manager@example.test", hash, "Менеджер")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	token := auth.NewSessionToken()
	if _, err := deps.Store.CreateSession(context.Background(), user.ID, agency.ID,
		auth.HashToken(token), time.Now().Add(time.Hour), "test"); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	customer, err := deps.Store.CreateTourist(context.Background(), agency.ID, store.Actor{Label: "test"},
		store.TouristInput{LastName: "Заказчиков", FirstName: "Клиент"})
	if err != nil {
		t.Fatalf("CreateTourist() error = %v", err)
	}

	return applicationsTestFixture{
		handler:    handler,
		cookie:     &http.Cookie{Name: sessionCookieName, Value: token},
		customerID: customer.ID,
	}
}

func (f applicationsTestFixture) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
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

func TestCreateApplicationAcceptsCountryCodeAndReturnsCountryName(t *testing.T) {
	t.Parallel()

	f := setupApplicationsTest(t)

	response := f.do(t, http.MethodPost, "/api/applications", map[string]any{
		"customerTouristId": f.customerID,
		"currency":          "RUB",
		"countryCode":       "tr",
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", response.Code, response.Body)
	}

	var application store.Application
	if err := json.Unmarshal(response.Body.Bytes(), &application); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if application.CountryCode == nil || *application.CountryCode != "TR" {
		t.Errorf("countryCode = %v, want TR (uppercased)", application.CountryCode)
	}
	if application.Country == nil || *application.Country != "Турция" {
		t.Errorf("country = %v, want Турция", application.Country)
	}
}

func TestCreateApplicationRejectsUnknownCountryCode(t *testing.T) {
	t.Parallel()

	f := setupApplicationsTest(t)

	response := f.do(t, http.MethodPost, "/api/applications", map[string]any{
		"customerTouristId": f.customerID,
		"currency":          "RUB",
		"countryCode":       "ZZ",
	})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", response.Code, response.Body)
	}
}

func TestListApplicationsIncludesTouristCountAndFinance(t *testing.T) {
	t.Parallel()

	f := setupApplicationsTest(t)

	createResponse := f.do(t, http.MethodPost, "/api/applications", map[string]any{
		"customerTouristId": f.customerID,
		"currency":          "RUB",
		"priceTotal":        "500.00",
	})
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body = %s", createResponse.Code, createResponse.Body)
	}

	listResponse := f.do(t, http.MethodGet, "/api/applications", nil)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body = %s", listResponse.Code, listResponse.Body)
	}

	var envelope listEnvelope[store.Application]
	if err := json.Unmarshal(listResponse.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(envelope.Items) != 1 {
		t.Fatalf("Items = %+v, want 1 application", envelope.Items)
	}

	item := envelope.Items[0]
	if item.TouristCount != 1 {
		t.Errorf("TouristCount = %d, want 1 (customer only)", item.TouristCount)
	}
	if item.Finance == nil {
		t.Fatal("Finance = nil, want a summary")
	}
	// application_balances (0003_payment_transactions.sql) отдаёт "0", а не
	// "0.00", когда транзакций ещё нет вовсе: COALESCE(SUM(...), 0) теряет
	// scale numeric(14,2) на literal-фоллбэке — это существующее поведение
	// вьюхи, не то, что добавляет этот список.
	if item.Finance.Transferred != "0" || item.Finance.NetReceived != "0" {
		t.Errorf("Finance = %+v, want zero transferred/netReceived (no transactions yet)", item.Finance)
	}
	if item.Finance.AgencyIncome != "0" {
		t.Errorf("Finance.AgencyIncome = %v, want 0 (nothing received yet)", item.Finance.AgencyIncome)
	}
}

func TestApplicationDetailOmitsTouristCountAndFinance(t *testing.T) {
	t.Parallel()

	f := setupApplicationsTest(t)

	createResponse := f.do(t, http.MethodPost, "/api/applications", map[string]any{
		"customerTouristId": f.customerID,
		"currency":          "RUB",
	})
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body = %s", createResponse.Code, createResponse.Body)
	}

	var raw map[string]any
	if err := json.Unmarshal(createResponse.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// touristCount/finance — расширение только GET /api/applications
	// (списка), не создания/деталей: их не должно быть в ответе вовсе.
	if _, ok := raw["touristCount"]; ok {
		t.Errorf("create response has touristCount, want absent: %+v", raw)
	}
	if _, ok := raw["finance"]; ok {
		t.Errorf("create response has finance, want absent: %+v", raw)
	}
}
