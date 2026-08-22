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

// analyticsTestFixture стоит агентство и сотрудника с активной сессией —
// общий набор для тестов эндпоинтов /api/reports/*, кроме revenue.
type analyticsTestFixture struct {
	deps    Deps
	handler http.Handler
	cookie  *http.Cookie
	agency  store.Agency
}

func setupAnalyticsTest(t *testing.T) analyticsTestFixture {
	t.Helper()

	deps := testDeps(t)
	handler := NewHandler(discardLogger(), testVersion, deps)

	agency, err := deps.Store.CreateAgency(context.Background(), "Агентство аналитики (HTTP)", nil, "Europe/Moscow")
	if err != nil {
		t.Fatalf("CreateAgency() error = %v", err)
	}

	hash, err := auth.HashPassword("Password1234!")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	user, err := deps.Store.CreateUser(context.Background(), agency.ID, "analytics@example.test", hash, "Аналитик")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	token := auth.NewSessionToken()
	if _, err := deps.Store.CreateSession(context.Background(), user.ID, agency.ID,
		auth.HashToken(token), time.Now().Add(time.Hour), "test"); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	return analyticsTestFixture{
		deps:    deps,
		handler: handler,
		cookie:  &http.Cookie{Name: sessionCookieName, Value: token},
		agency:  agency,
	}
}

// safeReportRange returns a "from=...&to=..." query string bracketing "now"
// with a two-day margin on each side — wide enough to catch fixtures created
// during the test regardless of which side of the UTC/local day boundary the
// run happens to land on (see the same fix applied to the revenue report
// test), but well inside the five-year cap enforced by reportDateRange.
func safeReportRange() string {
	from := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	to := time.Now().AddDate(0, 0, 2).Format("2006-01-02")

	return "from=" + from + "&to=" + to
}

func (f analyticsTestFixture) do(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(method, path, bytes.NewReader(nil))
	request.AddCookie(f.cookie)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)

	return response
}

func (f analyticsTestFixture) createTourist(t *testing.T) store.Tourist {
	t.Helper()

	tourist, err := f.deps.Store.CreateTourist(context.Background(), f.agency.ID, store.Actor{Label: "test"},
		store.TouristInput{LastName: "Тестов", FirstName: "Аналитик"})
	if err != nil {
		t.Fatalf("CreateTourist() error = %v", err)
	}

	return tourist
}

func (f analyticsTestFixture) createApplication(t *testing.T, customerID string, input store.ApplicationInput) store.Application {
	t.Helper()

	input.CustomerTouristID = customerID
	input.Currency = "RUB"
	app, err := f.deps.Store.CreateApplication(context.Background(), f.agency.ID, store.Actor{Label: "test"}, input, nil)
	if err != nil {
		t.Fatalf("CreateApplication() error = %v", err)
	}

	return app
}

func TestApplicationFunnelReportEndpoint(t *testing.T) {
	t.Parallel()

	f := setupAnalyticsTest(t)
	price := "1000.00"
	customer := f.createTourist(t)
	app := f.createApplication(t, customer.ID, store.ApplicationInput{PriceTotal: &price})

	if _, err := f.deps.Store.ChangeStatus(context.Background(), f.agency.ID, app.ID,
		store.Actor{Label: "test"}, store.StatusSelection, nil); err != nil {
		t.Fatalf("ChangeStatus() error = %v", err)
	}

	response := f.do(t, http.MethodGet, "/api/reports/applications?"+safeReportRange())
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body)
	}

	var funnel store.ApplicationFunnel
	if err := json.Unmarshal(response.Body.Bytes(), &funnel); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if funnel.Total != 1 {
		t.Errorf("Total = %d, want 1", funnel.Total)
	}
	if funnel.ConversionRate != nil {
		t.Errorf("ConversionRate = %v, want nil (no completed/cancelled yet)", *funnel.ConversionRate)
	}
}

func TestDirectionsReportEndpoint(t *testing.T) {
	t.Parallel()

	f := setupAnalyticsTest(t)
	price := "500.00"
	countryCode := "TR"
	customer := f.createTourist(t)
	f.createApplication(t, customer.ID, store.ApplicationInput{PriceTotal: &price, CountryCode: &countryCode})

	response := f.do(t, http.MethodGet, "/api/reports/directions?"+safeReportRange())
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body)
	}

	var envelope listEnvelope[store.DirectionStat]
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Total != 1 || envelope.Items[0].Country != "Турция" {
		t.Fatalf("directions = %+v, want one entry for Турция", envelope.Items)
	}
}

func TestOperatorsReportEndpoint(t *testing.T) {
	t.Parallel()

	f := setupAnalyticsTest(t)
	operator, err := f.deps.Store.CreateOperator(context.Background(), f.agency.ID, store.Actor{Label: "test"},
		store.TourOperatorInput{Name: "Оператор аналитики"})
	if err != nil {
		t.Fatalf("CreateOperator() error = %v", err)
	}

	price := "900.00"
	customer := f.createTourist(t)
	f.createApplication(t, customer.ID, store.ApplicationInput{PriceTotal: &price, TourOperatorID: &operator.ID})

	response := f.do(t, http.MethodGet, "/api/reports/tour-operators?"+safeReportRange())
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body)
	}

	var envelope listEnvelope[store.OperatorStat]
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Total != 1 || envelope.Items[0].TourOperatorID != operator.ID {
		t.Fatalf("operators = %+v, want one entry for %s", envelope.Items, operator.ID)
	}
}

func TestChannelsReportEndpoint(t *testing.T) {
	t.Parallel()

	f := setupAnalyticsTest(t)
	channel, err := f.deps.Store.CreateChannel(context.Background(), f.agency.ID, "site", "Сайт", 0)
	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}

	tourist, err := f.deps.Store.CreateTourist(context.Background(), f.agency.ID, store.Actor{Label: "test"},
		store.TouristInput{LastName: "Каналов", FirstName: "Клиент", AcquisitionChannelID: &channel.ID})
	if err != nil {
		t.Fatalf("CreateTourist() error = %v", err)
	}

	response := f.do(t, http.MethodGet, "/api/reports/channels?"+safeReportRange())
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body)
	}

	var envelope listEnvelope[store.ChannelStat]
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Total != 1 || envelope.Items[0].ChannelID != channel.ID || envelope.Items[0].NewTourists != 1 {
		t.Fatalf("channels = %+v, want one entry for %s with 1 new tourist (%s)", envelope.Items, channel.ID, tourist.ID)
	}
}

func TestRepeatCustomersReportEndpoint(t *testing.T) {
	t.Parallel()

	f := setupAnalyticsTest(t)
	customer := f.createTourist(t)
	price := "100.00"
	f.createApplication(t, customer.ID, store.ApplicationInput{PriceTotal: &price})
	f.createApplication(t, customer.ID, store.ApplicationInput{PriceTotal: &price})

	response := f.do(t, http.MethodGet, "/api/reports/repeat-customers?"+safeReportRange())
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body)
	}

	var report store.RepeatCustomersReport
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if report.TotalCustomers != 1 || report.RepeatCustomers != 1 {
		t.Fatalf("report = %+v, want 1 total / 1 repeat customer", report)
	}
}

func TestAnalyticsReportsRejectRangeOverFiveYears(t *testing.T) {
	t.Parallel()

	f := setupAnalyticsTest(t)

	paths := []string{
		"/api/reports/applications",
		"/api/reports/directions",
		"/api/reports/tour-operators",
		"/api/reports/channels",
		"/api/reports/repeat-customers",
	}
	for _, path := range paths {
		response := f.do(t, http.MethodGet, path+"?from=2000-01-01&to=2020-01-01")
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400; body = %s", path, response.Code, response.Body)
		}
	}
}
