package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func createTestApplication(t *testing.T, s *Store, agencyID, customerID, priceTotal string) Application {
	t.Helper()

	app, err := s.CreateApplication(context.Background(), agencyID, Actor{Label: "test"},
		ApplicationInput{CustomerTouristID: customerID, Currency: "RUB", PriceTotal: &priceTotal}, nil)
	if err != nil {
		t.Fatalf("create test application: %v", err)
	}

	return app
}

func createTestPayer(t *testing.T, s *Store, agencyID, touristID string) Payer {
	t.Helper()

	payer, err := s.CreatePayer(context.Background(), agencyID, Actor{Label: "test"},
		PayerInput{Kind: PayerIndividual, TouristID: &touristID})
	if err != nil {
		t.Fatalf("create test payer: %v", err)
	}

	return payer
}

func createTestOperator(t *testing.T, s *Store, agencyID, name string) TourOperator {
	t.Helper()

	operator, err := s.CreateOperator(context.Background(), agencyID, Actor{Label: "test"}, TourOperatorInput{Name: name})
	if err != nil {
		t.Fatalf("create test operator: %v", err)
	}

	return operator
}

func TestTransactionInputValidateRequiresCounterpartyByKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input TransactionInput
		field string
	}{
		{
			name:  "receipt without payer",
			input: TransactionInput{Kind: TransactionReceipt, Amount: "100", PaymentMethod: PaymentMethodCash, OccurredAt: NewDate(time.Now())},
			field: "payerId",
		},
		{
			name:  "operator transfer without tour operator",
			input: TransactionInput{Kind: TransactionOperatorTransfer, Amount: "100", PaymentMethod: PaymentMethodCash, OccurredAt: NewDate(time.Now())},
			field: "tourOperatorId",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.input.Validate()
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("Validate() error = %v, want *ValidationError", err)
			}
			if _, ok := validationErr.Fields[tt.field]; !ok {
				t.Errorf("Validate() fields = %+v, want %q present", validationErr.Fields, tt.field)
			}
		})
	}
}

func TestTransactionInputValidateFeeOnlyForAcquiring(t *testing.T) {
	t.Parallel()

	payerID := "11111111-1111-1111-1111-111111111111"
	fee := "5.00"
	input := TransactionInput{
		Kind: TransactionReceipt, Amount: "100", PaymentMethod: PaymentMethodCash,
		PayerID: &payerID, FeeAmount: &fee, OccurredAt: NewDate(time.Now()),
	}

	err := input.Validate()
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("Validate() error = %v, want *ValidationError", err)
	}
	if _, ok := validationErr.Fields["feeAmount"]; !ok {
		t.Errorf("Validate() fields = %+v, want \"feeAmount\" present", validationErr.Fields)
	}
}

func TestCreateTransactionReceiptClosesPaymentDeadlineOnceFullyPaid(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство платежей")
	customer := createTestTourist(t, s, agency.ID, 1)
	payer := createTestPayer(t, s, agency.ID, customer.ID)
	app := createTestApplication(t, s, agency.ID, customer.ID, "1000.00")

	deadline, err := s.CreateDeadline(context.Background(), agency.ID, app.ID, "payment", NewDate(time.Now()), nil)
	if err != nil {
		t.Fatalf("CreateDeadline() error = %v", err)
	}

	// Частичная оплата: заявка остаётся в статусе partial, дедлайн открыт.
	_, err = s.CreateTransaction(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, TransactionInput{
		Kind: TransactionReceipt, Amount: "400.00", PaymentMethod: PaymentMethodCash,
		PayerID: &payer.ID, OccurredAt: NewDate(time.Now()),
	})
	if err != nil {
		t.Fatalf("CreateTransaction() partial receipt error = %v", err)
	}

	balance, err := s.ApplicationBalance(context.Background(), agency.ID, app.ID)
	if err != nil {
		t.Fatalf("ApplicationBalance() error = %v", err)
	}
	if balance.PaymentStatus != PaymentStatusPartial {
		t.Errorf("PaymentStatus = %q, want %q", balance.PaymentStatus, PaymentStatusPartial)
	}

	deadlines, err := s.ListDeadlines(context.Background(), agency.ID, app.ID)
	if err != nil {
		t.Fatalf("ListDeadlines() error = %v", err)
	}
	if deadlines[0].CompletedAt != nil {
		t.Fatalf("deadline completed after partial payment, want still open")
	}

	// Доплата закрывает заявку полностью — дедлайн должен закрыться сам.
	_, err = s.CreateTransaction(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, TransactionInput{
		Kind: TransactionReceipt, Amount: "600.00", PaymentMethod: PaymentMethodTransfer,
		PayerID: &payer.ID, OccurredAt: NewDate(time.Now()),
	})
	if err != nil {
		t.Fatalf("CreateTransaction() final receipt error = %v", err)
	}

	balance, err = s.ApplicationBalance(context.Background(), agency.ID, app.ID)
	if err != nil {
		t.Fatalf("ApplicationBalance() error = %v", err)
	}
	if balance.PaymentStatus != PaymentStatusPaid {
		t.Errorf("PaymentStatus = %q, want %q", balance.PaymentStatus, PaymentStatusPaid)
	}
	if balance.NetReceived != "1000.00" {
		t.Errorf("NetReceived = %q, want 1000.00", balance.NetReceived)
	}

	deadlines, err = s.ListDeadlines(context.Background(), agency.ID, app.ID)
	if err != nil {
		t.Fatalf("ListDeadlines() error = %v", err)
	}
	if deadlines[0].CompletedAt == nil {
		t.Errorf("deadline %s not completed after full payment", deadline.ID)
	}
}

func TestCreateTransactionRejectsNonRubApplication(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство валют")
	customer := createTestTourist(t, s, agency.ID, 1)
	payer := createTestPayer(t, s, agency.ID, customer.ID)

	app, err := s.CreateApplication(context.Background(), agency.ID, Actor{Label: "test"},
		ApplicationInput{CustomerTouristID: customer.ID, Currency: "USD"}, nil)
	if err != nil {
		t.Fatalf("CreateApplication() error = %v", err)
	}

	_, err = s.CreateTransaction(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, TransactionInput{
		Kind: TransactionReceipt, Amount: "100.00", PaymentMethod: PaymentMethodCash,
		PayerID: &payer.ID, OccurredAt: NewDate(time.Now()),
	})

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("CreateTransaction() for a USD application error = %v, want *ValidationError", err)
	}
}

func TestApplicationBalanceCommissionAndAgencyIncome(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство комиссий")
	customer := createTestTourist(t, s, agency.ID, 1)
	app := createTestApplication(t, s, agency.ID, customer.ID, "1000.00")
	operator := createTestOperator(t, s, agency.ID, "Оператор комиссий")

	_, err := s.CreateTransaction(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, TransactionInput{
		Kind: TransactionOperatorTransfer, Amount: "850.00", PaymentMethod: PaymentMethodTransfer,
		TourOperatorID: &operator.ID, OccurredAt: NewDate(time.Now()),
	})
	if err != nil {
		t.Fatalf("CreateTransaction() operator_transfer error = %v", err)
	}

	_, err = s.CreateTransaction(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, TransactionInput{
		Kind: TransactionBonusIncome, Amount: "20.00", PaymentMethod: PaymentMethodTransfer,
		TourOperatorID: &operator.ID, OccurredAt: NewDate(time.Now()),
	})
	if err != nil {
		t.Fatalf("CreateTransaction() bonus_income error = %v", err)
	}

	balance, err := s.ApplicationBalance(context.Background(), agency.ID, app.ID)
	if err != nil {
		t.Fatalf("ApplicationBalance() error = %v", err)
	}
	if balance.Commission == nil || *balance.Commission != "150.00" {
		t.Errorf("Commission = %v, want 150.00", balance.Commission)
	}
	if balance.AgencyIncome != "-850.00" {
		t.Errorf("AgencyIncome = %v, want -850.00 (0 received − 850 transferred)", balance.AgencyIncome)
	}
}

func TestVoidTransactionExcludesItFromBalance(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство аннулирования")
	customer := createTestTourist(t, s, agency.ID, 1)
	payer := createTestPayer(t, s, agency.ID, customer.ID)
	app := createTestApplication(t, s, agency.ID, customer.ID, "500.00")

	transaction, err := s.CreateTransaction(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, TransactionInput{
		Kind: TransactionReceipt, Amount: "500.00", PaymentMethod: PaymentMethodCash,
		PayerID: &payer.ID, OccurredAt: NewDate(time.Now()),
	})
	if err != nil {
		t.Fatalf("CreateTransaction() error = %v", err)
	}

	balance, err := s.ApplicationBalance(context.Background(), agency.ID, app.ID)
	if err != nil {
		t.Fatalf("ApplicationBalance() error = %v", err)
	}
	if balance.PaymentStatus != PaymentStatusPaid {
		t.Fatalf("PaymentStatus before void = %q, want %q", balance.PaymentStatus, PaymentStatusPaid)
	}

	if err := s.VoidTransaction(context.Background(), agency.ID, transaction.ID, Actor{Label: "test"}); err != nil {
		t.Fatalf("VoidTransaction() error = %v", err)
	}

	balance, err = s.ApplicationBalance(context.Background(), agency.ID, app.ID)
	if err != nil {
		t.Fatalf("ApplicationBalance() error = %v", err)
	}
	if balance.PaymentStatus != PaymentStatusUnpaid {
		t.Errorf("PaymentStatus after void = %q, want %q", balance.PaymentStatus, PaymentStatusUnpaid)
	}

	entries, _, err := s.ListHistory(context.Background(), agency.ID, EntityPaymentTransaction, transaction.ID, 10, 0)
	if err != nil {
		t.Fatalf("ListHistory() error = %v", err)
	}
	foundArchive := false
	for _, entry := range entries {
		if entry.Action == ActionArchive {
			foundArchive = true
		}
	}
	if !foundArchive {
		t.Errorf("no archive entry found in %+v", entries)
	}
}

func TestListTransactionsFiltersByKindAndApplication(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство фильтров")
	customer := createTestTourist(t, s, agency.ID, 1)
	payer := createTestPayer(t, s, agency.ID, customer.ID)
	operator := createTestOperator(t, s, agency.ID, "Оператор фильтров")
	app := createTestApplication(t, s, agency.ID, customer.ID, "1000.00")

	if _, err := s.CreateTransaction(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, TransactionInput{
		Kind: TransactionReceipt, Amount: "300.00", PaymentMethod: PaymentMethodCash,
		PayerID: &payer.ID, OccurredAt: NewDate(time.Now()),
	}); err != nil {
		t.Fatalf("CreateTransaction() receipt error = %v", err)
	}
	if _, err := s.CreateTransaction(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, TransactionInput{
		Kind: TransactionOperatorTransfer, Amount: "250.00", PaymentMethod: PaymentMethodTransfer,
		TourOperatorID: &operator.ID, OccurredAt: NewDate(time.Now()),
	}); err != nil {
		t.Fatalf("CreateTransaction() operator_transfer error = %v", err)
	}

	transactions, total, err := s.ListTransactions(context.Background(), agency.ID, TransactionFilter{
		ApplicationID: app.ID, Kind: TransactionReceipt, Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListTransactions() error = %v", err)
	}
	if total != 1 || len(transactions) != 1 {
		t.Fatalf("ListTransactions() total = %d, len = %d, want 1", total, len(transactions))
	}
	if transactions[0].Kind != TransactionReceipt {
		t.Errorf("Kind = %q, want %q", transactions[0].Kind, TransactionReceipt)
	}
}

func TestRevenueByPeriodAttributesCommissionToLastTransferPeriod(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство отчётов")
	customer := createTestTourist(t, s, agency.ID, 1)
	operator := createTestOperator(t, s, agency.ID, "Оператор отчётов")
	app := createTestApplication(t, s, agency.ID, customer.ID, "1000.00")

	transferDate := NewDate(time.Now())
	if _, err := s.CreateTransaction(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, TransactionInput{
		Kind: TransactionOperatorTransfer, Amount: "900.00", PaymentMethod: PaymentMethodTransfer,
		TourOperatorID: &operator.ID, OccurredAt: transferDate,
	}); err != nil {
		t.Fatalf("CreateTransaction() error = %v", err)
	}

	from := Date{Year: transferDate.Year, Month: transferDate.Month, Day: 1}
	to := from.AddDays(45)

	periods, err := s.RevenueByPeriod(context.Background(), agency.ID, "month", from, to)
	if err != nil {
		t.Fatalf("RevenueByPeriod() error = %v", err)
	}

	var found bool
	for _, p := range periods {
		if p.Period.Year == transferDate.Year && p.Period.Month == transferDate.Month {
			found = true
			if p.Commission != "100.00" {
				t.Errorf("Commission = %q, want 100.00", p.Commission)
			}
			if p.Transferred != "900.00" {
				t.Errorf("Transferred = %q, want 900.00", p.Transferred)
			}
		}
	}
	if !found {
		t.Fatalf("no period found for %v in %+v", transferDate, periods)
	}
}

func TestRevenueByPeriodRejectsUnknownUnit(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство единиц")

	_, err := s.RevenueByPeriod(context.Background(), agency.ID, "week", NewDate(time.Now()), NewDate(time.Now()))
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("RevenueByPeriod() error = %v, want *ValidationError", err)
	}
}
