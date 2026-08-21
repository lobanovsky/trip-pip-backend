package store

import (
	"context"
	"time"
)

// Agency — одно туристическое агентство. Это граница арендатора: ни один
// запрос никогда не возвращает строки больше чем из одного агентства.
type Agency struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	INN       *string   `json:"inn,omitempty"`
	Timezone  string    `json:"timezone"`
	IsActive  bool      `json:"isActive"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// User — сотрудник агентства. На первом этапе у всех сотрудников агентства
// одинаковый доступ, поэтому столбца с ролью пока нет.
type User struct {
	ID          string     `json:"id"`
	AgencyID    string     `json:"agencyId"`
	Email       string     `json:"email"`
	FullName    string     `json:"fullName"`
	IsActive    bool       `json:"isActive"`
	LastLoginAt *time.Time `json:"lastLoginAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`

	// EmailVerifiedAt — nil у самостоятельно зарегистрировавшегося
	// пользователя, пока он не перешёл по ссылке из письма. Отдельно от
	// IsActive: тот флаг занят под отключение коллеги администратором
	// (SetUserActive) и означает совсем другое.
	EmailVerifiedAt *time.Time `json:"emailVerifiedAt,omitempty"`

	// PasswordHash никогда не сериализуется; тег json "-" стоит намеренно.
	PasswordHash string `json:"-"`
}

// Identity — аутентифицированный вызывающий, полученный из токена сессии.
type Identity struct {
	SessionID  string
	UserID     string
	AgencyID   string
	Email      string
	FullName   string
	AgencyName string
	LastSeenAt time.Time
}

// Actor описывает эту личность для журнала изменений.
func (i Identity) Actor(requestID string) Actor {
	return Actor{UserID: i.UserID, Label: i.FullName, RequestID: requestID}
}

const agencyColumns = `id, name, inn, timezone, is_active, created_at, updated_at`

// CreateAgency регистрирует новое агентство.
func (s *Store) CreateAgency(ctx context.Context, name string, inn *string, timezone string) (Agency, error) {
	const query = `
		INSERT INTO agencies (name, inn, timezone)
		VALUES ($1, $2, $3)
		RETURNING ` + agencyColumns

	if timezone == "" {
		timezone = "Europe/Moscow"
	}

	var agency Agency
	err := s.db.QueryRow(ctx, query, name, inn, timezone).Scan(
		&agency.ID, &agency.Name, &agency.INN, &agency.Timezone,
		&agency.IsActive, &agency.CreatedAt, &agency.UpdatedAt)
	if err != nil {
		return Agency{}, mapError(err)
	}

	return agency, nil
}

// Agency загружает одно агентство по id.
func (s *Store) Agency(ctx context.Context, id string) (Agency, error) {
	const query = `SELECT ` + agencyColumns + ` FROM agencies WHERE id = $1`

	var agency Agency
	err := s.db.QueryRow(ctx, query, id).Scan(
		&agency.ID, &agency.Name, &agency.INN, &agency.Timezone,
		&agency.IsActive, &agency.CreatedAt, &agency.UpdatedAt)
	if err != nil {
		return Agency{}, mapError(err)
	}

	return agency, nil
}

const userColumns = `id, agency_id, email, full_name, is_active, last_login_at, created_at, updated_at, email_verified_at`

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var user User
	err := row.Scan(&user.ID, &user.AgencyID, &user.Email, &user.FullName,
		&user.IsActive, &user.LastLoginAt, &user.CreatedAt, &user.UpdatedAt, &user.EmailVerifiedAt)
	if err != nil {
		return User{}, mapError(err)
	}

	return user, nil
}

// CreateUser добавляет сотрудника в агентство. Вызывающая сторона передаёт
// уже хешированный пароль: этот пакет никогда не видит пароль в открытом виде.
//
// email_verified_at проставляется сразу: и bootstrap, и POST /api/users
// создают учётную запись по решению уже доверенного действующего лица
// (оператора деплоя или вошедшего в систему коллеги), а не самого владельца
// адреса — подтверждать здесь нечего. NULL остаётся только у пользователей,
// заведённых через самостоятельную регистрацию (Store.RegisterAgency).
func (s *Store) CreateUser(ctx context.Context, agencyID, email, passwordHash, fullName string) (User, error) {
	const query = `
		INSERT INTO users (agency_id, email, password_hash, full_name, email_verified_at)
		VALUES ($1, $2, $3, $4, now())
		RETURNING ` + userColumns

	return scanUser(s.db.QueryRow(ctx, query, agencyID, email, passwordHash, fullName))
}

// UserByEmail — поиск для входа в систему. Email уникален среди всех
// агентств, поэтому позволяет найти учётную запись без предварительного
// выбора агентства.
func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	const query = `
		SELECT ` + userColumns + `, password_hash
		FROM users
		WHERE email = $1`

	var user User
	err := s.db.QueryRow(ctx, query, email).Scan(&user.ID, &user.AgencyID, &user.Email, &user.FullName,
		&user.IsActive, &user.LastLoginAt, &user.CreatedAt, &user.UpdatedAt, &user.EmailVerifiedAt, &user.PasswordHash)
	if err != nil {
		return User{}, mapError(err)
	}

	return user, nil
}

// User загружает коллегу внутри агентства вызывающей стороны.
func (s *Store) User(ctx context.Context, agencyID, id string) (User, error) {
	const query = `SELECT ` + userColumns + ` FROM users WHERE agency_id = $1 AND id = $2`

	return scanUser(s.db.QueryRow(ctx, query, agencyID, id))
}

// ListUsers возвращает сотрудников агентства, сначала самые старые записи.
func (s *Store) ListUsers(ctx context.Context, agencyID string, limit, offset int) ([]User, int, error) {
	const query = `
		SELECT ` + userColumns + `, count(*) OVER () AS total
		FROM users
		WHERE agency_id = $1
		ORDER BY created_at
		LIMIT $2 OFFSET $3`

	rows, err := s.db.Query(ctx, query, agencyID, limit, offset)
	if err != nil {
		return nil, 0, mapError(err)
	}
	defer rows.Close()

	users := make([]User, 0, limit)
	total := 0
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.ID, &user.AgencyID, &user.Email, &user.FullName,
			&user.IsActive, &user.LastLoginAt, &user.CreatedAt, &user.UpdatedAt, &user.EmailVerifiedAt, &total); err != nil {
			return nil, 0, mapError(err)
		}
		users = append(users, user)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, mapError(err)
	}

	return users, total, nil
}

// SetUserFullName меняет отображаемое имя коллеги.
func (s *Store) SetUserFullName(ctx context.Context, agencyID, id, fullName string) (User, error) {
	const query = `
		UPDATE users SET full_name = $3
		WHERE agency_id = $1 AND id = $2
		RETURNING ` + userColumns

	return scanUser(s.db.QueryRow(ctx, query, agencyID, id, fullName))
}

// SetUserActive включает или отключает учётную запись коллеги.
func (s *Store) SetUserActive(ctx context.Context, agencyID, id string, active bool) (User, error) {
	const query = `
		UPDATE users SET is_active = $3
		WHERE agency_id = $1 AND id = $2
		RETURNING ` + userColumns

	return scanUser(s.db.QueryRow(ctx, query, agencyID, id, active))
}

// SetUserPassword заменяет хеш пароля коллеги.
func (s *Store) SetUserPassword(ctx context.Context, agencyID, id, passwordHash string) error {
	const query = `UPDATE users SET password_hash = $3 WHERE agency_id = $1 AND id = $2`

	tag, err := s.db.Exec(ctx, query, agencyID, id, passwordHash)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	return nil
}

// CountUsers сообщает общее число учётных записей. Bootstrap использует это,
// чтобы понять, что перед ним совершенно новая установка.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		return 0, mapError(err)
	}

	return count, nil
}

// Сессии -----------------------------------------------------------------------

// CreateSession сохраняет новую сессию. Хранится только хеш токена, поэтому
// дамп базы данных не отдаёт рабочие сессии.
func (s *Store) CreateSession(ctx context.Context, userID, agencyID string, tokenHash []byte, expiresAt time.Time, userAgent string) (string, error) {
	const query = `
		INSERT INTO sessions (user_id, agency_id, token_hash, expires_at, user_agent)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`

	if len(userAgent) > 255 {
		userAgent = userAgent[:255]
	}

	var id string
	if err := s.db.QueryRow(ctx, query, userID, agencyID, tokenHash, expiresAt, nullString(userAgent)).Scan(&id); err != nil {
		return "", mapError(err)
	}

	return id, nil
}

// IdentityByToken определяет вызывающую сторону по хешу токена сессии.
//
// Истёкшая, отозванная, отключённая или неизвестная сессия сообщается
// одинаково — как ErrNotFound: клиент узнаёт только то, что нужно войти
// заново.
func (s *Store) IdentityByToken(ctx context.Context, tokenHash []byte) (Identity, error) {
	const query = `
		SELECT s.id, s.user_id, s.agency_id, u.email, u.full_name, a.name, s.last_seen_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		JOIN agencies a ON a.id = s.agency_id
		WHERE s.token_hash = $1
		  AND s.revoked_at IS NULL
		  AND s.expires_at > now()
		  AND u.is_active
		  AND a.is_active`

	var identity Identity
	err := s.db.QueryRow(ctx, query, tokenHash).Scan(&identity.SessionID, &identity.UserID, &identity.AgencyID,
		&identity.Email, &identity.FullName, &identity.AgencyName, &identity.LastSeenAt)
	if err != nil {
		return Identity{}, mapError(err)
	}

	return identity, nil
}

// TouchSession фиксирует, что сессия использовалась.
func (s *Store) TouchSession(ctx context.Context, sessionID string) error {
	_, err := s.db.Exec(ctx, `UPDATE sessions SET last_seen_at = now() WHERE id = $1`, sessionID)

	return mapError(err)
}

// RevokeSession завершает одну сессию; повторный выход не считается ошибкой.
func (s *Store) RevokeSession(ctx context.Context, sessionID string) error {
	const query = `UPDATE sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`
	_, err := s.db.Exec(ctx, query, sessionID)

	return mapError(err)
}

// RevokeUserSessions завершает все сессии одного пользователя — именно это
// должно происходить при отключении учётной записи или смене пароля.
func (s *Store) RevokeUserSessions(ctx context.Context, userID string) error {
	const query = `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`
	_, err := s.db.Exec(ctx, query, userID)

	return mapError(err)
}

// DeleteExpiredSessions удаляет строки, которые уже не могут никого аутентифицировать.
func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	const query = `DELETE FROM sessions WHERE expires_at < now() - interval '7 days'`

	tag, err := s.db.Exec(ctx, query)
	if err != nil {
		return 0, mapError(err)
	}

	return tag.RowsAffected(), nil
}

// MarkLogin фиксирует успешный вход в строке пользователя.
func (s *Store) MarkLogin(ctx context.Context, userID string) error {
	_, err := s.db.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, userID)

	return mapError(err)
}
