package store

import (
	"context"
	"strings"
	"time"
)

// Tourist — карточка клиента: кто этот человек, по каким документам он
// путешествует и как он пришёл в агентство.
type Tourist struct {
	ID         string  `json:"id"`
	LastName   string  `json:"lastName"`
	FirstName  string  `json:"firstName"`
	MiddleName *string `json:"middleName,omitempty"`
	BirthDate  *Date   `json:"birthDate,omitempty"`
	Gender     *string `json:"gender,omitempty"`
	Phone      *string `json:"phone,omitempty"`
	Email      *string `json:"email,omitempty"`

	PassportSeries       *string `json:"passportSeries,omitempty"`
	PassportNumber       *string `json:"passportNumber,omitempty"`
	PassportIssuedBy     *string `json:"passportIssuedBy,omitempty"`
	PassportIssueDate    *Date   `json:"passportIssueDate,omitempty"`
	PassportDivisionCode *string `json:"passportDivisionCode,omitempty"`

	IntlPassportNumber     *string `json:"intlPassportNumber,omitempty"`
	IntlPassportLastName   *string `json:"intlPassportLastName,omitempty"`
	IntlPassportFirstName  *string `json:"intlPassportFirstName,omitempty"`
	IntlPassportAuthority  *string `json:"intlPassportAuthority,omitempty"`
	IntlPassportIssueDate  *Date   `json:"intlPassportIssueDate,omitempty"`
	IntlPassportExpiryDate *Date   `json:"intlPassportExpiryDate,omitempty"`

	AcquisitionChannelID *string `json:"acquisitionChannelId,omitempty"`
	ReferrerPartnerID    *string `json:"referrerPartnerId,omitempty"`
	ReferrerTouristID    *string `json:"referrerTouristId,omitempty"`

	Note      *string   `json:"note,omitempty"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// FullName формирует заголовок карточки, используемый в кратком описании журнала.
func (t Tourist) FullName() string {
	parts := []string{t.LastName, t.FirstName}
	if t.MiddleName != nil && *t.MiddleName != "" {
		parts = append(parts, *t.MiddleName)
	}

	return strings.Join(parts, " ")
}

// Masked возвращает копию, безопасную для ответов в списках. Полные номера
// документов возвращаются только эндпоинтом одного туриста, поэтому
// просматриваемый список не разбрасывает паспортные данные по экранам и
// кэшам.
func (t Tourist) Masked() Tourist {
	masked := t
	masked.PassportSeries = maskTail(t.PassportSeries, 0)
	masked.PassportNumber = maskTail(t.PassportNumber, 2)
	masked.PassportDivisionCode = nil
	masked.PassportIssuedBy = nil
	masked.IntlPassportNumber = maskTail(t.IntlPassportNumber, 3)

	return masked
}

func maskTail(value *string, keep int) *string {
	if value == nil {
		return nil
	}

	runes := []rune(*value)
	if len(runes) <= keep {
		return value
	}

	masked := strings.Repeat("*", len(runes)-keep) + string(runes[len(runes)-keep:])

	return &masked
}

// TouristInput — изменяемая часть карточки туриста.
//
// Теги history управляют журналом изменений. Номера документов, дата
// рождения и контакты помечены как чувствительные: журнал тогда фиксирует
// сам факт их редактирования, не копируя значения во вторую таблицу.
type TouristInput struct {
	LastName   string  `json:"lastName" history:"lastName"`
	FirstName  string  `json:"firstName" history:"firstName"`
	MiddleName *string `json:"middleName" history:"middleName"`
	BirthDate  *Date   `json:"birthDate" history:"birthDate,sensitive"`
	Gender     *string `json:"gender" history:"gender"`
	Phone      *string `json:"phone" history:"phone,sensitive"`
	Email      *string `json:"email" history:"email,sensitive"`

	PassportSeries       *string `json:"passportSeries" history:"passportSeries,sensitive"`
	PassportNumber       *string `json:"passportNumber" history:"passportNumber,sensitive"`
	PassportIssuedBy     *string `json:"passportIssuedBy" history:"passportIssuedBy,sensitive"`
	PassportIssueDate    *Date   `json:"passportIssueDate" history:"passportIssueDate"`
	PassportDivisionCode *string `json:"passportDivisionCode" history:"passportDivisionCode,sensitive"`

	IntlPassportNumber     *string `json:"intlPassportNumber" history:"intlPassportNumber,sensitive"`
	IntlPassportLastName   *string `json:"intlPassportLastName" history:"intlPassportLastName"`
	IntlPassportFirstName  *string `json:"intlPassportFirstName" history:"intlPassportFirstName"`
	IntlPassportAuthority  *string `json:"intlPassportAuthority" history:"intlPassportAuthority"`
	IntlPassportIssueDate  *Date   `json:"intlPassportIssueDate" history:"intlPassportIssueDate"`
	IntlPassportExpiryDate *Date   `json:"intlPassportExpiryDate" history:"intlPassportExpiryDate"`

	AcquisitionChannelID *string `json:"acquisitionChannelId" history:"acquisitionChannelId"`
	ReferrerPartnerID    *string `json:"referrerPartnerId" history:"referrerPartnerId"`
	ReferrerTouristID    *string `json:"referrerTouristId" history:"referrerTouristId"`

	Note *string `json:"note" history:"note"`
}

// Normalize обрезает пробелы и приводит к каноническому виду значения,
// которые сравниваются на дубли, поэтому один и тот же человек, введённый
// дважды, оба раза выглядит одинаково.
func (in *TouristInput) Normalize() {
	in.LastName = strings.TrimSpace(in.LastName)
	in.FirstName = strings.TrimSpace(in.FirstName)
	trimPtr(&in.MiddleName)
	trimPtr(&in.PassportIssuedBy)
	trimPtr(&in.IntlPassportAuthority)
	trimPtr(&in.Note)

	if in.Phone != nil {
		normalized := NormalizePhone(*in.Phone)
		in.Phone = emptyToNil(normalized)
	}
	if in.Email != nil {
		lowered := strings.ToLower(strings.TrimSpace(*in.Email))
		in.Email = emptyToNil(lowered)
	}
	upperPtr(&in.IntlPassportLastName)
	upperPtr(&in.IntlPassportFirstName)
}

// Validate сообщает обо всех проблемах сразу. Повторяет CHECK-ограничения из
// схемы, поэтому некорректное тело запроса даёт понятный 400, а не 500 из-за
// нарушения ограничения.
func (in TouristInput) Validate(today Date) error {
	v := newValidator()

	v.required("lastName", in.LastName, 100)
	v.required("firstName", in.FirstName, 100)
	v.optional("middleName", in.MiddleName, 100)
	v.optional("note", in.Note, 4000)
	v.optional("passportIssuedBy", in.PassportIssuedBy, 255)
	v.optional("intlPassportAuthority", in.IntlPassportAuthority, 255)

	if in.Gender != nil && *in.Gender != "" {
		v.oneOf("gender", *in.Gender, "male", "female")
	}

	v.phone("phone", in.Phone)
	v.email("email", in.Email)

	if in.BirthDate != nil && !in.BirthDate.IsZero() {
		if in.BirthDate.Year < 1900 {
			v.add("birthDate", "дата рождения раньше 1900 года")
		}
		if today.Before(*in.BirthDate) {
			v.add("birthDate", "дата рождения не может быть в будущем")
		}
	}

	// Внутренний паспорт вводится как единый документ: серия без номера — это
	// наполовину заполненная форма, а не корректная карточка.
	hasSeries := in.PassportSeries != nil && *in.PassportSeries != ""
	hasNumber := in.PassportNumber != nil && *in.PassportNumber != ""
	if hasSeries != hasNumber {
		v.add("passportNumber", "серия и номер паспорта заполняются вместе")
	}
	v.pattern("passportSeries", in.PassportSeries, passportSeriesRe, "серия паспорта — 4 цифры")
	v.pattern("passportNumber", in.PassportNumber, passportNumberRe, "номер паспорта — 6 цифр")
	v.pattern("passportDivisionCode", in.PassportDivisionCode, divisionCodeRe, "код подразделения — в формате 000-000")

	if in.PassportIssueDate != nil && !in.PassportIssueDate.IsZero() && today.Before(*in.PassportIssueDate) {
		v.add("passportIssueDate", "дата выдачи не может быть в будущем")
	}

	v.pattern("intlPassportNumber", in.IntlPassportNumber, intlPassportRe, "номер загранпаспорта — 9 цифр")
	v.pattern("intlPassportLastName", in.IntlPassportLastName, latinNameRe, "фамилия латиницей заглавными буквами")
	v.pattern("intlPassportFirstName", in.IntlPassportFirstName, latinNameRe, "имя латиницей заглавными буквами")

	issue, expiry := in.IntlPassportIssueDate, in.IntlPassportExpiryDate
	if issue != nil && expiry != nil && !issue.IsZero() && !expiry.IsZero() && !issue.Before(*expiry) {
		v.add("intlPassportExpiryDate", "срок действия должен быть позже даты выдачи")
	}

	if in.ReferrerPartnerID != nil && in.ReferrerTouristID != nil {
		v.add("referrerTouristId", "укажите либо партнёра, либо туриста-рекомендателя")
	}

	return v.err()
}

const touristColumns = `id, last_name, first_name, middle_name, birth_date, gender, phone, email,
	passport_series, passport_number, passport_issued_by, passport_issue_date, passport_division_code,
	intl_passport_number, intl_passport_last_name, intl_passport_first_name, intl_passport_authority,
	intl_passport_issue_date, intl_passport_expiry_date,
	acquisition_channel_id, referrer_partner_id, referrer_tourist_id,
	note, version, created_at, updated_at`

func touristScanTargets(t *Tourist) []any {
	return []any{
		&t.ID, &t.LastName, &t.FirstName, &t.MiddleName, &t.BirthDate, &t.Gender, &t.Phone, &t.Email,
		&t.PassportSeries, &t.PassportNumber, &t.PassportIssuedBy, &t.PassportIssueDate, &t.PassportDivisionCode,
		&t.IntlPassportNumber, &t.IntlPassportLastName, &t.IntlPassportFirstName, &t.IntlPassportAuthority,
		&t.IntlPassportIssueDate, &t.IntlPassportExpiryDate,
		&t.AcquisitionChannelID, &t.ReferrerPartnerID, &t.ReferrerTouristID,
		&t.Note, &t.Version, &t.CreatedAt, &t.UpdatedAt,
	}
}

func scanTourist(row interface{ Scan(...any) error }) (Tourist, error) {
	var tourist Tourist
	if err := row.Scan(touristScanTargets(&tourist)...); err != nil {
		return Tourist{}, mapError(err)
	}

	return tourist, nil
}

func touristArgs(input TouristInput) []any {
	return []any{
		input.LastName, input.FirstName, input.MiddleName, input.BirthDate, input.Gender,
		input.Phone, input.Email,
		input.PassportSeries, input.PassportNumber, input.PassportIssuedBy,
		input.PassportIssueDate, input.PassportDivisionCode,
		input.IntlPassportNumber, input.IntlPassportLastName, input.IntlPassportFirstName,
		input.IntlPassportAuthority, input.IntlPassportIssueDate, input.IntlPassportExpiryDate,
		input.AcquisitionChannelID, input.ReferrerPartnerID, input.ReferrerTouristID, input.Note,
	}
}

// CreateTourist добавляет карточку клиента и записывает её в журнал в той же транзакции.
func (s *Store) CreateTourist(ctx context.Context, agencyID string, actor Actor, input TouristInput) (Tourist, error) {
	var created Tourist
	err := s.inTx(ctx, func(tx *Store) error {
		const query = `
			INSERT INTO tourists (
			    agency_id, last_name, first_name, middle_name, birth_date, gender, phone, email,
			    passport_series, passport_number, passport_issued_by, passport_issue_date, passport_division_code,
			    intl_passport_number, intl_passport_last_name, intl_passport_first_name, intl_passport_authority,
			    intl_passport_issue_date, intl_passport_expiry_date,
			    acquisition_channel_id, referrer_partner_id, referrer_tourist_id, note, created_by, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18,
			        $19, $20, $21, $22, $23, $24, $24)
			RETURNING ` + touristColumns

		args := append([]any{agencyID}, touristArgs(input)...)
		args = append(args, nullString(actor.UserID))

		tourist, err := scanTourist(tx.db.QueryRow(ctx, query, args...))
		if err != nil {
			return err
		}
		created = tourist

		return tx.recordChange(ctx, agencyID, actor, changeRecord{
			EntityType: EntityTourist,
			EntityID:   tourist.ID,
			Action:     ActionCreate,
			Changes:    diff(TouristInput{}, input),
			Summary:    tourist.FullName(),
		})
	})

	return created, err
}

// Tourist загружает одну карточку. Карточка, принадлежащая другому
// агентству, сообщается как ErrNotFound — это то, что вызывающей стороне
// позволено узнать.
func (s *Store) Tourist(ctx context.Context, agencyID, id string) (Tourist, error) {
	const query = `
		SELECT ` + touristColumns + `
		FROM tourists
		WHERE agency_id = $1 AND id = $2 AND archived_at IS NULL`

	return scanTourist(s.db.QueryRow(ctx, query, agencyID, id))
}

// UpdateTourist заменяет изменяемые поля карточки.
//
// Если expectedVersion не равен нулю, обновление применяется, только если
// карточку до этого никто не сохранял. Агентства работают с общей клиентской
// базой, поэтому два менеджера, редактирующих одного туриста одновременно, —
// обычное дело.
func (s *Store) UpdateTourist(ctx context.Context, agencyID, id string, actor Actor, input TouristInput, expectedVersion int) (Tourist, error) {
	var updated Tourist
	err := s.inTx(ctx, func(tx *Store) error {
		before, err := tx.Tourist(ctx, agencyID, id)
		if err != nil {
			return err
		}
		if expectedVersion != 0 && before.Version != expectedVersion {
			return ErrVersionConflict
		}

		const query = `
			UPDATE tourists SET
			    last_name = $3, first_name = $4, middle_name = $5, birth_date = $6, gender = $7,
			    phone = $8, email = $9,
			    passport_series = $10, passport_number = $11, passport_issued_by = $12,
			    passport_issue_date = $13, passport_division_code = $14,
			    intl_passport_number = $15, intl_passport_last_name = $16, intl_passport_first_name = $17,
			    intl_passport_authority = $18, intl_passport_issue_date = $19, intl_passport_expiry_date = $20,
			    acquisition_channel_id = $21, referrer_partner_id = $22, referrer_tourist_id = $23,
			    note = $24, updated_by = $25, version = version + 1
			WHERE agency_id = $1 AND id = $2 AND archived_at IS NULL
			RETURNING ` + touristColumns

		args := append([]any{agencyID, id}, touristArgs(input)...)
		args = append(args, nullString(actor.UserID))

		tourist, err := scanTourist(tx.db.QueryRow(ctx, query, args...))
		if err != nil {
			return err
		}
		updated = tourist

		changes := diff(touristInput(before), input)
		if len(changes) == 0 {
			return nil
		}

		return tx.recordChange(ctx, agencyID, actor, changeRecord{
			EntityType: EntityTourist,
			EntityID:   id,
			Action:     ActionUpdate,
			Changes:    changes,
			Summary:    tourist.FullName(),
		})
	})

	return updated, err
}

func touristInput(t Tourist) TouristInput {
	return TouristInput{
		LastName:               t.LastName,
		FirstName:              t.FirstName,
		MiddleName:             t.MiddleName,
		BirthDate:              t.BirthDate,
		Gender:                 t.Gender,
		Phone:                  t.Phone,
		Email:                  t.Email,
		PassportSeries:         t.PassportSeries,
		PassportNumber:         t.PassportNumber,
		PassportIssuedBy:       t.PassportIssuedBy,
		PassportIssueDate:      t.PassportIssueDate,
		PassportDivisionCode:   t.PassportDivisionCode,
		IntlPassportNumber:     t.IntlPassportNumber,
		IntlPassportLastName:   t.IntlPassportLastName,
		IntlPassportFirstName:  t.IntlPassportFirstName,
		IntlPassportAuthority:  t.IntlPassportAuthority,
		IntlPassportIssueDate:  t.IntlPassportIssueDate,
		IntlPassportExpiryDate: t.IntlPassportExpiryDate,
		AcquisitionChannelID:   t.AcquisitionChannelID,
		ReferrerPartnerID:      t.ReferrerPartnerID,
		ReferrerTouristID:      t.ReferrerTouristID,
		Note:                   t.Note,
	}
}

// TouristAsInput представляет сохранённую карточку в форме, в которую сливается PATCH.
func TouristAsInput(t Tourist) TouristInput { return touristInput(t) }

// ArchiveTourist мягко удаляет карточку. Строка остаётся, поэтому заявки, по
// которым человек путешествовал, сохраняют свою историю.
func (s *Store) ArchiveTourist(ctx context.Context, agencyID, id string, actor Actor) error {
	return s.inTx(ctx, func(tx *Store) error {
		const query = `
			UPDATE tourists SET archived_at = now(), updated_by = $3
			WHERE agency_id = $1 AND id = $2 AND archived_at IS NULL
			RETURNING last_name || ' ' || first_name`

		var name string
		if err := tx.db.QueryRow(ctx, query, agencyID, id, nullString(actor.UserID)).Scan(&name); err != nil {
			return mapError(err)
		}

		return tx.recordChange(ctx, agencyID, actor, changeRecord{
			EntityType: EntityTourist,
			EntityID:   id,
			Action:     ActionArchive,
			Summary:    name,
		})
	})
}

// TouristFilter управляет списком и поиском туристов.
type TouristFilter struct {
	Search         string
	ChannelID      string
	PartnerID      string
	ExpiringBefore *Date
	Sort           string
	Limit          int
	Offset         int
}

// touristSortColumns — белый список для ORDER BY. Ключи сортировки из
// запроса никогда не попадают в SQL как текст; попадает только
// сопоставленный им фрагмент.
var touristSortColumns = map[string]string{
	"lastName":   "last_name ASC, first_name ASC",
	"-lastName":  "last_name DESC, first_name DESC",
	"createdAt":  "created_at ASC",
	"-createdAt": "created_at DESC",
	"updatedAt":  "updated_at ASC",
	"-updatedAt": "updated_at DESC",
}

// ListTourists возвращает карточки, подходящие под фильтр; по умолчанию сначала новые.
func (s *Store) ListTourists(ctx context.Context, agencyID string, filter TouristFilter) ([]Tourist, int, error) {
	order, ok := touristSortColumns[filter.Sort]
	if !ok {
		order = touristSortColumns["-createdAt"]
	}

	// Поисковый запрос сопоставляется с хранимым столбцом search_text,
	// покрытым триграммным индексом. escapeLike не даёт символу %, введённому
	// пользователем, превратиться в маску для полного сканирования.
	query := `
		SELECT ` + touristColumns + `, count(*) OVER () AS total
		FROM tourists
		WHERE agency_id = $1
		  AND archived_at IS NULL
		  AND ($2 = '' OR search_text LIKE '%' || $2 || '%' OR email ILIKE '%' || $2 || '%')
		  AND ($3 = '' OR acquisition_channel_id = $3::uuid)
		  AND ($4 = '' OR referrer_partner_id = $4::uuid)
		  AND ($5::date IS NULL OR (intl_passport_expiry_date IS NOT NULL AND intl_passport_expiry_date <= $5))
		ORDER BY ` + order + `
		LIMIT $6 OFFSET $7`

	rows, err := s.db.Query(ctx, query, agencyID, searchTerm(filter.Search), filter.ChannelID,
		filter.PartnerID, filter.ExpiringBefore, filter.Limit, filter.Offset)
	if err != nil {
		return nil, 0, mapError(err)
	}
	defer rows.Close()

	tourists := make([]Tourist, 0, filter.Limit)
	total := 0
	for rows.Next() {
		var tourist Tourist
		targets := append(touristScanTargets(&tourist), &total)
		if err := rows.Scan(targets...); err != nil {
			return nil, 0, mapError(err)
		}
		tourists = append(tourists, tourist)
	}

	return tourists, total, mapError(rows.Err())
}

// searchTerm приводит поисковую строку пользователя к нижнему регистру и
// экранирует её. search_text хранится в нижнем регистре с телефоном,
// сведённым к цифрам, поэтому поиск по "+7 (999) 123" всё равно срабатывает.
func searchTerm(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	lowered := strings.ToLower(trimmed)
	if looksLikePhone(lowered) {
		lowered = strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}

			return -1
		}, lowered)
	}

	return escapeLike(lowered)
}

func looksLikePhone(value string) bool {
	digits := 0
	for _, r := range value {
		if r >= '0' && r <= '9' {
			digits++
		}
	}

	return digits >= 4
}

func escapeLike(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")

	return replacer.Replace(value)
}

// ExpiringDocument — один приближающийся срок по документу.
type ExpiringDocument struct {
	TouristID  string  `json:"touristId"`
	FullName   string  `json:"fullName"`
	Kind       string  `json:"kind"`
	ExpiryDate Date    `json:"expiryDate"`
	DaysLeft   int     `json:"daysLeft"`
	Phone      *string `json:"phone,omitempty"`
}

// ExpiringDocuments перечисляет загранпаспорта, срок которых истекает до
// указанной даты включительно, сначала самые срочные. Уже просроченные
// документы включены — это самые срочные из всех.
func (s *Store) ExpiringDocuments(ctx context.Context, agencyID string, before Date, today Date, limit int) ([]ExpiringDocument, error) {
	const query = `
		SELECT id, last_name || ' ' || first_name, intl_passport_expiry_date, phone
		FROM tourists
		WHERE agency_id = $1
		  AND archived_at IS NULL
		  AND intl_passport_expiry_date IS NOT NULL
		  AND intl_passport_expiry_date <= $2
		ORDER BY intl_passport_expiry_date
		LIMIT $3`

	rows, err := s.db.Query(ctx, query, agencyID, before, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	documents := make([]ExpiringDocument, 0, limit)
	for rows.Next() {
		var doc ExpiringDocument
		if err := rows.Scan(&doc.TouristID, &doc.FullName, &doc.ExpiryDate, &doc.Phone); err != nil {
			return nil, mapError(err)
		}
		doc.Kind = "international_passport"
		doc.DaysLeft = doc.ExpiryDate.DaysUntil(today)
		documents = append(documents, doc)
	}

	return documents, mapError(rows.Err())
}

func trimPtr(value **string) {
	if *value == nil {
		return
	}
	trimmed := strings.TrimSpace(**value)
	*value = emptyToNil(trimmed)
}

func upperPtr(value **string) {
	if *value == nil {
		return
	}
	upper := strings.ToUpper(strings.TrimSpace(**value))
	*value = emptyToNil(upper)
}

func emptyToNil(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}
