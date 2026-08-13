package store

import (
	"context"
	"errors"
	"testing"
)

func TestCreateAndGetTourist(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство А")

	input := TouristInput{
		LastName:       "Иванов",
		FirstName:      "Пётр",
		PassportSeries: strPtr("4509"),
		PassportNumber: strPtr("123456"),
	}

	created, err := s.CreateTourist(context.Background(), agency.ID, Actor{Label: "test"}, input)
	if err != nil {
		t.Fatalf("CreateTourist() error = %v", err)
	}
	if created.FullName() != "Иванов Пётр" {
		t.Errorf("FullName() = %q, want %q", created.FullName(), "Иванов Пётр")
	}
	if created.Version != 1 {
		t.Errorf("Version = %d, want 1", created.Version)
	}

	loaded, err := s.Tourist(context.Background(), agency.ID, created.ID)
	if err != nil {
		t.Fatalf("Tourist() error = %v", err)
	}
	if loaded.ID != created.ID {
		t.Errorf("loaded id = %v, want %v", loaded.ID, created.ID)
	}
}

func TestTouristMasked(t *testing.T) {
	t.Parallel()

	tourist := Tourist{
		PassportSeries:     strPtr("4509"),
		PassportNumber:     strPtr("123456"),
		IntlPassportNumber: strPtr("751234567"),
	}

	masked := tourist.Masked()

	if *masked.PassportSeries != "****" {
		t.Errorf("masked series = %q, want ****", *masked.PassportSeries)
	}
	if *masked.PassportNumber != "****56" {
		t.Errorf("masked number = %q, want ****56", *masked.PassportNumber)
	}
	if *masked.IntlPassportNumber != "******567" {
		t.Errorf("masked intl number = %q, want ******567", *masked.IntlPassportNumber)
	}

	// Оригинал должен остаться нетронутым: Masked возвращает копию, а не изменяет исходное значение.
	if *tourist.PassportSeries != "4509" {
		t.Errorf("original series mutated: %q", *tourist.PassportSeries)
	}
}

func TestDuplicatePassportIsRejected(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство Б")

	input := TouristInput{
		LastName:       "Сидоров",
		FirstName:      "Олег",
		PassportSeries: strPtr("1111"),
		PassportNumber: strPtr("222222"),
	}

	if _, err := s.CreateTourist(context.Background(), agency.ID, Actor{Label: "test"}, input); err != nil {
		t.Fatalf("first CreateTourist() error = %v", err)
	}

	// Тот же паспорт, но другое имя: это дублирующаяся запись клиента, а не
	// другой человек, поэтому база данных должна отклонить её, даже если
	// уровень API не успевает сравнить два имени.
	dup := input
	dup.FirstName = "Другое Имя"

	_, err := s.CreateTourist(context.Background(), agency.ID, Actor{Label: "test"}, dup)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateTourist() error = %v, want ErrConflict", err)
	}
	if name := ConstraintName(err); name != "tourists_passport_uk" {
		t.Errorf("constraint = %q, want tourists_passport_uk", name)
	}
}

// TestTenantIsolationOnTourists проверяет главную гарантию схемы: агентство
// никогда не должно уметь читать, редактировать или удалять клиента другого
// агентства и не должно узнавать даже о том, существует ли такой клиент.
func TestTenantIsolationOnTourists(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agencyA := createTestAgency(t, s, "Агентство А (изоляция)")
	agencyB := createTestAgency(t, s, "Агентство Б (изоляция)")

	tourist := createTestTourist(t, s, agencyA.ID, 1)

	// Все подтесты ниже используют одну транзакцию (а значит, одно
	// соединение): pgx не допускает параллельного использования одного Tx,
	// поэтому они выполняются последовательно, а не через t.Parallel().
	t.Run("get", func(t *testing.T) {
		_, err := s.Tourist(context.Background(), agencyB.ID, tourist.ID)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("Tourist() from other agency error = %v, want ErrNotFound", err)
		}
	})

	t.Run("update", func(t *testing.T) {
		_, err := s.UpdateTourist(context.Background(), agencyB.ID, tourist.ID, Actor{Label: "test"},
			TouristInput{LastName: "Взлом", FirstName: "Попытка"}, 0)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("UpdateTourist() from other agency error = %v, want ErrNotFound", err)
		}
	})

	t.Run("archive", func(t *testing.T) {
		err := s.ArchiveTourist(context.Background(), agencyB.ID, tourist.ID, Actor{Label: "test"})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("ArchiveTourist() from other agency error = %v, want ErrNotFound", err)
		}
	})

	t.Run("list", func(t *testing.T) {
		results, total, err := s.ListTourists(context.Background(), agencyB.ID, TouristFilter{Limit: 25})
		if err != nil {
			t.Fatalf("ListTourists() error = %v", err)
		}
		if total != 0 || len(results) != 0 {
			t.Fatalf("ListTourists() from other agency returned %d rows, want 0", total)
		}
	})

	t.Run("search finds nothing across agencies", func(t *testing.T) {
		results, total, err := s.ListTourists(context.Background(), agencyB.ID, TouristFilter{Search: "Тестов", Limit: 25})
		if err != nil {
			t.Fatalf("ListTourists() error = %v", err)
		}
		if total != 0 || len(results) != 0 {
			t.Fatalf("search from other agency returned %d rows, want 0", total)
		}
	})

	t.Run("cross tenant reference is rejected at the database level", func(t *testing.T) {
		// Связь заявки агентства Б с туристом агентства А должна не пройти,
		// даже если обработчик забыл отфильтровать по агентству: составной
		// внешний ключ (customer_tourist_id, agency_id) делает вставку такой
		// строки невозможной.
		_, err := s.CreateApplication(context.Background(), agencyB.ID, Actor{Label: "test"},
			ApplicationInput{CustomerTouristID: tourist.ID, Currency: "RUB"}, nil)
		if !errors.Is(err, ErrInvalidReference) {
			t.Fatalf("CreateApplication() with cross-tenant customer error = %v, want ErrInvalidReference", err)
		}
	})
}

func TestTouristValidate(t *testing.T) {
	t.Parallel()

	today := Date{Year: 2026, Month: 8, Day: 13}

	tests := []struct {
		name       string
		input      TouristInput
		wantField  string
		wantNoErrs bool
	}{
		{
			name:      "missing last name",
			input:     TouristInput{FirstName: "Пётр"},
			wantField: "lastName",
		},
		{
			name:      "short passport series",
			input:     TouristInput{LastName: "А", FirstName: "Б", PassportSeries: strPtr("123"), PassportNumber: strPtr("123456")},
			wantField: "passportSeries",
		},
		{
			name:      "passport number without series",
			input:     TouristInput{LastName: "А", FirstName: "Б", PassportNumber: strPtr("123456")},
			wantField: "passportNumber",
		},
		{
			name:      "birth date in the future",
			input:     TouristInput{LastName: "А", FirstName: "Б", BirthDate: &Date{Year: 2030, Month: 1, Day: 1}},
			wantField: "birthDate",
		},
		{
			name: "international passport expiry before issue",
			input: TouristInput{
				LastName: "А", FirstName: "Б",
				IntlPassportIssueDate:  &Date{Year: 2025, Month: 1, Day: 1},
				IntlPassportExpiryDate: &Date{Year: 2020, Month: 1, Day: 1},
			},
			wantField: "intlPassportExpiryDate",
		},
		{
			name:      "both referrer kinds set",
			input:     TouristInput{LastName: "А", FirstName: "Б", ReferrerPartnerID: strPtr("x"), ReferrerTouristID: strPtr("y")},
			wantField: "referrerTouristId",
		},
		{
			name: "valid minimal card",
			input: TouristInput{
				LastName: "Иванов", FirstName: "Пётр",
				Phone: strPtr("+79991234567"),
			},
			wantNoErrs: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.input.Validate(today)
			if tt.wantNoErrs {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}

				return
			}

			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("Validate() error = %v, want *ValidationError", err)
			}
			if _, ok := validationErr.Fields[tt.wantField]; !ok {
				t.Errorf("Validate() fields = %v, want field %q", validationErr.Fields, tt.wantField)
			}
		})
	}
}

func TestNormalizePhone(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		{"89991234567", "+79991234567"},
		{"+7 (999) 123-45-67", "+79991234567"},
		{"9991234567", "+79991234567"},
		{"79991234567", "+79991234567"},
		{"+380991234567", "+380991234567"},
		{"", ""},
	}

	for _, tt := range tests {
		if got := NormalizePhone(tt.in); got != tt.want {
			t.Errorf("NormalizePhone(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func strPtr(s string) *string { return &s }
