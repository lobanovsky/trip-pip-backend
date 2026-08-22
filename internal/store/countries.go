package store

import "context"

// Country — строка справочника стран (ISO 3166-1 alpha-2 код + русское
// название) с видимостью и порядком, применёнными для конкретного
// агентства. IsActive/SortOrder читаются из agency_countries — отдельной
// таблицы на агентство, а не колонок прямо на countries: countries общая
// для всех агентств, и переключатель одного агентства не должен менять то,
// что видят остальные.
type Country struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	IsActive  bool   `json:"isActive"`
	SortOrder int    `json:"sortOrder"`
}

func scanCountry(row interface{ Scan(...any) error }) (Country, error) {
	var country Country
	err := row.Scan(&country.Code, &country.Name, &country.IsActive, &country.SortOrder)
	if err != nil {
		return Country{}, mapError(err)
	}

	return country, nil
}

// ListCountries возвращает справочник стран с видимостью и порядком,
// применёнными для agencyID. Страна без строки в agency_countries считается
// видимой (isActive true) с порядком по умолчанию (0).
func (s *Store) ListCountries(ctx context.Context, agencyID string) ([]Country, error) {
	const query = `
		SELECT c.code, c.name,
		       coalesce(ac.is_active, true) AS is_active,
		       coalesce(ac.sort_order, 0)   AS sort_order
		FROM countries c
		LEFT JOIN agency_countries ac ON ac.country_code = c.code AND ac.agency_id = $1
		ORDER BY coalesce(ac.sort_order, 0), c.name`

	rows, err := s.db.Query(ctx, query, agencyID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	countries := make([]Country, 0, 256)
	for rows.Next() {
		country, err := scanCountry(rows)
		if err != nil {
			return nil, err
		}
		countries = append(countries, country)
	}

	return countries, mapError(rows.Err())
}

// UpdateCountryVisibility скрывает/показывает страну или меняет её порядок
// для agencyID, не затрагивая другие агентства. Возвращает
// ErrInvalidReference, если code не существует в countries.
func (s *Store) UpdateCountryVisibility(ctx context.Context, agencyID, code string, isActive *bool, sortOrder *int) (Country, error) {
	const query = `
		INSERT INTO agency_countries (agency_id, country_code, is_active, sort_order)
		VALUES ($1, $2, coalesce($3, true), coalesce($4, 0))
		ON CONFLICT (agency_id, country_code) DO UPDATE
		SET is_active  = coalesce($3, agency_countries.is_active),
		    sort_order = coalesce($4, agency_countries.sort_order)
		RETURNING country_code, (SELECT name FROM countries WHERE code = country_code), is_active, sort_order`

	return scanCountry(s.db.QueryRow(ctx, query, agencyID, code, isActive, sortOrder))
}
