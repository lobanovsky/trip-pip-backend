package store

import (
	"context"
	"strings"
	"time"
)

// AcquisitionChannel — то, как клиент нашёл агентство. Хранится как таблица
// на каждое агентство, а не как фиксированный enum, потому что в описании
// продукта перечислены сайт, реклама, Telegram, VK, повторное обращение
// «и другие источники».
type AcquisitionChannel struct {
	ID        string    `json:"id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	IsActive  bool      `json:"isActive"`
	SortOrder int       `json:"sortOrder"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// DefaultChannels заполняют новое агентство, чтобы в первой карточке туриста
// было из чего выбрать.
var DefaultChannels = []AcquisitionChannel{
	{Code: "site", Name: "Сайт", SortOrder: 10},
	{Code: "ads", Name: "Реклама", SortOrder: 20},
	{Code: "telegram", Name: "Telegram", SortOrder: 30},
	{Code: "vk", Name: "VK", SortOrder: 40},
	{Code: "repeat", Name: "Повторное обращение", SortOrder: 50},
	{Code: "referral", Name: "Рекомендация", SortOrder: 60},
	{Code: "other", Name: "Другое", SortOrder: 70},
}

const channelColumns = `id, code, name, is_active, sort_order, created_at, updated_at`

func scanChannel(row interface{ Scan(...any) error }) (AcquisitionChannel, error) {
	var channel AcquisitionChannel
	err := row.Scan(&channel.ID, &channel.Code, &channel.Name, &channel.IsActive,
		&channel.SortOrder, &channel.CreatedAt, &channel.UpdatedAt)
	if err != nil {
		return AcquisitionChannel{}, mapError(err)
	}

	return channel, nil
}

// CreateChannel добавляет агентству канал привлечения.
func (s *Store) CreateChannel(ctx context.Context, agencyID, code, name string, sortOrder int) (AcquisitionChannel, error) {
	const query = `
		INSERT INTO acquisition_channels (agency_id, code, name, sort_order)
		VALUES ($1, $2, $3, $4)
		RETURNING ` + channelColumns

	return scanChannel(s.db.QueryRow(ctx, query, agencyID, code, name, sortOrder))
}

// ListChannels возвращает каналы агентства в порядке отображения.
func (s *Store) ListChannels(ctx context.Context, agencyID string, activeOnly bool) ([]AcquisitionChannel, error) {
	const query = `
		SELECT ` + channelColumns + `
		FROM acquisition_channels
		WHERE agency_id = $1 AND ($2 = false OR is_active)
		ORDER BY sort_order, name`

	rows, err := s.db.Query(ctx, query, agencyID, activeOnly)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	channels := make([]AcquisitionChannel, 0, len(DefaultChannels))
	for rows.Next() {
		channel, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		channels = append(channels, channel)
	}

	return channels, mapError(rows.Err())
}

// UpdateChannel переименовывает канал или меняет его позицию.
func (s *Store) UpdateChannel(ctx context.Context, agencyID, id string, name *string, isActive *bool, sortOrder *int) (AcquisitionChannel, error) {
	const query = `
		UPDATE acquisition_channels
		SET name       = coalesce($3, name),
		    is_active  = coalesce($4, is_active),
		    sort_order = coalesce($5, sort_order)
		WHERE agency_id = $1 AND id = $2
		RETURNING ` + channelColumns

	return scanChannel(s.db.QueryRow(ctx, query, agencyID, id, name, isActive, sortOrder))
}

// DeleteChannel удаляет канал. Туристы и заявки, ссылающиеся на него,
// продолжают работать: внешние ключи объявлены как ON DELETE SET NULL.
func (s *Store) DeleteChannel(ctx context.Context, agencyID, id string) error {
	const query = `DELETE FROM acquisition_channels WHERE agency_id = $1 AND id = $2`

	tag, err := s.db.Exec(ctx, query, agencyID, id)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	return nil
}

// Partner — человек или компания, которые приводят клиентов агентству.
type Partner struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	Phone     *string   `json:"phone,omitempty"`
	Email     *string   `json:"email,omitempty"`
	Note      *string   `json:"note,omitempty"`
	IsActive  bool      `json:"isActive"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// PartnerInput — изменяемая часть партнёра.
type PartnerInput struct {
	Kind  string  `json:"kind" history:"kind"`
	Name  string  `json:"name" history:"name"`
	Phone *string `json:"phone" history:"phone"`
	Email *string `json:"email" history:"email"`
	Note  *string `json:"note" history:"note"`
}

// Normalize обрезает пробелы в текстовых полях.
func (in *PartnerInput) Normalize() {
	in.Name = strings.TrimSpace(in.Name)
	if in.Kind == "" {
		in.Kind = "person"
	}
	if in.Phone != nil {
		in.Phone = emptyToNil(NormalizePhone(*in.Phone))
	}
	if in.Email != nil {
		in.Email = emptyToNil(strings.ToLower(strings.TrimSpace(*in.Email)))
	}
	trimPtr(&in.Note)
}

// Validate сообщает обо всех проблемах сразу.
func (in PartnerInput) Validate() error {
	v := newValidator()
	v.required("name", in.Name, 200)
	v.oneOf("kind", in.Kind, "person", "company")
	v.phone("phone", in.Phone)
	v.email("email", in.Email)
	v.optional("note", in.Note, 4000)

	return v.err()
}

const partnerColumns = `id, kind, name, phone, email, note, is_active, created_at, updated_at`

func scanPartner(row interface{ Scan(...any) error }) (Partner, error) {
	var partner Partner
	err := row.Scan(&partner.ID, &partner.Kind, &partner.Name, &partner.Phone, &partner.Email,
		&partner.Note, &partner.IsActive, &partner.CreatedAt, &partner.UpdatedAt)
	if err != nil {
		return Partner{}, mapError(err)
	}

	return partner, nil
}

// CreatePartner регистрирует партнёра-рекомендателя.
func (s *Store) CreatePartner(ctx context.Context, agencyID string, actor Actor, input PartnerInput) (Partner, error) {
	var created Partner
	err := s.inTx(ctx, func(tx *Store) error {
		const query = `
			INSERT INTO partners (agency_id, kind, name, phone, email, note)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING ` + partnerColumns

		partner, err := scanPartner(tx.db.QueryRow(ctx, query,
			agencyID, input.Kind, input.Name, input.Phone, input.Email, input.Note))
		if err != nil {
			return err
		}
		created = partner

		return tx.recordChange(ctx, agencyID, actor, changeRecord{
			EntityType: EntityPartner,
			EntityID:   partner.ID,
			Action:     ActionCreate,
			Changes:    diff(PartnerInput{}, input),
			Summary:    partner.Name,
		})
	})

	return created, err
}

// Partner загружает одного партнёра внутри агентства.
func (s *Store) Partner(ctx context.Context, agencyID, id string) (Partner, error) {
	const query = `SELECT ` + partnerColumns + ` FROM partners WHERE agency_id = $1 AND id = $2 AND archived_at IS NULL`

	return scanPartner(s.db.QueryRow(ctx, query, agencyID, id))
}

// ListPartners возвращает партнёров, подходящих под необязательный поиск по имени.
func (s *Store) ListPartners(ctx context.Context, agencyID, search string, limit, offset int) ([]Partner, int, error) {
	const query = `
		SELECT ` + partnerColumns + `, count(*) OVER () AS total
		FROM partners
		WHERE agency_id = $1
		  AND archived_at IS NULL
		  AND ($2 = '' OR name ILIKE '%' || $2 || '%')
		ORDER BY name
		LIMIT $3 OFFSET $4`

	rows, err := s.db.Query(ctx, query, agencyID, search, limit, offset)
	if err != nil {
		return nil, 0, mapError(err)
	}
	defer rows.Close()

	partners := make([]Partner, 0, limit)
	total := 0
	for rows.Next() {
		var partner Partner
		if err := rows.Scan(&partner.ID, &partner.Kind, &partner.Name, &partner.Phone, &partner.Email,
			&partner.Note, &partner.IsActive, &partner.CreatedAt, &partner.UpdatedAt, &total); err != nil {
			return nil, 0, mapError(err)
		}
		partners = append(partners, partner)
	}

	return partners, total, mapError(rows.Err())
}

// UpdatePartner полностью заменяет изменяемые поля.
func (s *Store) UpdatePartner(ctx context.Context, agencyID, id string, actor Actor, input PartnerInput) (Partner, error) {
	var updated Partner
	err := s.inTx(ctx, func(tx *Store) error {
		before, err := tx.Partner(ctx, agencyID, id)
		if err != nil {
			return err
		}

		const query = `
			UPDATE partners
			SET kind = $3, name = $4, phone = $5, email = $6, note = $7
			WHERE agency_id = $1 AND id = $2 AND archived_at IS NULL
			RETURNING ` + partnerColumns

		partner, err := scanPartner(tx.db.QueryRow(ctx, query,
			agencyID, id, input.Kind, input.Name, input.Phone, input.Email, input.Note))
		if err != nil {
			return err
		}
		updated = partner

		changes := diff(partnerInput(before), input)
		if len(changes) == 0 {
			return nil
		}

		return tx.recordChange(ctx, agencyID, actor, changeRecord{
			EntityType: EntityPartner,
			EntityID:   id,
			Action:     ActionUpdate,
			Changes:    changes,
			Summary:    partner.Name,
		})
	})

	return updated, err
}

// PartnerAsInput представляет сохранённого партнёра в форме, в которую сливается PATCH.
func PartnerAsInput(partner Partner) PartnerInput { return partnerInput(partner) }

func partnerInput(partner Partner) PartnerInput {
	return PartnerInput{
		Kind:  partner.Kind,
		Name:  partner.Name,
		Phone: partner.Phone,
		Email: partner.Email,
		Note:  partner.Note,
	}
}

// ArchivePartner мягко удаляет партнёра, сохраняя историю того, кто кого рекомендовал.
func (s *Store) ArchivePartner(ctx context.Context, agencyID, id string, actor Actor) error {
	return s.inTx(ctx, func(tx *Store) error {
		const query = `
			UPDATE partners SET archived_at = now()
			WHERE agency_id = $1 AND id = $2 AND archived_at IS NULL
			RETURNING name`

		var name string
		if err := tx.db.QueryRow(ctx, query, agencyID, id).Scan(&name); err != nil {
			return mapError(err)
		}

		return tx.recordChange(ctx, agencyID, actor, changeRecord{
			EntityType: EntityPartner,
			EntityID:   id,
			Action:     ActionArchive,
			Summary:    name,
		})
	})
}

// TourOperator — поставщик, через которого агентство совершает бронирования.
type TourOperator struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	INN          *string   `json:"inn,omitempty"`
	ContactPhone *string   `json:"contactPhone,omitempty"`
	ContactEmail *string   `json:"contactEmail,omitempty"`
	Website      *string   `json:"website,omitempty"`
	Note         *string   `json:"note,omitempty"`
	IsActive     bool      `json:"isActive"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// TourOperatorInput — изменяемая часть туроператора.
type TourOperatorInput struct {
	Name         string  `json:"name" history:"name"`
	INN          *string `json:"inn" history:"inn"`
	ContactPhone *string `json:"contactPhone" history:"contactPhone"`
	ContactEmail *string `json:"contactEmail" history:"contactEmail"`
	Website      *string `json:"website" history:"website"`
	Note         *string `json:"note" history:"note"`
}

// Normalize обрезает пробелы в текстовых полях.
func (in *TourOperatorInput) Normalize() {
	in.Name = strings.TrimSpace(in.Name)
	trimPtr(&in.INN)
	trimPtr(&in.Website)
	trimPtr(&in.Note)
	if in.ContactPhone != nil {
		in.ContactPhone = emptyToNil(NormalizePhone(*in.ContactPhone))
	}
	if in.ContactEmail != nil {
		in.ContactEmail = emptyToNil(strings.ToLower(strings.TrimSpace(*in.ContactEmail)))
	}
}

// Validate сообщает обо всех проблемах сразу.
func (in TourOperatorInput) Validate() error {
	v := newValidator()
	v.required("name", in.Name, 200)
	v.pattern("inn", in.INN, innRe, "ИНН — 10 или 12 цифр")
	v.phone("contactPhone", in.ContactPhone)
	v.email("contactEmail", in.ContactEmail)
	v.optional("website", in.Website, 255)
	v.optional("note", in.Note, 4000)

	return v.err()
}

const operatorColumns = `id, name, inn, contact_phone, contact_email, website, note, is_active, created_at, updated_at`

func scanOperator(row interface{ Scan(...any) error }) (TourOperator, error) {
	var operator TourOperator
	err := row.Scan(&operator.ID, &operator.Name, &operator.INN, &operator.ContactPhone,
		&operator.ContactEmail, &operator.Website, &operator.Note, &operator.IsActive,
		&operator.CreatedAt, &operator.UpdatedAt)
	if err != nil {
		return TourOperator{}, mapError(err)
	}

	return operator, nil
}

// CreateOperator регистрирует туроператора.
func (s *Store) CreateOperator(ctx context.Context, agencyID string, actor Actor, input TourOperatorInput) (TourOperator, error) {
	var created TourOperator
	err := s.inTx(ctx, func(tx *Store) error {
		const query = `
			INSERT INTO tour_operators (agency_id, name, inn, contact_phone, contact_email, website, note)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING ` + operatorColumns

		operator, err := scanOperator(tx.db.QueryRow(ctx, query, agencyID, input.Name, input.INN,
			input.ContactPhone, input.ContactEmail, input.Website, input.Note))
		if err != nil {
			return err
		}
		created = operator

		return tx.recordChange(ctx, agencyID, actor, changeRecord{
			EntityType: EntityTourOperator,
			EntityID:   operator.ID,
			Action:     ActionCreate,
			Changes:    diff(TourOperatorInput{}, input),
			Summary:    operator.Name,
		})
	})

	return created, err
}

// TourOperator загружает одного оператора внутри агентства.
func (s *Store) TourOperator(ctx context.Context, agencyID, id string) (TourOperator, error) {
	const query = `SELECT ` + operatorColumns + ` FROM tour_operators WHERE agency_id = $1 AND id = $2 AND archived_at IS NULL`

	return scanOperator(s.db.QueryRow(ctx, query, agencyID, id))
}

// ListOperators возвращает операторов, подходящих под необязательный поиск по имени.
func (s *Store) ListOperators(ctx context.Context, agencyID, search string, limit, offset int) ([]TourOperator, int, error) {
	const query = `
		SELECT ` + operatorColumns + `, count(*) OVER () AS total
		FROM tour_operators
		WHERE agency_id = $1
		  AND archived_at IS NULL
		  AND ($2 = '' OR name ILIKE '%' || $2 || '%')
		ORDER BY name
		LIMIT $3 OFFSET $4`

	rows, err := s.db.Query(ctx, query, agencyID, search, limit, offset)
	if err != nil {
		return nil, 0, mapError(err)
	}
	defer rows.Close()

	operators := make([]TourOperator, 0, limit)
	total := 0
	for rows.Next() {
		var operator TourOperator
		if err := rows.Scan(&operator.ID, &operator.Name, &operator.INN, &operator.ContactPhone,
			&operator.ContactEmail, &operator.Website, &operator.Note, &operator.IsActive,
			&operator.CreatedAt, &operator.UpdatedAt, &total); err != nil {
			return nil, 0, mapError(err)
		}
		operators = append(operators, operator)
	}

	return operators, total, mapError(rows.Err())
}

// UpdateOperator полностью заменяет изменяемые поля.
func (s *Store) UpdateOperator(ctx context.Context, agencyID, id string, actor Actor, input TourOperatorInput) (TourOperator, error) {
	var updated TourOperator
	err := s.inTx(ctx, func(tx *Store) error {
		before, err := tx.TourOperator(ctx, agencyID, id)
		if err != nil {
			return err
		}

		const query = `
			UPDATE tour_operators
			SET name = $3, inn = $4, contact_phone = $5, contact_email = $6, website = $7, note = $8
			WHERE agency_id = $1 AND id = $2 AND archived_at IS NULL
			RETURNING ` + operatorColumns

		operator, err := scanOperator(tx.db.QueryRow(ctx, query, agencyID, id, input.Name, input.INN,
			input.ContactPhone, input.ContactEmail, input.Website, input.Note))
		if err != nil {
			return err
		}
		updated = operator

		changes := diff(operatorInput(before), input)
		if len(changes) == 0 {
			return nil
		}

		return tx.recordChange(ctx, agencyID, actor, changeRecord{
			EntityType: EntityTourOperator,
			EntityID:   id,
			Action:     ActionUpdate,
			Changes:    changes,
			Summary:    operator.Name,
		})
	})

	return updated, err
}

// TourOperatorAsInput представляет сохранённого оператора в форме, в которую сливается PATCH.
func TourOperatorAsInput(operator TourOperator) TourOperatorInput { return operatorInput(operator) }

func operatorInput(operator TourOperator) TourOperatorInput {
	return TourOperatorInput{
		Name:         operator.Name,
		INN:          operator.INN,
		ContactPhone: operator.ContactPhone,
		ContactEmail: operator.ContactEmail,
		Website:      operator.Website,
		Note:         operator.Note,
	}
}

// ArchiveOperator мягко удаляет туроператора.
func (s *Store) ArchiveOperator(ctx context.Context, agencyID, id string, actor Actor) error {
	return s.inTx(ctx, func(tx *Store) error {
		const query = `
			UPDATE tour_operators SET archived_at = now()
			WHERE agency_id = $1 AND id = $2 AND archived_at IS NULL
			RETURNING name`

		var name string
		if err := tx.db.QueryRow(ctx, query, agencyID, id).Scan(&name); err != nil {
			return mapError(err)
		}

		return tx.recordChange(ctx, agencyID, actor, changeRecord{
			EntityType: EntityTourOperator,
			EntityID:   id,
			Action:     ActionArchive,
			Summary:    name,
		})
	})
}
