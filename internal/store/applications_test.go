package store

import (
	"context"
	"errors"
	"testing"
)

func TestCanTransition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		from, to string
		want     bool
	}{
		{StatusInquiry, StatusSelection, true},
		{StatusInquiry, StatusCompleted, false},
		{StatusInquiry, StatusCancelled, true},
		{StatusCompleted, StatusInquiry, false},
		{StatusCancelled, StatusSelection, false},
		{StatusBooked, StatusApproval, true}, // возврат на стадию назад разрешён
	}

	for _, tt := range tests {
		if got := CanTransition(tt.from, tt.to); got != tt.want {
			t.Errorf("CanTransition(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestCreateApplicationLinksTouristsAndNumbers(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство заявок")
	customer := createTestTourist(t, s, agency.ID, 1)
	companion := createTestTourist(t, s, agency.ID, 2)

	first, err := s.CreateApplication(context.Background(), agency.ID, Actor{Label: "test"},
		ApplicationInput{CustomerTouristID: customer.ID, Currency: "RUB"}, []string{companion.ID})
	if err != nil {
		t.Fatalf("CreateApplication() error = %v", err)
	}
	if first.Number != "1" {
		t.Errorf("first application number = %q, want 1", first.Number)
	}
	if first.Status != StatusInquiry {
		t.Errorf("initial status = %q, want %q", first.Status, StatusInquiry)
	}
	if len(first.Tourists) != 2 {
		t.Fatalf("Tourists = %v, want 2 entries", first.Tourists)
	}

	foundCustomer := false
	for _, link := range first.Tourists {
		if link.TouristID == customer.ID && link.Role == "customer" {
			foundCustomer = true
		}
	}
	if !foundCustomer {
		t.Errorf("customer not linked with role=customer: %+v", first.Tourists)
	}

	// Номера привязаны к агентству, а не глобальны: вторая заявка в том же
	// агентстве продолжает последовательность.
	second, err := s.CreateApplication(context.Background(), agency.ID, Actor{Label: "test"},
		ApplicationInput{CustomerTouristID: customer.ID, Currency: "RUB"}, nil)
	if err != nil {
		t.Fatalf("second CreateApplication() error = %v", err)
	}
	if second.Number != "2" {
		t.Errorf("second application number = %q, want 2", second.Number)
	}

	otherAgency := createTestAgency(t, s, "Другое агентство заявок")
	otherCustomer := createTestTourist(t, s, otherAgency.ID, 3)
	thirdInOtherAgency, err := s.CreateApplication(context.Background(), otherAgency.ID, Actor{Label: "test"},
		ApplicationInput{CustomerTouristID: otherCustomer.ID, Currency: "RUB"}, nil)
	if err != nil {
		t.Fatalf("CreateApplication() in other agency error = %v", err)
	}
	if thirdInOtherAgency.Number != "1" {
		t.Errorf("first application number in a fresh agency = %q, want 1 (numbering must not leak across agencies)",
			thirdInOtherAgency.Number)
	}
}

func TestChangeStatusEnforcesTransitions(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство статусов")
	customer := createTestTourist(t, s, agency.ID, 1)

	app, err := s.CreateApplication(context.Background(), agency.ID, Actor{Label: "test"},
		ApplicationInput{CustomerTouristID: customer.ID, Currency: "RUB"}, nil)
	if err != nil {
		t.Fatalf("CreateApplication() error = %v", err)
	}

	// inquiry -> completed пропускает весь конвейер и должен быть отклонён.
	_, err = s.ChangeStatus(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, StatusCompleted, nil)
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("ChangeStatus() to completed from inquiry error = %v, want *ValidationError", err)
	}

	// отмена без указания причины должна быть отклонена.
	_, err = s.ChangeStatus(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, StatusCancelled, nil)
	if !errors.As(err, &validationErr) {
		t.Fatalf("ChangeStatus() to cancelled without reason error = %v, want *ValidationError", err)
	}

	// Допустимый переход вперёд проходит успешно и попадает в журнал.
	updated, err := s.ChangeStatus(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, StatusSelection, nil)
	if err != nil {
		t.Fatalf("ChangeStatus() to selection error = %v", err)
	}
	if updated.Status != StatusSelection {
		t.Errorf("status = %q, want %q", updated.Status, StatusSelection)
	}

	entries, total, err := s.ListHistory(context.Background(), agency.ID, EntityApplication, app.ID, 10, 0)
	if err != nil {
		t.Fatalf("ListHistory() error = %v", err)
	}
	if total < 2 {
		t.Fatalf("ListHistory() total = %d, want at least 2 (create + status_change)", total)
	}

	foundTransition := false
	for _, entry := range entries {
		if entry.Action == ActionStatusChange {
			foundTransition = true
			if change, ok := entry.Changes["status"]; !ok || change.To != StatusSelection {
				t.Errorf("status_change entry = %+v, want To=%q", entry.Changes, StatusSelection)
			}
		}
	}
	if !foundTransition {
		t.Errorf("no status_change entry found in %+v", entries)
	}
}

func TestApplicationVersionConflict(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство версий")
	customer := createTestTourist(t, s, agency.ID, 1)

	app, err := s.CreateApplication(context.Background(), agency.ID, Actor{Label: "test"},
		ApplicationInput{CustomerTouristID: customer.ID, Currency: "RUB"}, nil)
	if err != nil {
		t.Fatalf("CreateApplication() error = %v", err)
	}

	// Кто-то другой сохраняет запись первым.
	_, err = s.UpdateApplication(context.Background(), agency.ID, app.ID, Actor{Label: "first editor"},
		ApplicationAsInput(app), 0)
	if err != nil {
		t.Fatalf("first UpdateApplication() error = %v", err)
	}

	// Вызывающей стороне, у которой всё ещё версия 1, должно быть сказано
	// перезагрузить страницу, а не молча перезаписать чужое изменение.
	_, err = s.UpdateApplication(context.Background(), agency.ID, app.ID, Actor{Label: "second editor"},
		ApplicationAsInput(app), app.Version)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale UpdateApplication() error = %v, want ErrVersionConflict", err)
	}
}
