package store

import (
	"context"
	"testing"
)

// advanceApplicationTo устанавливает нужный тесту статус заявки.
func advanceApplicationTo(t *testing.T, s *Store, agencyID, appID, target string) {
	t.Helper()

	if target == StatusCancelled {
		reason := "тестовая отмена"
		if _, err := s.ChangeStatus(context.Background(), agencyID, appID, Actor{Label: "test"}, StatusCancelled, &reason); err != nil {
			t.Fatalf("ChangeStatus(cancelled) error = %v", err)
		}

		return
	}

	if _, err := s.ChangeStatus(context.Background(), agencyID, appID, Actor{Label: "test"}, target, nil); err != nil {
		t.Fatalf("ChangeStatus(%s) error = %v", target, err)
	}
}

func createTestChannel(t *testing.T, s *Store, agencyID, code, name string) AcquisitionChannel {
	t.Helper()

	channel, err := s.CreateChannel(context.Background(), agencyID, code, name, 0)
	if err != nil {
		t.Fatalf("create test channel: %v", err)
	}

	return channel
}

func TestApplicationFunnelCountsStatusesAndMetrics(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство воронки")
	from := Date{Year: 2000, Month: 1, Day: 1}
	to := Date{Year: 2100, Month: 1, Day: 1}

	customer1 := createTestTourist(t, s, agency.ID, 1)
	app1 := createTestApplication(t, s, agency.ID, customer1.ID, "1000.00")
	advanceApplicationTo(t, s, agency.ID, app1.ID, StatusCompleted)

	customer2 := createTestTourist(t, s, agency.ID, 2)
	app2 := createTestApplication(t, s, agency.ID, customer2.ID, "2000.00")
	advanceApplicationTo(t, s, agency.ID, app2.ID, StatusCompleted)

	customer3 := createTestTourist(t, s, agency.ID, 3)
	app3 := createTestApplication(t, s, agency.ID, customer3.ID, "500.00")
	advanceApplicationTo(t, s, agency.ID, app3.ID, StatusCancelled)

	customer4 := createTestTourist(t, s, agency.ID, 4)
	createTestApplication(t, s, agency.ID, customer4.ID, "300.00") // остаётся в inquiry

	funnel, err := s.ApplicationFunnel(context.Background(), agency.ID, from, to)
	if err != nil {
		t.Fatalf("ApplicationFunnel() error = %v", err)
	}

	if funnel.Total != 4 {
		t.Errorf("Total = %d, want 4", funnel.Total)
	}
	if funnel.Completed != 2 {
		t.Errorf("Completed = %d, want 2", funnel.Completed)
	}
	if funnel.Cancelled != 1 {
		t.Errorf("Cancelled = %d, want 1", funnel.Cancelled)
	}
	if funnel.ConversionRate == nil || *funnel.ConversionRate != 2.0/3.0 {
		t.Errorf("ConversionRate = %v, want 0.666...", funnel.ConversionRate)
	}
	if funnel.AverageCheck == nil || *funnel.AverageCheck != "1500.00" {
		t.Errorf("AverageCheck = %v, want 1500.00", funnel.AverageCheck)
	}

	var inquiryCount int
	for _, sc := range funnel.StatusCounts {
		if sc.Status == StatusInquiry {
			inquiryCount = sc.Count
		}
	}
	if inquiryCount != 1 {
		t.Errorf("inquiry count = %d, want 1", inquiryCount)
	}
}

func TestApplicationFunnelNilMetricsWithoutCompletedOrCancelled(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство пустой воронки")
	customer := createTestTourist(t, s, agency.ID, 1)
	createTestApplication(t, s, agency.ID, customer.ID, "100.00") // остаётся в inquiry

	from := Date{Year: 2000, Month: 1, Day: 1}
	to := Date{Year: 2100, Month: 1, Day: 1}

	funnel, err := s.ApplicationFunnel(context.Background(), agency.ID, from, to)
	if err != nil {
		t.Fatalf("ApplicationFunnel() error = %v", err)
	}
	if funnel.ConversionRate != nil {
		t.Errorf("ConversionRate = %v, want nil", *funnel.ConversionRate)
	}
	if funnel.AverageCheck != nil {
		t.Errorf("AverageCheck = %v, want nil", *funnel.AverageCheck)
	}
}

func TestDirectionStatsExcludesEmptyCountryAndOrdersByCount(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство направлений")
	from := Date{Year: 2000, Month: 1, Day: 1}
	to := Date{Year: 2100, Month: 1, Day: 1}

	turkey := "Турция"
	egypt := "Египет"

	for i, country := range []*string{&turkey, &turkey, &egypt, nil} {
		customer := createTestTourist(t, s, agency.ID, 100+i)
		price := "100.00"
		_, err := s.CreateApplication(context.Background(), agency.ID, Actor{Label: "test"},
			ApplicationInput{CustomerTouristID: customer.ID, Currency: "RUB", PriceTotal: &price, Country: country}, nil)
		if err != nil {
			t.Fatalf("CreateApplication() error = %v", err)
		}
	}

	stats, err := s.DirectionStats(context.Background(), agency.ID, from, to, 10)
	if err != nil {
		t.Fatalf("DirectionStats() error = %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("len(stats) = %d, want 2 (empty country excluded)", len(stats))
	}
	if stats[0].Country != "Турция" || stats[0].Applications != 2 {
		t.Errorf("top direction = %+v, want Турция/2", stats[0])
	}
}

func TestOperatorStatsIncludesArchivedOperator(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство операторов")
	from := Date{Year: 2000, Month: 1, Day: 1}
	to := Date{Year: 2100, Month: 1, Day: 1}

	operator := createTestOperator(t, s, agency.ID, "Архивный оператор")
	customer := createTestTourist(t, s, agency.ID, 1)
	price := "777.00"
	_, err := s.CreateApplication(context.Background(), agency.ID, Actor{Label: "test"},
		ApplicationInput{CustomerTouristID: customer.ID, Currency: "RUB", PriceTotal: &price, TourOperatorID: &operator.ID}, nil)
	if err != nil {
		t.Fatalf("CreateApplication() error = %v", err)
	}

	if err := s.ArchiveOperator(context.Background(), agency.ID, operator.ID, Actor{Label: "test"}); err != nil {
		t.Fatalf("ArchiveOperator() error = %v", err)
	}

	stats, err := s.OperatorStats(context.Background(), agency.ID, from, to, 10)
	if err != nil {
		t.Fatalf("OperatorStats() error = %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("len(stats) = %d, want 1 (archived operator still counted)", len(stats))
	}
	if stats[0].Applications != 1 || stats[0].PriceTotal != "777.00" {
		t.Errorf("stats[0] = %+v, want 1 application / 777.00", stats[0])
	}
}

func TestChannelStatsCountsNewTouristsAndApplications(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство каналов")
	from := Date{Year: 2000, Month: 1, Day: 1}
	to := Date{Year: 2100, Month: 1, Day: 1}

	channel := createTestChannel(t, s, agency.ID, "site", "Сайт")

	tourist, err := s.CreateTourist(context.Background(), agency.ID, Actor{Label: "test"},
		TouristInput{LastName: "Ивановa", FirstName: "Мария", AcquisitionChannelID: &channel.ID})
	if err != nil {
		t.Fatalf("CreateTourist() error = %v", err)
	}

	price := "500.00"
	_, err = s.CreateApplication(context.Background(), agency.ID, Actor{Label: "test"},
		ApplicationInput{CustomerTouristID: tourist.ID, Currency: "RUB", PriceTotal: &price, AcquisitionChannelID: &channel.ID}, nil)
	if err != nil {
		t.Fatalf("CreateApplication() error = %v", err)
	}

	stats, err := s.ChannelStats(context.Background(), agency.ID, from, to, 10)
	if err != nil {
		t.Fatalf("ChannelStats() error = %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("len(stats) = %d, want 1", len(stats))
	}
	if stats[0].NewTourists != 1 {
		t.Errorf("NewTourists = %d, want 1", stats[0].NewTourists)
	}
	if stats[0].Applications != 1 || stats[0].PriceTotal != "500.00" {
		t.Errorf("Applications/PriceTotal = %d/%s, want 1/500.00", stats[0].Applications, stats[0].PriceTotal)
	}
}

func TestRepeatCustomerReportDistinguishesPeriodFromAllTime(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство повторных клиентов")
	from := Date{Year: 2000, Month: 1, Day: 1}
	to := Date{Year: 2100, Month: 1, Day: 1}

	// Повторный клиент: 2 неотменённые заявки за всё время.
	repeatCustomer := createTestTourist(t, s, agency.ID, 1)
	createTestApplication(t, s, agency.ID, repeatCustomer.ID, "100.00")
	createTestApplication(t, s, agency.ID, repeatCustomer.ID, "200.00")

	// Клиент с 2 заявками, но одна отменена — не повторный.
	cancelledCustomer := createTestTourist(t, s, agency.ID, 2)
	createTestApplication(t, s, agency.ID, cancelledCustomer.ID, "100.00")
	secondApp := createTestApplication(t, s, agency.ID, cancelledCustomer.ID, "100.00")
	advanceApplicationTo(t, s, agency.ID, secondApp.ID, StatusCancelled)

	// Разовый клиент.
	oneTimeCustomer := createTestTourist(t, s, agency.ID, 3)
	createTestApplication(t, s, agency.ID, oneTimeCustomer.ID, "50.00")

	report, err := s.RepeatCustomerReport(context.Background(), agency.ID, from, to, 10)
	if err != nil {
		t.Fatalf("RepeatCustomerReport() error = %v", err)
	}

	if report.TotalCustomers != 3 {
		t.Errorf("TotalCustomers = %d, want 3", report.TotalCustomers)
	}
	if report.RepeatCustomers != 1 {
		t.Errorf("RepeatCustomers = %d, want 1", report.RepeatCustomers)
	}
	if report.RepeatShare == nil || *report.RepeatShare != 1.0/3.0 {
		t.Errorf("RepeatShare = %v, want 0.333...", report.RepeatShare)
	}
	if len(report.TopRepeatCustomers) != 1 || report.TopRepeatCustomers[0].TouristID != repeatCustomer.ID {
		t.Errorf("TopRepeatCustomers = %+v, want only %s", report.TopRepeatCustomers, repeatCustomer.ID)
	}
	if report.TopRepeatCustomers[0].Applications != 2 {
		t.Errorf("repeat customer Applications = %d, want 2", report.TopRepeatCustomers[0].Applications)
	}
}
