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

// CanTransition сообщает, можно ли выбрать целевую стадию. Менеджер может
// исправить ошибочно выбранный статус и перейти на любой этап жизненного цикла.
func CanTransition(from, to string) bool {
	return from != to && slices.Contains(AllStatuses, from) && slices.Contains(AllStatuses, to)
}

// AllowedTransitions перечисляет стадии, достижимые из текущей.
func AllowedTransitions(from string) []string {
	if !slices.Contains(AllStatuses, from) {
		return []string{}
	}

	allowed := make([]string, 0, len(AllStatuses)-1)
	for _, status := range AllStatuses {
		if status != from {
			allowed = append(allowed, status)
		}
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

	CustomerTouristID     string  `json:"customerTouristId"`
	ManagerUserID         *string `json:"managerUserId,omitempty"`
	PayerID               *string `json:"payerId,omitempty"`
	TourOperatorID        *string `json:"tourOperatorId,omitempty"`
	TourOperatorReference *string `json:"tourOperatorReference,omitempty"`
	AcquisitionChannelID  *string `json:"acquisitionChannelId,omitempty"`

	// Country — название страны, всегда производное от CountryCode
	// (см. Normalize/CreateApplication/UpdateApplication); отдельно не пишется.
	Country     *string `json:"country,omitempty"`
	CountryCode *string `json:"countryCode,omitempty"`
	City        *string `json:"city,omitempty"`
	Resort      *string `json:"resort,omitempty"`
	Hotel       *string `json:"hotel,omitempty"`
	DepartDate  *Date   `json:"departDate,omitempty"`
	ReturnDate  *Date   `json:"returnDate,omitempty"`
	Adults      *int    `json:"adults,omitempty"`
	Children    *int    `json:"children,omitempty"`
	PriceTotal  *string `json:"priceTotal,omitempty"`
	Currency    string  `json:"currency"`

	Note         *string `json:"note,omitempty"`
	CancelReason *string `json:"cancelReason,omitempty"`

	Version   int       `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	// Tourists заполняется эндпоинтом деталей, а не запросами списка.
	Tourists []ApplicationTourist `json:"tourists,omitempty"`

	// TouristCount и Finance — наоборот, заполняются только ListApplications:
	// в Create/Update/Get/ChangeStatus JOIN'ов, из которых они считаются, нет.
	TouristCount int                        `json:"touristCount,omitempty"`
	Finance      *ApplicationFinanceSummary `json:"finance,omitempty"`
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
	CustomerTouristID     string  `json:"customerTouristId" history:"customerTouristId"`
	ManagerUserID         *string `json:"managerUserId" history:"managerUserId"`
	PayerID               *string `json:"payerId" history:"payerId"`
	TourOperatorID        *string `json:"tourOperatorId" history:"tourOperatorId"`
	TourOperatorReference *string `json:"tourOperatorReference" history:"tourOperatorReference"`
	AcquisitionChannelID  *string `json:"acquisitionChannelId" history:"acquisitionChannelId"`

	CountryCode *string `json:"countryCode" history:"countryCode"`
	City        *string `json:"city" history:"city"`
	Resort      *string `json:"resort" history:"resort"`
	Hotel       *string `json:"hotel" history:"hotel"`
	DepartDate  *Date   `json:"departDate" history:"departDate"`
	ReturnDate  *Date   `json:"returnDate" history:"returnDate"`
	Adults      *int    `json:"adults" history:"adults"`
	Children    *int    `json:"children" history:"children"`
	PriceTotal  *string `json:"priceTotal" history:"priceTotal"`
	Currency    string  `json:"currency" history:"currency"`

	Note *string `json:"note" history:"note"`
}

// Normalize обрезает пробелы в тексте и заполняет значения по умолчанию, ожидаемые схемой.
func (in *ApplicationInput) Normalize() {
	trimPtr(&in.City)
	trimPtr(&in.Resort)
	trimPtr(&in.Hotel)
	trimPtr(&in.Note)
	trimPtr(&in.PriceTotal)
	trimPtr(&in.TourOperatorReference)

	if in.CountryCode != nil {
		code := strings.ToUpper(strings.TrimSpace(*in.CountryCode))
		in.CountryCode = &code
	}

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

	v.pattern("countryCode", in.CountryCode, countryCodeRe, "код страны — два заглавных латинских символа (ISO 3166-1 alpha-2)")
	v.optional("city", in.City, 100)
	v.optional("resort", in.Resort, 100)
	v.optional("hotel", in.Hotel, 200)
	v.optional("note", in.Note, 4000)
	v.optional("tourOperatorReference", in.TourOperatorReference, 100)

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
	customer_tourist_id, manager_user_id, payer_id, tour_operator_id, tour_operator_reference, acquisition_channel_id,
	country, country_code, city, resort, hotel, depart_date, return_date, adults, children,
	price_total::text, currency, note, cancel_reason, version, created_at, updated_at`

func applicationScanTargets(a *Application) []any {
	return []any{
		&a.ID, &a.Number, &a.Status, &a.StatusChangedAt,
		&a.CustomerTouristID, &a.ManagerUserID, &a.PayerID, &a.TourOperatorID, &a.TourOperatorReference, &a.AcquisitionChannelID,
		&a.Country, &a.CountryCode, &a.City, &a.Resort, &a.Hotel, &a.DepartDate, &a.ReturnDate, &a.Adults, &a.Children,
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
		input.CustomerTouristID, input.ManagerUserID, input.PayerID, input.TourOperatorID, input.TourOperatorReference,
		input.AcquisitionChannelID, input.CountryCode, input.City, input.Resort, input.Hotel,
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

		// country не приходит от клиента: он всегда вычисляется из country_code
		// через справочник countries, чтобы полнотекстовый поиск (search_text)
		// и отчёты по направлениям видели читаемое название, а не код.
		const query = `
			INSERT INTO applications (
			    agency_id, number, customer_tourist_id, manager_user_id, payer_id, tour_operator_id, tour_operator_reference,
			    acquisition_channel_id, country, country_code, city, resort, hotel, depart_date, return_date,
			    adults, children, price_total, currency, note, created_by, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8,
			        (SELECT name FROM countries WHERE code = $9), $9,
			        $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $20)
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

		// country не трогаем напрямую: если country_code не передан, старое
		// название сохраняется как есть (см. комментарий в CreateApplication) —
		// иначе непривязанный текст старых заявок стирался бы любым чужим PATCH.
		const query = `
			UPDATE applications SET
			    customer_tourist_id = $3, manager_user_id = $4, payer_id = $5, tour_operator_id = $6, tour_operator_reference = $7,
			    acquisition_channel_id = $8,
			    country = CASE WHEN $9::text IS NULL THEN country ELSE (SELECT name FROM countries WHERE code = $9) END,
			    country_code = $9,
			    city = $10, resort = $11, hotel = $12,
			    depart_date = $13, return_date = $14, adults = $15, children = $16,
			    price_total = $17, currency = $18, note = $19, updated_by = $20, version = version + 1
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
		CustomerTouristID:     a.CustomerTouristID,
		ManagerUserID:         a.ManagerUserID,
		PayerID:               a.PayerID,
		TourOperatorID:        a.TourOperatorID,
		TourOperatorReference: a.TourOperatorReference,
		AcquisitionChannelID:  a.AcquisitionChannelID,
		CountryCode:           a.CountryCode,
		City:                  a.City,
		Resort:                a.Resort,
		Hotel:                 a.Hotel,
		DepartDate:            a.DepartDate,
		ReturnDate:            a.ReturnDate,
		Adults:                a.Adults,
		Children:              a.Children,
		PriceTotal:            a.PriceTotal,
		Currency:              a.Currency,
		Note:                  a.Note,
	}
}

// ApplicationAsInput представляет сохранённую заявку в форме, в которую сливается PATCH.
func ApplicationAsInput(a Application) ApplicationInput { return applicationInput(a) }

// ChangeStatus устанавливает выбранную стадию жизненного цикла.
func (s *Store) ChangeStatus(ctx context.Context, agencyID, id string, actor Actor, status string, reason *string) (Application, error) {
	if status != StatusCancelled {
		// Причина отмены имеет смысл только для cancelled — не полагаемся на
		// то, что клиент не пришлёт её вместе с остальными статусами.
		reason = nil
	}

	var updated Application
	err := s.inTx(ctx, func(tx *Store) error {
		before, err := tx.Application(ctx, agencyID, id)
		if err != nil {
			return err
		}

		if before.Status != status && !CanTransition(before.Status, status) {
			return &ValidationError{Fields: map[string]string{
				"status": fmt.Sprintf("из статуса %q нельзя перейти в %q", before.Status, status),
			}}
		}

		if status == StatusCancelled && (reason == nil || strings.TrimSpace(*reason) == "") {
			return &ValidationError{Fields: map[string]string{
				"cancelReason": "укажите причину отмены",
			}}
		}

		if before.Status == status && equalValues(before.CancelReason, reason) {
			updated = before

			return nil
		}

		statusChanged := before.Status != status

		const query = `
			UPDATE applications
			SET status = $3,
			    status_changed_at = CASE WHEN $6 THEN now() ELSE status_changed_at END,
			    cancel_reason = $4,
			    updated_by = $5, version = version + 1
			WHERE agency_id = $1 AND id = $2 AND archived_at IS NULL
			RETURNING ` + applicationColumns

		application, err := scanApplication(tx.db.QueryRow(ctx, query,
			agencyID, id, status, reason, nullString(actor.UserID), statusChanged))
		if err != nil {
			return err
		}
		updated = application

		changes := map[string]Change{}
		if statusChanged {
			changes["status"] = Change{From: before.Status, To: status}
		}
		if !equalValues(before.CancelReason, reason) {
			changes["cancelReason"] = Change{From: derefValue(before.CancelReason), To: derefValue(reason)}
		}

		return tx.recordChange(ctx, agencyID, actor, changeRecord{
			EntityType: EntityApplication,
			EntityID:   id,
			Action:     ActionStatusChange,
			Changes:    changes,
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
	Search        string
	Statuses      []string
	TouristID     string
	OperatorID    string
	ChannelID     string
	ManagerID     string
	DepartFrom    *Date
	DepartTo      *Date
	PaymentStatus string
	Sort          string
	// Today — «сегодня» в часовом поясе агентства, нужно только сортировке
	// upcomingDepartDate. Считается на уровне HTTP (a.today()), а не через
	// now()/CURRENT_DATE в SQL — как и везде в этой кодовой базе.
	Today  Date
	Limit  int
	Offset int
}

var applicationSortColumns = map[string]string{
	// number — всегда простая строка из nextApplicationNumber (никогда не
	// вводится и не редактируется пользователем), поэтому ::bigint безопасен
	// и даёт числовой, а не лексикографический порядок (1, 2, 10, а не 1, 10, 2).
	"number":      "number::bigint ASC",
	"-number":     "number::bigint DESC",
	"createdAt":   "created_at ASC",
	"-createdAt":  "created_at DESC",
	"updatedAt":   "updated_at ASC",
	"-updatedAt":  "updated_at DESC",
	"departDate":  "depart_date ASC NULLS LAST, a.id ASC",
	"-departDate": "depart_date DESC NULLS LAST, a.id ASC",
}

// ListApplications возвращает заявки, подходящие под фильтр, вместе с
// touristCount и денежной сводкой на строку — одним запросом, без N+1 к
// application_tourists/application_balances на каждую заявку.
func (s *Store) ListApplications(ctx context.Context, agencyID string, filter ApplicationFilter) ([]Application, int, error) {
	statuses := filter.Statuses
	if statuses == nil {
		statuses = []string{}
	}

	args := []any{
		agencyID, searchTerm(filter.Search), statuses,
		filter.OperatorID, filter.ChannelID, filter.ManagerID,
		filter.DepartFrom, filter.DepartTo, filter.TouristID, filter.PaymentStatus,
	}

	var order string
	if filter.Sort == "upcomingDepartDate" {
		// Сегодня и будущее — по возрастанию (ближайший вылет первый),
		// прошлое — по убыванию (свежее прошлое первое), без даты — в конце.
		// a.id — стабильный вторичный порядок для пагинации.
		today := len(args) + 1
		args = append(args, filter.Today)
		order = fmt.Sprintf(`
			CASE WHEN a.depart_date IS NULL THEN 2
			     WHEN a.depart_date >= $%d THEN 0
			     ELSE 1 END,
			CASE WHEN a.depart_date >= $%d THEN a.depart_date END ASC,
			CASE WHEN a.depart_date < $%d THEN a.depart_date END DESC,
			a.id`, today, today, today)
	} else {
		var ok bool
		order, ok = applicationSortColumns[filter.Sort]
		if !ok {
			order = applicationSortColumns["-createdAt"]
		}
	}

	limitParam, offsetParam := len(args)+1, len(args)+2
	args = append(args, filter.Limit, filter.Offset)

	query := `
		SELECT ` + applicationColumns + `,
		    (SELECT count(*) FROM application_tourists at2 WHERE at2.application_id = a.id)::int AS tourist_count,
		    coalesce(bal.transferred, 0)::text AS transferred,
		    coalesce(bal.received - bal.refunded, 0)::text AS net_received,
		    (coalesce(bal.received - bal.refunded, 0) - coalesce(bal.transferred, 0))::text AS agency_income,
		    count(*) OVER () AS total
		FROM applications a
		LEFT JOIN (
		    SELECT application_id, transferred, received, refunded, bonus_income FROM application_balances
		) bal ON bal.application_id = a.id
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
		  AND ($10 = '' OR (
		        SELECT CASE
		            WHEN b.price_total IS NULL THEN ''
		            WHEN b.received - b.refunded <= 0 THEN 'unpaid'
		            WHEN b.received - b.refunded < b.price_total THEN 'partial'
		            WHEN b.received - b.refunded = b.price_total THEN 'paid'
		            ELSE 'overpaid'
		        END
		        FROM application_balances b WHERE b.application_id = a.id) = $10)
		ORDER BY ` + order + fmt.Sprintf(`
		LIMIT $%d OFFSET $%d`, limitParam, offsetParam)

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, mapError(err)
	}
	defer rows.Close()

	applications := make([]Application, 0, filter.Limit)
	total := 0
	for rows.Next() {
		var application Application
		var touristCount int
		var transferred, netReceived, agencyIncome string
		targets := append(applicationScanTargets(&application),
			&touristCount, &transferred, &netReceived, &agencyIncome, &total)
		if err := rows.Scan(targets...); err != nil {
			return nil, 0, mapError(err)
		}
		application.TouristCount = touristCount
		application.Finance = &ApplicationFinanceSummary{
			Transferred: transferred, NetReceived: netReceived, AgencyIncome: agencyIncome,
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
