package pg

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("open TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping TEST_DATABASE_URL: %v", err)
	}

	return pool
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// TestMigrateIsIdempotent и TestMigrateDetectsEditedMigration оба изменяют
// одну общую строку schema_migrations для 0001_init, поэтому выполняются
// последовательно, а не через t.Parallel(), чтобы не гоняться друг с другом.
func TestMigrateIsIdempotent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	if err := Migrate(ctx, pool, discardLogger()); err != nil {
		t.Fatalf("first Migrate() error = %v", err)
	}

	var before []string
	rows, err := pool.Query(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			t.Fatalf("scan version: %v", err)
		}
		before = append(before, version)
	}
	rows.Close()

	if len(before) == 0 {
		t.Fatal("no migrations recorded after the first run")
	}

	// Повторный запуск не должен давать ошибку и не должен трогать
	// schema_migrations: все миграции уже применены.
	if err := Migrate(ctx, pool, discardLogger()); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}

	var after []string
	rows, err = pool.Query(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			t.Fatalf("scan version: %v", err)
		}
		after = append(after, version)
	}
	rows.Close()

	if len(after) != len(before) {
		t.Fatalf("schema_migrations changed on a second run: before=%v after=%v", before, after)
	}
}

func TestMigrateDetectsEditedMigration(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	if err := Migrate(ctx, pool, discardLogger()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	// Имитируем правку файла миграции после того, как он уже был выпущен:
	// записанная контрольная сумма больше не совпадает с тем, что даст
	// свежий подсчёт. Исходное значение сначала сохраняется, а затем
	// восстанавливается при завершении, поскольку другие тесты этого
	// пакета используют ту же базу данных.
	var original string
	if err := pool.QueryRow(ctx, `SELECT checksum FROM schema_migrations WHERE version = '0001_init'`).Scan(&original); err != nil {
		t.Fatalf("read original checksum: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`UPDATE schema_migrations SET checksum = $1 WHERE version = '0001_init'`, original)
	})

	if _, err := pool.Exec(ctx, `UPDATE schema_migrations SET checksum = 'tampered' WHERE version = '0001_init'`); err != nil {
		t.Fatalf("tamper with checksum: %v", err)
	}

	err := Migrate(ctx, pool, discardLogger())
	if err == nil {
		t.Fatal("Migrate() with a tampered checksum succeeded, want an error")
	}
}

func TestLoadMigrationsAreSorted(t *testing.T) {
	t.Parallel()

	migrations, err := loadMigrations(migrationsFS)
	if err != nil {
		t.Fatalf("loadMigrations() error = %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("loadMigrations() returned no migrations")
	}

	for i := 1; i < len(migrations); i++ {
		if migrations[i-1].version >= migrations[i].version {
			t.Errorf("migrations not sorted: %q before %q", migrations[i-1].version, migrations[i].version)
		}
	}

	for _, m := range migrations {
		if m.checksum == "" {
			t.Errorf("migration %s has an empty checksum", m.version)
		}
	}
}
