package store

import (
	"context"
	"strings"
	"time"
)

// Виды плательщика.
const (
	PayerIndividual = "individual"
	PayerCompany    = "company"
)

// Payer — тот, кто на самом деле платит за поездку: один из туристов,
// другое частное лицо или компания, покупающая поездку для своих сотрудников.
type Payer struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`

	TouristID      *string `json:"touristId,omitempty"`
	IndividualName *string `json:"individualName,omitempty"`

	CompanyName  *string `json:"companyName,omitempty"`
	INN          *string `json:"inn,omitempty"`
	KPP          *string `json:"kpp,omitempty"`
	OGRN         *string `json:"ogrn,omitempty"`
	LegalAddress *string `json:"legalAddress,omitempty"`
	BankDetails  *string `json:"bankDetails,omitempty"`

	ContactPhone *string `json:"contactPhone,omitempty"`
	ContactEmail *string `json:"contactEmail,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// PayerInput — изменяемая часть плательщика.
type PayerInput struct {
	Kind string `json:"kind" history:"kind"`

	TouristID      *string `json:"touristId" history:"touristId"`
	IndividualName *string `json:"individualName" history:"individualName"`

	CompanyName  *string `json:"companyName" history:"companyName"`
	INN          *string `json:"inn" history:"inn"`
	KPP          *string `json:"kpp" history:"kpp"`
	OGRN         *string `json:"ogrn" history:"ogrn"`
	LegalAddress *string `json:"legalAddress" history:"legalAddress"`
	BankDetails  *string `json:"bankDetails" history:"bankDetails"`

	ContactPhone *string `json:"contactPhone" history:"contactPhone"`
	ContactEmail *string `json:"contactEmail" history:"contactEmail"`
}

// Normalize обрезает пробелы в текстовых полях и очищает те из них, что не
// относятся к выбранному виду, — поэтому форма, переключённая с компании на
// физлицо, не оставляет после себя бесхозный ИНН.
func (in *PayerInput) Normalize() {
	in.Kind = strings.TrimSpace(in.Kind)
	trimPtr(&in.IndividualName)
	trimPtr(&in.CompanyName)
	trimPtr(&in.INN)
	trimPtr(&in.KPP)
	trimPtr(&in.OGRN)
	trimPtr(&in.LegalAddress)
	trimPtr(&in.BankDetails)

	if in.ContactPhone != nil {
		in.ContactPhone = emptyToNil(NormalizePhone(*in.ContactPhone))
	}
	if in.ContactEmail != nil {
		in.ContactEmail = emptyToNil(strings.ToLower(strings.TrimSpace(*in.ContactEmail)))
	}

	switch in.Kind {
	case PayerIndividual:
		in.CompanyName, in.INN, in.KPP, in.OGRN, in.LegalAddress = nil, nil, nil, nil, nil
	case PayerCompany:
		in.TouristID, in.IndividualName = nil, nil
	}
}

// Validate сообщает обо всех проблемах сразу.
func (in PayerInput) Validate() error {
	v := newValidator()
	v.oneOf("kind", in.Kind, PayerIndividual, PayerCompany)

	switch in.Kind {
	case PayerIndividual:
		if in.TouristID == nil && (in.IndividualName == nil || *in.IndividualName == "") {
			v.add("individualName", "укажите туриста или имя плательщика")
		}
		v.optional("individualName", in.IndividualName, 200)
	case PayerCompany:
		if in.CompanyName == nil || *in.CompanyName == "" {
			v.add("companyName", "обязательное поле")
		}
		v.optional("companyName", in.CompanyName, 200)
		if in.INN == nil || *in.INN == "" {
			v.add("inn", "обязательное поле")
		}
		v.pattern("inn", in.INN, innRe, "ИНН — 10 или 12 цифр")
		v.pattern("kpp", in.KPP, kppRe, "КПП — 9 цифр")
		v.pattern("ogrn", in.OGRN, ogrnRe, "ОГРН — 13 или 15 цифр")
	}

	v.phone("contactPhone", in.ContactPhone)
	v.email("contactEmail", in.ContactEmail)
	v.optional("legalAddress", in.LegalAddress, 500)
	v.optional("bankDetails", in.BankDetails, 1000)

	return v.err()
}

const payerColumns = `id, kind, tourist_id, individual_name, company_name, inn, kpp, ogrn,
	legal_address, bank_details, contact_phone, contact_email, created_at, updated_at`

func scanPayer(row interface{ Scan(...any) error }) (Payer, error) {
	var payer Payer
	err := row.Scan(&payer.ID, &payer.Kind, &payer.TouristID, &payer.IndividualName,
		&payer.CompanyName, &payer.INN, &payer.KPP, &payer.OGRN, &payer.LegalAddress,
		&payer.BankDetails, &payer.ContactPhone, &payer.ContactEmail,
		&payer.CreatedAt, &payer.UpdatedAt)
	if err != nil {
		return Payer{}, mapError(err)
	}

	return payer, nil
}

// Label — читаемое человеком имя плательщика, используется в кратком описании журнала.
func (p Payer) Label() string {
	switch {
	case p.CompanyName != nil:
		return *p.CompanyName
	case p.IndividualName != nil:
		return *p.IndividualName
	default:
		return "плательщик"
	}
}

// CreatePayer регистрирует плательщика.
func (s *Store) CreatePayer(ctx context.Context, agencyID string, actor Actor, input PayerInput) (Payer, error) {
	var created Payer
	err := s.inTx(ctx, func(tx *Store) error {
		const query = `
			INSERT INTO payers (agency_id, kind, tourist_id, individual_name, company_name, inn, kpp,
			                    ogrn, legal_address, bank_details, contact_phone, contact_email)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			RETURNING ` + payerColumns

		payer, err := scanPayer(tx.db.QueryRow(ctx, query, agencyID, input.Kind, input.TouristID,
			input.IndividualName, input.CompanyName, input.INN, input.KPP, input.OGRN,
			input.LegalAddress, input.BankDetails, input.ContactPhone, input.ContactEmail))
		if err != nil {
			return err
		}
		created = payer

		return tx.recordChange(ctx, agencyID, actor, changeRecord{
			EntityType: EntityPayer,
			EntityID:   payer.ID,
			Action:     ActionCreate,
			Changes:    diff(PayerInput{}, input),
			Summary:    payer.Label(),
		})
	})

	return created, err
}

// Payer загружает одного плательщика внутри агентства.
func (s *Store) Payer(ctx context.Context, agencyID, id string) (Payer, error) {
	const query = `SELECT ` + payerColumns + ` FROM payers WHERE agency_id = $1 AND id = $2 AND archived_at IS NULL`

	return scanPayer(s.db.QueryRow(ctx, query, agencyID, id))
}

// ListPayers возвращает плательщиков, с необязательной фильтрацией по виду и имени.
func (s *Store) ListPayers(ctx context.Context, agencyID, kind, search string, limit, offset int) ([]Payer, int, error) {
	const query = `
		SELECT ` + payerColumns + `, count(*) OVER () AS total
		FROM payers
		WHERE agency_id = $1
		  AND archived_at IS NULL
		  AND ($2 = '' OR kind = $2)
		  AND ($3 = '' OR coalesce(company_name, '') || ' ' || coalesce(individual_name, '') ILIKE '%' || $3 || '%')
		ORDER BY created_at DESC
		LIMIT $4 OFFSET $5`

	rows, err := s.db.Query(ctx, query, agencyID, kind, search, limit, offset)
	if err != nil {
		return nil, 0, mapError(err)
	}
	defer rows.Close()

	payers := make([]Payer, 0, limit)
	total := 0
	for rows.Next() {
		var payer Payer
		if err := rows.Scan(&payer.ID, &payer.Kind, &payer.TouristID, &payer.IndividualName,
			&payer.CompanyName, &payer.INN, &payer.KPP, &payer.OGRN, &payer.LegalAddress,
			&payer.BankDetails, &payer.ContactPhone, &payer.ContactEmail,
			&payer.CreatedAt, &payer.UpdatedAt, &total); err != nil {
			return nil, 0, mapError(err)
		}
		payers = append(payers, payer)
	}

	return payers, total, mapError(rows.Err())
}

// UpdatePayer полностью заменяет изменяемые поля.
func (s *Store) UpdatePayer(ctx context.Context, agencyID, id string, actor Actor, input PayerInput) (Payer, error) {
	var updated Payer
	err := s.inTx(ctx, func(tx *Store) error {
		before, err := tx.Payer(ctx, agencyID, id)
		if err != nil {
			return err
		}

		const query = `
			UPDATE payers
			SET kind = $3, tourist_id = $4, individual_name = $5, company_name = $6, inn = $7,
			    kpp = $8, ogrn = $9, legal_address = $10, bank_details = $11,
			    contact_phone = $12, contact_email = $13
			WHERE agency_id = $1 AND id = $2 AND archived_at IS NULL
			RETURNING ` + payerColumns

		payer, err := scanPayer(tx.db.QueryRow(ctx, query, agencyID, id, input.Kind, input.TouristID,
			input.IndividualName, input.CompanyName, input.INN, input.KPP, input.OGRN,
			input.LegalAddress, input.BankDetails, input.ContactPhone, input.ContactEmail))
		if err != nil {
			return err
		}
		updated = payer

		changes := diff(payerInput(before), input)
		if len(changes) == 0 {
			return nil
		}

		return tx.recordChange(ctx, agencyID, actor, changeRecord{
			EntityType: EntityPayer,
			EntityID:   id,
			Action:     ActionUpdate,
			Changes:    changes,
			Summary:    payer.Label(),
		})
	})

	return updated, err
}

// PayerAsInput представляет сохранённого плательщика в форме, в которую сливается PATCH.
func PayerAsInput(payer Payer) PayerInput { return payerInput(payer) }

func payerInput(payer Payer) PayerInput {
	return PayerInput{
		Kind:           payer.Kind,
		TouristID:      payer.TouristID,
		IndividualName: payer.IndividualName,
		CompanyName:    payer.CompanyName,
		INN:            payer.INN,
		KPP:            payer.KPP,
		OGRN:           payer.OGRN,
		LegalAddress:   payer.LegalAddress,
		BankDetails:    payer.BankDetails,
		ContactPhone:   payer.ContactPhone,
		ContactEmail:   payer.ContactEmail,
	}
}

// ArchivePayer мягко удаляет плательщика.
func (s *Store) ArchivePayer(ctx context.Context, agencyID, id string, actor Actor) error {
	return s.inTx(ctx, func(tx *Store) error {
		const query = `
			UPDATE payers SET archived_at = now()
			WHERE agency_id = $1 AND id = $2 AND archived_at IS NULL
			RETURNING id`

		var archived string
		if err := tx.db.QueryRow(ctx, query, agencyID, id).Scan(&archived); err != nil {
			return mapError(err)
		}

		return tx.recordChange(ctx, agencyID, actor, changeRecord{
			EntityType: EntityPayer,
			EntityID:   id,
			Action:     ActionArchive,
		})
	})
}
