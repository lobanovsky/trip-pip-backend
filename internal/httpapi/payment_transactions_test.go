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

// transactionTestFixture стоит агентство, сотрудника с активной сессией,
// туриста-заказчика, плательщика, туроператора и заявку со стоимостью
// 1000.00 ₽ — общий набор данных для тестов финансового эндпоинта.
type transactionTestFixture struct {
	handler http.Handler
	cookie  *http.Cookie
	appID   string
	payerID string
	operID  string
}

func setupTransactionTest(t *testing.T) transactionTestFixture {
	t.Helper()

	deps := testDeps(t)
	handler := NewHandler(discardLogger(), testVersion, deps)

	agency, err := deps.Store.CreateAgency(context.Background(), "Агентство финансов (HTTP)", nil, "Europe/Moscow")
	if err != nil {
		t.Fatalf("CreateAgency() error = %v", err)
	}

	hash, err := auth.HashPassword("Password1234!")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	user, err := deps.Store.CreateUser(context.Background(), agency.ID, "finance@example.test", hash, "Финансист")
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

	payer, err := deps.Store.CreatePayer(context.Background(), agency.ID, store.Actor{Label: "test"},
		store.PayerInput{Kind: store.PayerIndividual, TouristID: &customer.ID})
	if err != nil {
		t.Fatalf("CreatePayer() error = %v", err)
	}

	operator, err := deps.Store.CreateOperator(context.Background(), agency.ID, store.Actor{Label: "test"},
		store.TourOperatorInput{Name: "Оператор (HTTP)"})
	if err != nil {
		t.Fatalf("CreateOperator() error = %v", err)
	}

	price := "1000.00"
	app, err := deps.Store.CreateApplication(context.Background(), agency.ID, store.Actor{Label: "test"},
		store.ApplicationInput{CustomerTouristID: customer.ID, Currency: "RUB", PriceTotal: &price}, nil)
	if err != nil {
		t.Fatalf("CreateApplication() error = %v", err)
	}

	return transactionTestFixture{
		handler: handler,
		cookie:  &http.Cookie{Name: sessionCookieName, Value: token},
		appID:   app.ID,
		payerID: payer.ID,
		operID:  operator.ID,
	}
}

func (f transactionTestFixture) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
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

func TestCreateTransactionRequiresPayerForReceipt(t *testing.T) {
	t.Parallel()

	f := setupTransactionTest(t)

	response := f.do(t, http.MethodPost, "/api/applications/"+f.appID+"/transactions", map[string]any{
		"kind":          store.TransactionReceipt,
		"amount":        "500.00",
		"paymentMethod": store.PaymentMethodCash,
		"occurredAt":    time.Now().Format("2006-01-02"),
	})

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", response.Code, response.Body)
	}
}

func TestCreateTransactionAndFinanceReflectsBalance(t *testing.T) {
	t.Parallel()

	f := setupTransactionTest(t)
	today := time.Now().Format("2006-01-02")

	create := f.do(t, http.MethodPost, "/api/applications/"+f.appID+"/transactions", map[string]any{
		"kind":          store.TransactionReceipt,
		"amount":        "400.00",
		"payerId":       f.payerID,
		"paymentMethod": store.PaymentMethodCash,
		"occurredAt":    today,
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body = %s", create.Code, create.Body)
	}

	finance := f.do(t, http.MethodGet, "/api/applications/"+f.appID+"/finance", nil)
	if finance.Code != http.StatusOK {
		t.Fatalf("finance status = %d, want 200; body = %s", finance.Code, finance.Body)
	}

	var balance store.Balance
	if err := json.Unmarshal(finance.Body.Bytes(), &balance); err != nil {
		t.Fatalf("decode finance response: %v", err)
	}
	if balance.PaymentStatus != store.PaymentStatusPartial {
		t.Errorf("PaymentStatus = %q, want %q", balance.PaymentStatus, store.PaymentStatusPartial)
	}
	if balance.Received != "400.00" {
		t.Errorf("Received = %q, want 400.00", balance.Received)
	}
}

func TestVoidTransactionEndpoint(t *testing.T) {
	t.Parallel()

	f := setupTransactionTest(t)
	today := time.Now().Format("2006-01-02")

	create := f.do(t, http.MethodPost, "/api/applications/"+f.appID+"/transactions", map[string]any{
		"kind":          store.TransactionReceipt,
		"amount":        "1000.00",
		"payerId":       f.payerID,
		"paymentMethod": store.PaymentMethodCash,
		"occurredAt":    today,
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body = %s", create.Code, create.Body)
	}

	var transaction store.Transaction
	if err := json.Unmarshal(create.Body.Bytes(), &transaction); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	voided := f.do(t, http.MethodDelete, "/api/applications/"+f.appID+"/transactions/"+transaction.ID, nil)
	if voided.Code != http.StatusNoContent {
		t.Fatalf("void status = %d, want 204; body = %s", voided.Code, voided.Body)
	}

	finance := f.do(t, http.MethodGet, "/api/applications/"+f.appID+"/finance", nil)
	var balance store.Balance
	if err := json.Unmarshal(finance.Body.Bytes(), &balance); err != nil {
		t.Fatalf("decode finance response: %v", err)
	}
	if balance.PaymentStatus != store.PaymentStatusUnpaid {
		t.Errorf("PaymentStatus after void = %q, want %q", balance.PaymentStatus, store.PaymentStatusUnpaid)
	}
}

func TestListTransactionsEndpointFiltersByKind(t *testing.T) {
	t.Parallel()

	f := setupTransactionTest(t)
	today := time.Now().Format("2006-01-02")

	if response := f.do(t, http.MethodPost, "/api/applications/"+f.appID+"/transactions", map[string]any{
		"kind": store.TransactionOperatorTransfer, "amount": "700.00",
		"tourOperatorId": f.operID, "paymentMethod": store.PaymentMethodTransfer, "occurredAt": today,
	}); response.Code != http.StatusCreated {
		t.Fatalf("create operator_transfer status = %d, want 201; body = %s", response.Code, response.Body)
	}

	response := f.do(t, http.MethodGet, "/api/transactions?kind="+store.TransactionOperatorTransfer, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body = %s", response.Code, response.Body)
	}

	var envelope listEnvelope[store.Transaction]
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if envelope.Total != 1 {
		t.Fatalf("Total = %d, want 1; body = %s", envelope.Total, response.Body)
	}
	if envelope.Items[0].Kind != store.TransactionOperatorTransfer {
		t.Errorf("Kind = %q, want %q", envelope.Items[0].Kind, store.TransactionOperatorTransfer)
	}
}

func TestRevenueReportEndpointRejectsUnknownUnit(t *testing.T) {
	t.Parallel()

	f := setupTransactionTest(t)

	response := f.do(t, http.MethodGet, "/api/reports/revenue?unit=week", nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", response.Code, response.Body)
	}
}

func TestRevenueReportEndpointReturnsAgencyIncome(t *testing.T) {
	t.Parallel()

	f := setupTransactionTest(t)
	today := time.Now().Format("2006-01-02")

	if response := f.do(t, http.MethodPost, "/api/applications/"+f.appID+"/transactions", map[string]any{
		"kind": store.TransactionReceipt, "amount": "900.00",
		"payerId": f.payerID, "paymentMethod": store.PaymentMethodCash, "occurredAt": today,
	}); response.Code != http.StatusCreated {
		t.Fatalf("create receipt status = %d, want 201; body = %s", response.Code, response.Body)
	}
	if response := f.do(t, http.MethodPost, "/api/applications/"+f.appID+"/transactions", map[string]any{
		"kind": store.TransactionOperatorTransfer, "amount": "850.00",
		"tourOperatorId": f.operID, "paymentMethod": store.PaymentMethodTransfer, "occurredAt": today,
	}); response.Code != http.StatusCreated {
		t.Fatalf("create operator_transfer status = %d, want 201; body = %s", response.Code, response.Body)
	}

	// Границы заданы явно, с запасом в двое суток по обе стороны: время
	// теста может приходиться на несколько минут по разные стороны от
	// полуночи UTC, а Deps.Location в тестах — time.UTC, тогда как occurredAt
	// выше записан по местному времени машины. Дефолтный диапазон ("с начала
	// года по a.today()") в этом окне может не захватить только что
	// созданную транзакцию.
	from := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	to := time.Now().AddDate(0, 0, 2).Format("2006-01-02")
	response := f.do(t, http.MethodGet, "/api/reports/revenue?unit=month&from="+from+"&to="+to, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body)
	}

	var envelope listEnvelope[store.PeriodRevenue]
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode revenue response: %v", err)
	}

	var found bool
	for _, period := range envelope.Items {
		if period.AgencyIncome == "50.00" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no period with agencyIncome=50.00 found in %+v", envelope.Items)
	}
}
