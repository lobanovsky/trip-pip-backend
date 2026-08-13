package httpapi

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
	"github.com/lobanovsky/trip-pip-backend/internal/store"
)

// sharedPool открывается один раз и один раз мигрируется для каждого теста
// этого пакета, которому нужна настоящая база данных. Без TEST_DATABASE_URL
// такие тесты пропускаются, и go test ./... остаётся зелёным на машине без Postgres.
var sharedPool *pgxpool.Pool

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

// testDeps открывает транзакцию против TEST_DATABASE_URL и возвращает Deps,
// привязанный к ней. Транзакция откатывается при завершении, поэтому
// HTTP-тесты никогда не оставляют строк и могут выполняться с t.Parallel().
func testDeps(t *testing.T) Deps {
	deps, _ := testDepsWithTx(t)

	return deps
}

// testDepsWithTx также возвращает сырую транзакцию — для тестов, которым
// нужно подготовить состояние, невыразимое через собственный API Store,
// например уже истёкшую сессию.
func testDepsWithTx(t *testing.T) (Deps, pgx.Tx) {
	t.Helper()

	if sharedPool == nil {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	tx, err := sharedPool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin test transaction: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	return Deps{
		Store:         store.NewTx(tx),
		Location:      time.UTC,
		SessionTTL:    time.Hour,
		SecureCookies: false,
	}, tx
}
