package store

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestCanTransition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		from, to string
		want     bool
	}{
		{StatusInquiry, StatusSelection, true},
		{StatusInquiry, StatusCompleted, true},
		{StatusInquiry, StatusCancelled, true},
		{StatusCompleted, StatusInquiry, true},
		{StatusCancelled, StatusSelection, true},
		{StatusBooked, StatusBooked, false},
		{"unknown", StatusApproval, false},
	}

	for _, tt := range tests {
		if got := CanTransition(tt.from, tt.to); got != tt.want {
			t.Errorf("CanTransition(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestAllowedTransitionsReturnsEveryOtherKnownStatus(t *testing.T) {
	t.Parallel()

	got := AllowedTransitions(StatusCompleted)
	want := []string{StatusInquiry, StatusSelection, StatusApproval, StatusBooked, StatusPreparation, StatusCancelled}
	if !slices.Equal(got, want) {
		t.Errorf("AllowedTransitions(%q) = %v, want %v", StatusCompleted, got, want)
	}
	if got := AllowedTransitions("unknown"); len(got) != 0 {
		t.Errorf("AllowedTransitions(unknown) = %v, want empty", got)
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

func TestChangeStatusAllowsAnyKnownStatus(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство статусов")
	customer := createTestTourist(t, s, agency.ID, 1)

	app, err := s.CreateApplication(context.Background(), agency.ID, Actor{Label: "test"},
		ApplicationInput{CustomerTouristID: customer.ID, Currency: "RUB"}, nil)
	if err != nil {
		t.Fatalf("CreateApplication() error = %v", err)
	}

	// Можно сразу выбрать любую рабочую стадию, минуя промежуточные.
	updated, err := s.ChangeStatus(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, StatusCompleted, nil)
	if err != nil {
		t.Fatalf("ChangeStatus() to completed error = %v", err)
	}
	if updated.Status != StatusCompleted {
		t.Errorf("status = %q, want %q", updated.Status, StatusCompleted)
	}

	// отмена без указания причины должна быть отклонена.
	_, err = s.ChangeStatus(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, StatusCancelled, nil)
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("ChangeStatus() to cancelled without reason error = %v, want *ValidationError", err)
	}

	// Из завершённой заявки можно вернуться на любой рабочий этап.
	updated, err = s.ChangeStatus(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, StatusSelection, nil)
	if err != nil {
		t.Fatalf("ChangeStatus() to selection error = %v", err)
	}
	if updated.Status != StatusSelection {
		t.Errorf("status = %q, want %q", updated.Status, StatusSelection)
	}

	reason := "турист отказался"
	updated, err = s.ChangeStatus(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, StatusCancelled, &reason)
	if err != nil {
		t.Fatalf("ChangeStatus() to cancelled error = %v", err)
	}
	if updated.CancelReason == nil || *updated.CancelReason != reason {
		t.Errorf("cancel reason = %v, want %q", updated.CancelReason, reason)
	}

	// Отменённую заявку можно восстановить, причина отмены при этом очищается.
	updated, err = s.ChangeStatus(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, StatusBooked, nil)
	if err != nil {
		t.Fatalf("ChangeStatus() from cancelled error = %v", err)
	}
	if updated.Status != StatusBooked {
		t.Errorf("status = %q, want %q", updated.Status, StatusBooked)
	}
	if updated.CancelReason != nil {
		t.Errorf("cancel reason = %q, want nil", *updated.CancelReason)
	}

	entries, total, err := s.ListHistory(context.Background(), agency.ID, EntityApplication, app.ID, 10, 0)
	if err != nil {
		t.Fatalf("ListHistory() error = %v", err)
	}
	if total < 5 {
		t.Fatalf("ListHistory() total = %d, want at least 5 (create + four status changes)", total)
	}

	foundTargets := map[string]bool{}
	for _, entry := range entries {
		if entry.Action == ActionStatusChange {
			if change, ok := entry.Changes["status"]; ok {
				if target, ok := change.To.(string); ok {
					foundTargets[target] = true
				}
			}
		}
	}
	for _, target := range []string{StatusCompleted, StatusSelection, StatusCancelled, StatusBooked} {
		if !foundTargets[target] {
			t.Errorf("no status_change to %q found in %+v", target, entries)
		}
	}
}

func TestChangeStatusClearsStrayCancelReasonForNonCancelStatus(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство статусов")
	customer := createTestTourist(t, s, agency.ID, 1)

	app, err := s.CreateApplication(context.Background(), agency.ID, Actor{Label: "test"},
		ApplicationInput{CustomerTouristID: customer.ID, Currency: "RUB"}, nil)
	if err != nil {
		t.Fatalf("CreateApplication() error = %v", err)
	}

	reason := "турист отказался"
	if _, err := s.ChangeStatus(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, StatusCancelled, &reason); err != nil {
		t.Fatalf("ChangeStatus() to cancelled error = %v", err)
	}

	// Клиент прислал причину отмены вместе с переходом на рабочий статус — она
	// не должна осесть в базе на не отменённой заявке.
	strayReason := "случайно оставленный текст"
	updated, err := s.ChangeStatus(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, StatusBooked, &strayReason)
	if err != nil {
		t.Fatalf("ChangeStatus() to booked error = %v", err)
	}
	if updated.CancelReason != nil {
		t.Errorf("cancel reason = %q, want nil", *updated.CancelReason)
	}
}

func TestChangeStatusUpdatesCancelReasonWithoutStatusChange(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство статусов")
	customer := createTestTourist(t, s, agency.ID, 1)

	app, err := s.CreateApplication(context.Background(), agency.ID, Actor{Label: "test"},
		ApplicationInput{CustomerTouristID: customer.ID, Currency: "RUB"}, nil)
	if err != nil {
		t.Fatalf("CreateApplication() error = %v", err)
	}

	reason := "турист отказался"
	cancelled, err := s.ChangeStatus(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, StatusCancelled, &reason)
	if err != nil {
		t.Fatalf("ChangeStatus() to cancelled error = %v", err)
	}

	fixedReason := "турист отказался: не подошли даты"
	updated, err := s.ChangeStatus(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, StatusCancelled, &fixedReason)
	if err != nil {
		t.Fatalf("ChangeStatus() same status with new reason error = %v", err)
	}
	if updated.CancelReason == nil || *updated.CancelReason != fixedReason {
		t.Errorf("cancel reason = %v, want %q", updated.CancelReason, fixedReason)
	}
	if !updated.StatusChangedAt.Equal(cancelled.StatusChangedAt) {
		t.Errorf("statusChangedAt = %v, want unchanged %v (only cancelReason changed, not status)",
			updated.StatusChangedAt, cancelled.StatusChangedAt)
	}

	entries, _, err := s.ListHistory(context.Background(), agency.ID, EntityApplication, app.ID, 10, 0)
	if err != nil {
		t.Fatalf("ListHistory() error = %v", err)
	}

	var foundReasonChange bool
	for _, entry := range entries {
		if entry.Action != ActionStatusChange {
			continue
		}
		if change, ok := entry.Changes["cancelReason"]; ok {
			if to, ok := change.To.(string); ok && to == fixedReason {
				foundReasonChange = true
			}
		}
	}
	if !foundReasonChange {
		t.Errorf("no status_change entry recording cancelReason change to %q found in %+v", fixedReason, entries)
	}

	// Резолвинг того же статуса и той же причины — настоящий no-op, без ошибки.
	if _, err := s.ChangeStatus(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, StatusCancelled, &fixedReason); err != nil {
		t.Fatalf("ChangeStatus() true no-op error = %v", err)
	}
}

func TestChangeStatusResendingCancelledRequiresReason(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство статусов")
	customer := createTestTourist(t, s, agency.ID, 1)

	app, err := s.CreateApplication(context.Background(), agency.ID, Actor{Label: "test"},
		ApplicationInput{CustomerTouristID: customer.ID, Currency: "RUB"}, nil)
	if err != nil {
		t.Fatalf("CreateApplication() error = %v", err)
	}

	reason := "турист отказался"
	if _, err := s.ChangeStatus(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, StatusCancelled, &reason); err != nil {
		t.Fatalf("ChangeStatus() to cancelled error = %v", err)
	}

	_, err = s.ChangeStatus(context.Background(), agency.ID, app.ID, Actor{Label: "test"}, StatusCancelled, nil)
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("ChangeStatus() re-cancel without reason error = %v, want *ValidationError", err)
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
