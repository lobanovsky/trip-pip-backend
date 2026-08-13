package store

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Семь стадий жизненного цикла, через которые проходит заявка.
const (
	StatusInquiry     = "inquiry"     // первичное обращение
	StatusSelection   = "selection"   // подбор
	StatusApproval    = "approval"    // согласование
	StatusBooked      = "booked"      // бронирование
	StatusPreparation = "preparation" // подготовка к поездке
	StatusCompleted   = "completed"   // завершение
	StatusCancelled   = "cancelled"   // отмена
)

// AllStatuses — упорядоченный жизненный цикл, используется эндпоинтом справочников.
var AllStatuses = []string{
	StatusInquiry, StatusSelection, StatusApproval,
	StatusBooked, StatusPreparation, StatusCompleted, StatusCancelled,
}

// statusTransitions — граф допустимых переходов жизненного цикла. Заявка
// движется вперёд по одной стадии за раз и может быть отменена из любого
// нетерминального состояния; из терминальной стадии выхода нет.
var statusTransitions = map[string][]string{
	StatusInquiry:     {StatusSelection, StatusCancelled},
	StatusSelection:   {StatusApproval, StatusInquiry, StatusCancelled},
	StatusApproval:    {StatusBooked, StatusSelection, StatusCancelled},
	StatusBooked:      {StatusPreparation, StatusApproval, StatusCancelled},
	StatusPreparation: {StatusCompleted, StatusBooked, StatusCancelled},
	StatusCompleted:   {},
	StatusCancelled:   {},
}

// CanTransition сообщает, может ли заявка перейти между двумя стадиями.
func CanTransition(from, to string) bool {
	return slices.Contains(statusTransitions[from], to)
}

// AllowedTransitions перечисляет стадии, достижимые из текущей.
func AllowedTransitions(from string) []string {
	allowed := statusTransitions[from]
	if allowed == nil {
		return []string{}
	}

	return allowed
}

// Application — единица работы от первого обращения до завершения или
// отмены поездки.
type Application struct {
	ID              string    `json:"id"`
	Number          string    `json:"number"`
	Status          string    `json:"status"`
	StatusChangedAt time.Time `json:"statusChangedAt"`

	CustomerTouristID    string  `json:"customerTouristId"`
	ManagerUserID        *string `json:"managerUserId,omitempty"`
	PayerID              *string `json:"payerId,omitempty"`
	TourOperatorID       *string `json:"tourOperatorId,omitempty"`
	AcquisitionChannelID *string `json:"acquisitionChannelId,omitempty"`

	Country    *string `json:"country,omitempty"`
	City       *string `json:"city,omitempty"`
	Resort     *string `json:"resort,omitempty"`
	Hotel      *string `json:"hotel,omitempty"`
	DepartDate *Date   `json:"departDate,omitempty"`
	ReturnDate *Date   `json:"returnDate,omitempty"`
	Adults     *int    `json:"adults,omitempty"`
	Children   *int    `json:"children,omitempty"`
	PriceTotal *string `json:"priceTotal,omitempty"`
	Currency   string  `json:"currency"`

	Note         *string `json:"note,omitempty"`
	CancelReason *string `json:"cancelReason,omitempty"`

	Version   int       `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	// Tourists заполняется эндпоинтом деталей, а не запросами списка.
	Tourists []ApplicationTourist `json:"tourists,omitempty"`
}

// ApplicationTourist связывает путешественника с заявкой.
type ApplicationTourist struct {
	TouristID string `json:"touristId"`
	FullName  string `json:"fullName,omitempty"`
	Role      string `json:"role"`
	Position  int    `json:"position"`
}

// ApplicationInput — изменяемая часть заявки. Статуса здесь нет: он меняется
// через собственный эндпоинт, чтобы каждый переход проверялся и попадал в журнал.
type ApplicationInput struct {
	CustomerTouristID    string  `json:"customerTouristId" history:"customerTouristId"`
	ManagerUserID        *string `json:"managerUserId" history:"managerUserId"`
	PayerID              *string `json:"payerId" history:"payerId"`
	TourOperatorID       *string `json:"tourOperatorId" history:"tourOperatorId"`
	AcquisitionChannelID *string `json:"acquisitionChannelId" history:"acquisitionChannelId"`

	Country    *string `json:"country" history:"country"`
	City       *string `json:"city" history:"city"`
	Resort     *string `json:"resort" history:"resort"`
	Hotel      *string `json:"hotel" history:"hotel"`
	DepartDate *Date   `json:"departDate" history:"departDate"`
	ReturnDate *Date   `json:"returnDate" history:"returnDate"`
	Adults     *int    `json:"adults" history:"adults"`
	Children   *int    `json:"children" history:"children"`
	PriceTotal *string `json:"priceTotal" history:"priceTotal"`
	Currency   string  `json:"currency" history:"currency"`

	Note *string `json:"note" history:"note"`
}

// Normalize обрезает пробелы в тексте и заполняет значения по умолчанию, ожидаемые схемой.
func (in *ApplicationInput) Normalize() {
	trimPtr(&in.Country)
	trimPtr(&in.City)
	trimPtr(&in.Resort)
	trimPtr(&in.Hotel)
	trimPtr(&in.Note)
	trimPtr(&in.PriceTotal)

	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.Currency == "" {
		in.Currency = "RUB"
	}
}

// Validate сообщает обо всех проблемах сразу.
func (in ApplicationInput) Validate() error {
	v := newValidator()

	if strings.TrimSpace(in.CustomerTouristID) == "" {
		v.add("customerTouristId", "укажите заказчика")
	}

	v.optional("country", in.Country, 100)
	v.optional("city", in.City, 100)
	v.optional("resort", in.Resort, 100)
	v.optional("hotel", in.Hotel, 200)
	v.optional("note", in.Note, 4000)

	if !currencyRe.MatchString(in.Currency) {
		v.add("currency", "код валюты — три заглавные латинские буквы")
	}

	if in.DepartDate != nil && in.ReturnDate != nil &&
		!in.DepartDate.IsZero() && !in.ReturnDate.IsZero() && in.ReturnDate.Before(*in.DepartDate) {
		v.add("returnDate", "дата возвращения раньше даты вылета")
	}

	if in.Adults != nil && (*in.Adults < 0 || *in.Adults > 30) {
		v.add("adults", "от 0 до 30")
	}
	if in.Children != nil && (*in.Children < 0 || *in.Children > 30) {
		v.add("children", "от 0 до 30")
	}

	if in.PriceTotal != nil && *in.PriceTotal != "" {
		amount, err := strconv.ParseFloat(*in.PriceTotal, 64)
		switch {
		case err != nil:
			v.add("priceTotal", "сумма должна быть числом")
		case amount < 0:
			v.add("priceTotal", "сумма не может быть отрицательной")
		}
	}

	return v.err()
}

const applicationColumns = `id, number, status, status_changed_at,
	customer_tourist_id, manager_user_id, payer_id, tour_operator_id, acquisition_channel_id,
	country, city, resort, hotel, depart_date, return_date, adults, children,
	price_total::text, currency, note, cancel_reason, version, created_at, updated_at`

func applicationScanTargets(a *Application) []any {
	return []any{
		&a.ID, &a.Number, &a.Status, &a.StatusChangedAt,
		&a.CustomerTouristID, &a.ManagerUserID, &a.PayerID, &a.TourOperatorID, &a.AcquisitionChannelID,
		&a.Country, &a.City, &a.Resort, &a.Hotel, &a.DepartDate, &a.ReturnDate, &a.Adults, &a.Children,
		&a.PriceTotal, &a.Currency, &a.Note, &a.CancelReason, &a.Version, &a.CreatedAt, &a.UpdatedAt,
	}
}

func scanApplication(row interface{ Scan(...any) error }) (Application, error) {
	var application Application
	if err := row.Scan(applicationScanTargets(&application)...); err != nil {
		return Application{}, mapError(err)
	}

	return application, nil
}

func applicationArgs(input ApplicationInput) []any {
	return []any{
		input.CustomerTouristID, input.ManagerUserID, input.PayerID, input.TourOperatorID,
		input.AcquisitionChannelID, input.Country, input.City, input.Resort, input.Hotel,
		input.DepartDate, input.ReturnDate, input.Adults, input.Children,
		emptyNumeric(input.PriceTotal), input.Currency, input.Note,
	}
}

// emptyNumeric не даёт пустой цене попасть в числовую колонку.
func emptyNumeric(value *string) *string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}

	return value
}

// nextApplicationNumber выдаёт следующий номер для конкретного агентства.
//
// Счётчик хранится в собственной таблице, а не в последовательности
// PostgreSQL, потому что нумерация начинается заново с 1 для каждого
// агентства: общая последовательность позволила бы одному агентству по
// пробелам в номерах судить об объёме дел других агентств.
func (s *Store) nextApplicationNumber(ctx context.Context, agencyID string) (string, error) {
	const query = `
		INSERT INTO agency_sequences (agency_id, name, last_value)
		VALUES ($1, 'application', 1)
		ON CONFLICT (agency_id, name)
		DO UPDATE SET last_value = agency_sequences.last_value + 1
		RETURNING last_value`

	var value int64
	if err := s.db.QueryRow(ctx, query, agencyID).Scan(&value); err != nil {
		return "", mapError(err)
	}

	return strconv.FormatInt(value, 10), nil
}

// CreateApplication открывает новую заявку, связывает её туристов и
// записывает всё это в журнал в одной транзакции.
func (s *Store) CreateApplication(ctx context.Context, agencyID string, actor Actor, input ApplicationInput, touristIDs []string) (Application, error) {
	var created Application
	err := s.inTx(ctx, func(tx *Store) error {
		number, err := tx.nextApplicationNumber(ctx, agencyID)
		if err != nil {
			return err
		}

		const query = `
			INSERT INTO applications (
			    agency_id, number, customer_tourist_id, manager_user_id, payer_id, tour_operator_id,
			    acquisition_channel_id, country, city, resort, hotel, depart_date, return_date,
			    adults, children, price_total, currency, note, created_by, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $19)
			RETURNING ` + applicationColumns

		args := append([]any{agencyID, number}, applicationArgs(input)...)
		args = append(args, nullString(actor.UserID))

		application, err := scanApplication(tx.db.QueryRow(ctx, query, args...))
		if err != nil {
			return err
		}

		if err := tx.replaceApplicationTourists(ctx, agencyID, application.ID, input.CustomerTouristID, touristIDs); err != nil {
			return err
		}

		application.Tourists, err = tx.applicationTourists(ctx, agencyID, application.ID)
		if err != nil {
			return err
		}
		created = application

		return tx.recordChange(ctx, agencyID, actor, changeRecord{
			EntityType: EntityApplication,
			EntityID:   application.ID,
			Action:     ActionCreate,
			Changes:    diff(ApplicationInput{}, input),
			Summary:    "Заявка № " + application.Number,
		})
	})

	return created, err
}

// Application загружает одну заявку вместе с её путешественниками.
func (s *Store) Application(ctx context.Context, agencyID, id string) (Application, error) {
	const query = `
		SELECT ` + applicationColumns + `
		FROM applications
		WHERE agency_id = $1 AND id = $2 AND archived_at IS NULL`

	application, err := scanApplication(s.db.QueryRow(ctx, query, agencyID, id))
	if err != nil {
		return Application{}, err
	}

	application.Tourists, err = s.applicationTourists(ctx, agencyID, id)
	if err != nil {
		return Application{}, err
	}

	return application, nil
}

// UpdateApplication заменяет изменяемые поля.
func (s *Store) UpdateApplication(ctx context.Context, agencyID, id string, actor Actor, input ApplicationInput, expectedVersion int) (Application, error) {
	var updated Application
	err := s.inTx(ctx, func(tx *Store) error {
		before, err := tx.Application(ctx, agencyID, id)
		if err != nil {
			return err
		}
		if expectedVersion != 0 && before.Version != expectedVersion {
			return ErrVersionConflict
		}

		const query = `
			UPDATE applications SET
			    customer_tourist_id = $3, manager_user_id = $4, payer_id = $5, tour_operator_id = $6,
			    acquisition_channel_id = $7, country = $8, city = $9, resort = $10, hotel = $11,
			    depart_date = $12, return_date = $13, adults = $14, children = $15,
			    price_total = $16, currency = $17, note = $18, updated_by = $19, version = version + 1
			WHERE agency_id = $1 AND id = $2 AND archived_at IS NULL
			RETURNING ` + applicationColumns

		args := append([]any{agencyID, id}, applicationArgs(input)...)
		args = append(args, nullString(actor.UserID))

		application, err := scanApplication(tx.db.QueryRow(ctx, query, args...))
		if err != nil {
			return err
		}

		// Заказчик всегда должен быть одним из путешественников по этой поездке.
		if err := tx.ensureCustomerLinked(ctx, agencyID, id, input.CustomerTouristID); err != nil {
			return err
		}

		application.Tourists, err = tx.applicationTourists(ctx, agencyID, id)
		if err != nil {
			return err
		}
		updated = application

		changes := diff(applicationInput(before), input)
		if len(changes) == 0 {
			return nil
		}

		return tx.recordChange(ctx, agencyID, actor, changeRecord{
			EntityType: EntityApplication,
			EntityID:   id,
			Action:     ActionUpdate,
			Changes:    changes,
			Summary:    "Заявка № " + application.Number,
		})
	})

	return updated, err
}

func applicationInput(a Application) ApplicationInput {
	return ApplicationInput{
		CustomerTouristID:    a.CustomerTouristID,
		ManagerUserID:        a.ManagerUserID,
		PayerID:              a.PayerID,
		TourOperatorID:       a.TourOperatorID,
		AcquisitionChannelID: a.AcquisitionChannelID,
		Country:              a.Country,
		City:                 a.City,
		Resort:               a.Resort,
		Hotel:                a.Hotel,
		DepartDate:           a.DepartDate,
		ReturnDate:           a.ReturnDate,
		Adults:               a.Adults,
		Children:             a.Children,
		PriceTotal:           a.PriceTotal,
		Currency:             a.Currency,
		Note:                 a.Note,
	}
}

// ApplicationAsInput представляет сохранённую заявку в форме, в которую сливается PATCH.
func ApplicationAsInput(a Application) ApplicationInput { return applicationInput(a) }

// ChangeStatus продвигает заявку по её жизненному циклу.
func (s *Store) ChangeStatus(ctx context.Context, agencyID, id string, actor Actor, status string, reason *string) (Application, error) {
	var updated Application
	err := s.inTx(ctx, func(tx *Store) error {
		before, err := tx.Application(ctx, agencyID, id)
		if err != nil {
			return err
		}

		if before.Status == status {
			updated = before

			return nil
		}

		if !CanTransition(before.Status, status) {
			return &ValidationError{Fields: map[string]string{
				"status": fmt.Sprintf("из статуса %q нельзя перейти в %q", before.Status, status),
			}}
		}

		if status == StatusCancelled && (reason == nil || strings.TrimSpace(*reason) == "") {
			return &ValidationError{Fields: map[string]string{
				"cancelReason": "укажите причину отмены",
			}}
		}

		const query = `
			UPDATE applications
			SET status = $3, status_changed_at = now(), cancel_reason = $4,
			    updated_by = $5, version = version + 1
			WHERE agency_id = $1 AND id = $2 AND archived_at IS NULL
			RETURNING ` + applicationColumns

		application, err := scanApplication(tx.db.QueryRow(ctx, query, agencyID, id, status, reason, nullString(actor.UserID)))
		if err != nil {
			return err
		}
		updated = application

		return tx.recordChange(ctx, agencyID, actor, changeRecord{
			EntityType: EntityApplication,
			EntityID:   id,
			Action:     ActionStatusChange,
			Changes:    map[string]Change{"status": {From: before.Status, To: status}},
			Summary:    "Заявка № " + application.Number,
		})
	})

	return updated, err
}

// ArchiveApplication мягко удаляет заявку.
func (s *Store) ArchiveApplication(ctx context.Context, agencyID, id string, actor Actor) error {
	return s.inTx(ctx, func(tx *Store) error {
		const query = `
			UPDATE applications SET archived_at = now(), updated_by = $3
			WHERE agency_id = $1 AND id = $2 AND archived_at IS NULL
			RETURNING number`

		var number string
		if err := tx.db.QueryRow(ctx, query, agencyID, id, nullString(actor.UserID)).Scan(&number); err != nil {
			return mapError(err)
		}

		return tx.recordChange(ctx, agencyID, actor, changeRecord{
			EntityType: EntityApplication,
			EntityID:   id,
			Action:     ActionArchive,
			Summary:    "Заявка № " + number,
		})
	})
}

// Путешественники ---------------------------------------------------------------

func (s *Store) applicationTourists(ctx context.Context, agencyID, applicationID string) ([]ApplicationTourist, error) {
	const query = `
		SELECT at.tourist_id, t.last_name || ' ' || t.first_name, at.role, at.position
		FROM application_tourists at
		JOIN tourists t ON t.id = at.tourist_id
		WHERE at.agency_id = $1 AND at.application_id = $2
		ORDER BY at.position, t.last_name`

	rows, err := s.db.Query(ctx, query, agencyID, applicationID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	tourists := make([]ApplicationTourist, 0, 4)
	for rows.Next() {
		var link ApplicationTourist
		if err := rows.Scan(&link.TouristID, &link.FullName, &link.Role, &link.Position); err != nil {
			return nil, mapError(err)
		}
		tourists = append(tourists, link)
	}

	return tourists, mapError(rows.Err())
}

// SetApplicationTourists заменяет список путешественников заявки.
func (s *Store) SetApplicationTourists(ctx context.Context, agencyID, id string, actor Actor, touristIDs []string) ([]ApplicationTourist, error) {
	var tourists []ApplicationTourist
	err := s.inTx(ctx, func(tx *Store) error {
		application, err := tx.Application(ctx, agencyID, id)
		if err != nil {
			return err
		}

		if err := tx.replaceApplicationTourists(ctx, agencyID, id, application.CustomerTouristID, touristIDs); err != nil {
			return err
		}

		tourists, err = tx.applicationTourists(ctx, agencyID, id)
		if err != nil {
			return err
		}

		before := make([]string, 0, len(application.Tourists))
		for _, link := range application.Tourists {
			before = append(before, link.TouristID)
		}
		after := make([]string, 0, len(tourists))
		for _, link := range tourists {
			after = append(after, link.TouristID)
		}
		if slices.Equal(before, after) {
			return nil
		}

		return tx.recordChange(ctx, agencyID, actor, changeRecord{
			EntityType: EntityApplication,
			EntityID:   id,
			Action:     ActionUpdate,
			Changes:    map[string]Change{"tourists": {From: len(before), To: len(after)}},
			Summary:    "Состав туристов заявки № " + application.Number,
		})
	})

	return tourists, err
}

// replaceApplicationTourists перезаписывает таблицу связей. Заказчик
// включается всегда, потому что заказчик, не входящий в список поездки, —
// это ошибка ввода данных, а не допустимое состояние.
func (s *Store) replaceApplicationTourists(ctx context.Context, agencyID, applicationID, customerID string, touristIDs []string) error {
	ordered := make([]string, 0, len(touristIDs)+1)
	ordered = append(ordered, customerID)
	for _, id := range touristIDs {
		if id != customerID && !slices.Contains(ordered, id) {
			ordered = append(ordered, id)
		}
	}

	if _, err := s.db.Exec(ctx,
		`DELETE FROM application_tourists WHERE agency_id = $1 AND application_id = $2`,
		agencyID, applicationID); err != nil {
		return mapError(err)
	}

	const insert = `
		INSERT INTO application_tourists (application_id, tourist_id, agency_id, role, position)
		VALUES ($1, $2, $3, $4, $5)`

	for position, touristID := range ordered {
		role := "tourist"
		if touristID == customerID {
			role = "customer"
		}
		if _, err := s.db.Exec(ctx, insert, applicationID, touristID, agencyID, role, position); err != nil {
			return mapError(err)
		}
	}

	return nil
}

func (s *Store) ensureCustomerLinked(ctx context.Context, agencyID, applicationID, customerID string) error {
	const query = `
		INSERT INTO application_tourists (application_id, tourist_id, agency_id, role, position)
		VALUES ($1, $2, $3, 'customer', 0)
		ON CONFLICT (application_id, tourist_id) DO UPDATE SET role = 'customer'`

	_, err := s.db.Exec(ctx, query, applicationID, customerID, agencyID)

	return mapError(err)
}

// Список --------------------------------------------------------------------

// ApplicationFilter управляет списком и поиском заявок.
type ApplicationFilter struct {
	Search     string
	Statuses   []string
	TouristID  string
	OperatorID string
	ChannelID  string
	ManagerID  string
	DepartFrom *Date
	DepartTo   *Date
	Sort       string
	Limit      int
	Offset     int
}

var applicationSortColumns = map[string]string{
	"number":      "number ASC",
	"-number":     "number DESC",
	"createdAt":   "created_at ASC",
	"-createdAt":  "created_at DESC",
	"updatedAt":   "updated_at ASC",
	"-updatedAt":  "updated_at DESC",
	"departDate":  "depart_date ASC NULLS LAST",
	"-departDate": "depart_date DESC NULLS LAST",
}

// ListApplications возвращает заявки, подходящие под фильтр.
func (s *Store) ListApplications(ctx context.Context, agencyID string, filter ApplicationFilter) ([]Application, int, error) {
	order, ok := applicationSortColumns[filter.Sort]
	if !ok {
		order = applicationSortColumns["-createdAt"]
	}

	query := `
		SELECT ` + applicationColumns + `, count(*) OVER () AS total
		FROM applications a
		WHERE a.agency_id = $1
		  AND a.archived_at IS NULL
		  AND ($2 = '' OR a.search_text LIKE '%' || $2 || '%')
		  AND (cardinality($3::text[]) = 0 OR a.status = ANY($3))
		  AND ($4 = '' OR a.tour_operator_id = $4::uuid)
		  AND ($5 = '' OR a.acquisition_channel_id = $5::uuid)
		  AND ($6 = '' OR a.manager_user_id = $6::uuid)
		  AND ($7::date IS NULL OR a.depart_date >= $7)
		  AND ($8::date IS NULL OR a.depart_date <= $8)
		  AND ($9 = '' OR EXISTS (
		        SELECT 1 FROM application_tourists at
		        WHERE at.application_id = a.id AND at.tourist_id = $9::uuid))
		ORDER BY ` + order + `
		LIMIT $10 OFFSET $11`

	statuses := filter.Statuses
	if statuses == nil {
		statuses = []string{}
	}

	rows, err := s.db.Query(ctx, query, agencyID, searchTerm(filter.Search), statuses,
		filter.OperatorID, filter.ChannelID, filter.ManagerID,
		filter.DepartFrom, filter.DepartTo, filter.TouristID, filter.Limit, filter.Offset)
	if err != nil {
		return nil, 0, mapError(err)
	}
	defer rows.Close()

	applications := make([]Application, 0, filter.Limit)
	total := 0
	for rows.Next() {
		var application Application
		targets := append(applicationScanTargets(&application), &total)
		if err := rows.Scan(targets...); err != nil {
			return nil, 0, mapError(err)
		}
		applications = append(applications, application)
	}

	return applications, total, mapError(rows.Err())
}

// Сроки -----------------------------------------------------------------------

// Deadline — одно обязательство с датой, привязанное к заявке.
type Deadline struct {
	ID            string     `json:"id"`
	ApplicationID string     `json:"applicationId"`
	Kind          string     `json:"kind"`
	DueDate       Date       `json:"dueDate"`
	Note          *string    `json:"note,omitempty"`
	CompletedAt   *time.Time `json:"completedAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

// DeadlineKinds перечисляет обязательства, которые отслеживает первый этап.
var DeadlineKinds = []string{"booking", "payment", "documents", "visa", "departure", "return", "other"}

const deadlineColumns = `id, application_id, kind, due_date, note, completed_at, created_at, updated_at`

func scanDeadline(row interface{ Scan(...any) error }) (Deadline, error) {
	var deadline Deadline
	err := row.Scan(&deadline.ID, &deadline.ApplicationID, &deadline.Kind, &deadline.DueDate,
		&deadline.Note, &deadline.CompletedAt, &deadline.CreatedAt, &deadline.UpdatedAt)
	if err != nil {
		return Deadline{}, mapError(err)
	}

	return deadline, nil
}

// CreateDeadline привязывает к заявке обязательство с датой.
func (s *Store) CreateDeadline(ctx context.Context, agencyID, applicationID, kind string, dueDate Date, note *string) (Deadline, error) {
	const query = `
		INSERT INTO application_deadlines (application_id, agency_id, kind, due_date, note)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING ` + deadlineColumns

	return scanDeadline(s.db.QueryRow(ctx, query, applicationID, agencyID, kind, dueDate, note))
}

// ListDeadlines возвращает обязательства одной заявки.
func (s *Store) ListDeadlines(ctx context.Context, agencyID, applicationID string) ([]Deadline, error) {
	const query = `
		SELECT ` + deadlineColumns + `
		FROM application_deadlines
		WHERE agency_id = $1 AND application_id = $2
		ORDER BY due_date`

	rows, err := s.db.Query(ctx, query, agencyID, applicationID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	deadlines := make([]Deadline, 0, 4)
	for rows.Next() {
		deadline, err := scanDeadline(rows)
		if err != nil {
			return nil, err
		}
		deadlines = append(deadlines, deadline)
	}

	return deadlines, mapError(rows.Err())
}

// UpdateDeadline меняет дату, заметку или отметку о выполнении срока.
func (s *Store) UpdateDeadline(ctx context.Context, agencyID, id string, dueDate *Date, note *string, completed *bool) (Deadline, error) {
	const query = `
		UPDATE application_deadlines
		SET due_date = coalesce($3, due_date),
		    note     = coalesce($4, note),
		    completed_at = CASE
		        WHEN $5::boolean IS NULL THEN completed_at
		        WHEN $5 THEN coalesce(completed_at, now())
		        ELSE NULL
		    END
		WHERE agency_id = $1 AND id = $2
		RETURNING ` + deadlineColumns

	return scanDeadline(s.db.QueryRow(ctx, query, agencyID, id, dueDate, note, completed))
}

// DeleteDeadline удаляет обязательство.
func (s *Store) DeleteDeadline(ctx context.Context, agencyID, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM application_deadlines WHERE agency_id = $1 AND id = $2`, agencyID, id)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	return nil
}

// UpcomingDeadline — срок, сообщаемый вместе с заявкой, которой он принадлежит.
type UpcomingDeadline struct {
	Deadline
	ApplicationNumber string `json:"applicationNumber"`
	DaysLeft          int    `json:"daysLeft"`
}

// UpcomingDeadlines перечисляет невыполненные обязательства со сроком до указанной даты включительно.
func (s *Store) UpcomingDeadlines(ctx context.Context, agencyID string, before Date, today Date, limit int) ([]UpcomingDeadline, error) {
	const query = `
		SELECT d.id, d.application_id, d.kind, d.due_date, d.note, d.completed_at,
		       d.created_at, d.updated_at, a.number
		FROM application_deadlines d
		JOIN applications a ON a.id = d.application_id
		WHERE d.agency_id = $1
		  AND d.completed_at IS NULL
		  AND a.archived_at IS NULL
		  AND a.status NOT IN ('completed', 'cancelled')
		  AND d.due_date <= $2
		ORDER BY d.due_date
		LIMIT $3`

	rows, err := s.db.Query(ctx, query, agencyID, before, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	deadlines := make([]UpcomingDeadline, 0, limit)
	for rows.Next() {
		var item UpcomingDeadline
		if err := rows.Scan(&item.ID, &item.ApplicationID, &item.Kind, &item.DueDate, &item.Note,
			&item.CompletedAt, &item.CreatedAt, &item.UpdatedAt, &item.ApplicationNumber); err != nil {
			return nil, mapError(err)
		}
		item.DaysLeft = item.DueDate.DaysUntil(today)
		deadlines = append(deadlines, item)
	}

	return deadlines, mapError(rows.Err())
}
