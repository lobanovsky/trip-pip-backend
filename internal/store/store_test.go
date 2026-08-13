package store

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lobanovsky/trip-pip-backend/internal/pg"
)

// TestMain применяет миграции один раз за запуск тестового бинарника,
// поэтому отдельным тестам остаётся заботиться только о своих фикстурах.
func TestMain(m *testing.M) {
	code := func() int {
		url := os.Getenv("TEST_DATABASE_URL")
		if url == "" {
			return m.Run()
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		pool, err := pgxpool.New(ctx, url)
		if err != nil {
			panic("open TEST_DATABASE_URL: " + err.Error())
		}
		defer pool.Close()

		logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
		if err := pg.Migrate(ctx, pool, logger); err != nil {
			panic("migrate test database: " + err.Error())
		}

		sharedPool = pool

		return m.Run()
	}()

	os.Exit(code)
}

// testStore открывает транзакцию против TEST_DATABASE_URL и привязывает к
// ней Store. Транзакция откатывается при завершении, поэтому тесты никогда
// не видят чужие данные и могут выполняться с t.Parallel().
//
// Без TEST_DATABASE_URL тест пропускается: go test ./... остаётся зелёным
// на машине без Postgres, а значит проверка в CI обходится без новой
// тестовой зависимости.
func testStore(t *testing.T) *Store {
	t.Helper()

	pool := testPool(t)

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin test transaction: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	return newWithQuerier(tx)
}

// sharedPool открывается один раз в TestMain, который же применяет миграции.
var sharedPool *pgxpool.Pool

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	if sharedPool == nil {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	return sharedPool
}

// createTestAgency вставляет одноразовое агентство внутри транзакции вызывающей стороны.
func createTestAgency(t *testing.T, s *Store, name string) Agency {
	t.Helper()

	agency, err := s.CreateAgency(context.Background(), name, nil, "Europe/Moscow")
	if err != nil {
		t.Fatalf("create test agency: %v", err)
	}

	return agency
}

// createTestTourist вставляет минимального туриста для использования в
// качестве фикстуры в других тестах.
func createTestTourist(t *testing.T, s *Store, agencyID string, seq int) Tourist {
	t.Helper()

	input := TouristInput{
		LastName:  "Тестов",
		FirstName: "Турист",
	}

	tourist, err := s.CreateTourist(context.Background(), agencyID, Actor{Label: "test"}, input)
	if err != nil {
		t.Fatalf("create test tourist: %v", err)
	}

	return tourist
}

func rowExists(t *testing.T, s *Store, query string, args ...any) bool {
	t.Helper()

	var one int
	err := s.db.QueryRow(context.Background(), query, args...).Scan(&one)
	if err == nil {
		return true
	}
	if err == pgx.ErrNoRows {
		return false
	}
	t.Fatalf("check row existence: %v", err)

	return false
}
