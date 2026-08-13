// Package store хранит доменные модели и все SQL-запросы, которые их
// затрагивают.
//
// Изоляция арендаторов (агентств) — правило, определяющее устройство этого
// пакета: каждый метод, который читает или пишет данные агентства, получает
// agencyID первым аргументом и добавляет его в WHERE. Строка, принадлежащая
// другому агентству, сообщается как ErrNotFound, а не как ошибка доступа,
// потому что ошибка доступа подтвердила бы, что строка где-то существует.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// querier — подмножество pgx, используемое запросами ниже. И *pgxpool.Pool,
// и pgx.Tx удовлетворяют этому интерфейсу, поэтому многошаговая запись
// использует тот же код, что и одношаговая, а тесты могут выполнять каждый
// метод внутри транзакции, которую потом откатывают.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store — точка входа к постоянным данным.
type Store struct {
	db querier
}

// New возвращает Store поверх пула соединений.
func New(pool *pgxpool.Pool) *Store {
	return &Store{db: pool}
}

// newWithQuerier используется тестами, чтобы привязать Store к открытой транзакции.
func newWithQuerier(db querier) *Store {
	return &Store{db: db}
}

// NewTx привязывает Store к уже открытой транзакции, а не к пулу.
//
// Существует для тестов в других пакетах, которым нужно, чтобы каждая запись
// теста жила внутри одной транзакции, откатываемой при завершении: pgx.Tx
// поддерживает вложенные транзакции через savepoint'ы, поэтому собственный
// inTx у Store продолжает работать без изменений, даже когда ему передают
// транзакцию вместо пула.
func NewTx(tx pgx.Tx) *Store {
	return &Store{db: tx}
}

// Ping сообщает, отвечает ли база данных. Используется в /api/health, но
// никогда в /api/ping: последний — это healthcheck контейнера и проверка
// деплоя, и он не должен начать падать из-за того, что база данных на
// мгновение стала недоступна.
func (s *Store) Ping(ctx context.Context) error {
	var one int

	return s.db.QueryRow(ctx, "SELECT 1").Scan(&one)
}

// inTx выполняет fn внутри транзакции, откатывая её при любой ошибке.
//
// Когда Store уже привязан к транзакции (в тестах), fn выполняется напрямую:
// pgx поддерживает вложенные транзакции через savepoint'ы, но откат одной из
// них не отменил бы внешнюю, поэтому так семантика остаётся понятнее.
func (s *Store) inTx(ctx context.Context, fn func(*Store) error) error {
	beginner, ok := s.db.(interface {
		Begin(context.Context) (pgx.Tx, error)
	})
	if !ok {
		return fn(s)
	}

	tx, err := beginner.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	// Rollback после успешного commit — это no-op, поэтому такой defer
	// покрывает все пути выполнения.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if err := fn(newWithQuerier(tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// mapError переводит ошибки PostgreSQL, которые означают отклонённый
// запрос, а не сломанный сервис. Всё остальное возвращается без изменений,
// поэтому проявляется как 500 и попадает в лог.
func mapError(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}

	switch pgErr.Code {
	case "23505": // unique_violation — нарушение уникальности
		return &ConstraintError{Kind: ErrConflict, Constraint: pgErr.ConstraintName}
	case "23503": // foreign_key_violation — нарушение внешнего ключа
		return &ConstraintError{Kind: ErrInvalidReference, Constraint: pgErr.ConstraintName}
	case "23514": // check_violation — нарушение CHECK-ограничения
		return &ConstraintError{Kind: ErrInvalidValue, Constraint: pgErr.ConstraintName}
	case "22P02": // invalid_text_representation, например некорректный uuid
		return ErrNotFound
	default:
		return err
	}
}
