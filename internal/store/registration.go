package store

import (
	"context"
	"time"
)

// RegisterAgency заводит новое агентство через открытую саморегистрацию:
// агентство, стандартные каналы привлечения (как при bootstrap), пользователь
// с email_verified_at = NULL и токен подтверждения — одной транзакцией.
//
// В отличие от EnsureBootstrap здесь нет advisory-lock: гонка невозможна,
// потому что уникальность email обеспечена на уровне схемы (users_email_key),
// а параллельная регистрация с одним и тем же адресом просто получит ErrConflict.
func (s *Store) RegisterAgency(ctx context.Context, agencyName, email, passwordHash, fullName string,
	tokenHash []byte, tokenTTL time.Duration) (Agency, User, error) {
	var (
		agency Agency
		user   User
	)

	err := s.inTx(ctx, func(tx *Store) error {
		created, err := tx.CreateAgency(ctx, agencyName, nil, "")
		if err != nil {
			return err
		}
		agency = created

		for _, channel := range DefaultChannels {
			if _, err := tx.CreateChannel(ctx, agency.ID, channel.Code, channel.Name, channel.SortOrder); err != nil {
				return err
			}
		}

		const insertUser = `
			INSERT INTO users (agency_id, email, password_hash, full_name)
			VALUES ($1, $2, $3, $4)
			RETURNING ` + userColumns

		created2, err := scanUser(tx.db.QueryRow(ctx, insertUser, agency.ID, email, passwordHash, fullName))
		if err != nil {
			return err
		}
		user = created2

		const insertToken = `
			INSERT INTO email_verification_tokens (user_id, agency_id, token_hash, expires_at)
			VALUES ($1, $2, $3, $4)`

		if _, err := tx.db.Exec(ctx, insertToken, user.ID, agency.ID, tokenHash, time.Now().Add(tokenTTL)); err != nil {
			return mapError(err)
		}

		return tx.recordChange(ctx, agency.ID, Actor{Label: "registration"}, changeRecord{
			EntityType: EntityUser,
			EntityID:   user.ID,
			Action:     ActionCreate,
			Summary:    "Саморегистрация агентства",
		})
	})

	return agency, user, err
}

// VerifyEmailToken активирует учётную запись по токену из письма
// подтверждения. Возвращает пользователя и агентство, чтобы вызывающая
// сторона могла сразу открыть сессию.
func (s *Store) VerifyEmailToken(ctx context.Context, tokenHash []byte) (User, Agency, error) {
	var (
		user   User
		agency Agency
	)

	err := s.inTx(ctx, func(tx *Store) error {
		const selectToken = `
			SELECT user_id, agency_id
			FROM email_verification_tokens
			WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()`

		var userID, agencyID string
		if err := tx.db.QueryRow(ctx, selectToken, tokenHash).Scan(&userID, &agencyID); err != nil {
			return mapError(err)
		}

		const markUsed = `UPDATE email_verification_tokens SET used_at = now() WHERE token_hash = $1`
		if _, err := tx.db.Exec(ctx, markUsed, tokenHash); err != nil {
			return mapError(err)
		}

		const activateUser = `
			UPDATE users SET email_verified_at = now()
			WHERE id = $1 AND agency_id = $2
			RETURNING ` + userColumns

		activated, err := scanUser(tx.db.QueryRow(ctx, activateUser, userID, agencyID))
		if err != nil {
			return err
		}
		user = activated

		loaded, err := tx.Agency(ctx, agencyID)
		if err != nil {
			return err
		}
		agency = loaded

		return tx.recordChange(ctx, agencyID, Actor{Label: "registration"}, changeRecord{
			EntityType: EntityUser,
			EntityID:   user.ID,
			Action:     ActionUpdate,
			Summary:    "Email подтверждён",
		})
	})

	return user, agency, err
}

// ReissueVerificationToken выпускает новый токен подтверждения взамен
// возможно утраченного или истёкшего. Не различает в ошибке «нет такого
// email» и «email уже подтверждён» — обе сообщаются как ErrNotFound, чтобы
// вызывающая сторона не превратила этот эндпоинт в способ проверить, кто уже
// зарегистрирован.
func (s *Store) ReissueVerificationToken(ctx context.Context, email string, tokenHash []byte, tokenTTL time.Duration) error {
	return s.inTx(ctx, func(tx *Store) error {
		const selectUser = `
			SELECT id, agency_id FROM users WHERE email = $1 AND email_verified_at IS NULL`

		var userID, agencyID string
		if err := tx.db.QueryRow(ctx, selectUser, email).Scan(&userID, &agencyID); err != nil {
			return mapError(err)
		}

		const invalidateOld = `
			UPDATE email_verification_tokens SET used_at = now()
			WHERE user_id = $1 AND used_at IS NULL`
		if _, err := tx.db.Exec(ctx, invalidateOld, userID); err != nil {
			return mapError(err)
		}

		const insertToken = `
			INSERT INTO email_verification_tokens (user_id, agency_id, token_hash, expires_at)
			VALUES ($1, $2, $3, $4)`

		_, err := tx.db.Exec(ctx, insertToken, userID, agencyID, tokenHash, time.Now().Add(tokenTTL))

		return mapError(err)
	})
}
