package store

import (
	"context"
	"fmt"
)

// BootstrapRequest описывает первое агентство и его первую учётную запись.
type BootstrapRequest struct {
	AgencyName   string
	Timezone     string
	Email        string
	FullName     string
	PasswordHash string
}

// EnsureBootstrap создаёт первое агентство и пользователя, но только пока в
// установке вообще нет ни одной учётной записи.
//
// Новые агентства теперь заводятся и через открытую саморегистрацию
// (RegisterAgency, POST /api/auth/register), но та зависит от настроенного
// SMTP — письмо с подтверждением иначе отправить нечем. EnsureBootstrap
// остаётся операционным запасным входом для самой первой установки, когда
// почта ещё не настроена: переменные окружения дают идемпотентность и
// работоспособность в контейнере, доступном только для чтения, чего не дал
// бы отдельный CLI-бинарник.
//
// Функция сообщает, создала ли она что-нибудь.
func (s *Store) EnsureBootstrap(ctx context.Context, request BootstrapRequest) (bool, error) {
	created := false

	err := s.inTx(ctx, func(tx *Store) error {
		// Сериализует параллельно стартующие процессы, чтобы две реплики не
		// решили одновременно, что установка пуста, и не создали два агентства.
		if _, err := tx.db.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", bootstrapLockID); err != nil {
			return fmt.Errorf("acquire bootstrap lock: %w", err)
		}

		count, err := tx.CountUsers(ctx)
		if err != nil {
			return err
		}
		if count > 0 {
			return nil
		}

		agency, err := tx.CreateAgency(ctx, request.AgencyName, nil, request.Timezone)
		if err != nil {
			return fmt.Errorf("create agency: %w", err)
		}

		for _, channel := range DefaultChannels {
			if _, err := tx.CreateChannel(ctx, agency.ID, channel.Code, channel.Name, channel.SortOrder); err != nil {
				return fmt.Errorf("seed channel %s: %w", channel.Code, err)
			}
		}

		user, err := tx.CreateUser(ctx, agency.ID, request.Email, request.PasswordHash, request.FullName)
		if err != nil {
			return fmt.Errorf("create user: %w", err)
		}

		created = true

		return tx.recordChange(ctx, agency.ID, Actor{Label: "bootstrap"}, changeRecord{
			EntityType: EntityUser,
			EntityID:   user.ID,
			Action:     ActionCreate,
			Summary:    "Первичная учётная запись агентства",
		})
	})

	return created, err
}

// bootstrapLockID не связан с блокировкой миграций; они не должны совпадать.
const bootstrapLockID int64 = 8274532
