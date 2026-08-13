package pg

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationLockID защищает весь прогон миграций целиком. Если несколько
// реплик стартуют одновременно, они встают в очередь, а не гонятся за
// созданием одних и тех же таблиц.
const migrationLockID int64 = 8274531

const createMigrationsTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    text        PRIMARY KEY,
    checksum   text        NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
)`

type migration struct {
	version  string
	checksum string
	body     string
}

// Migrate применяет каждую встроенную миграцию, которая ещё не выполнялась.
// Миграции идут только вперёд: ошибка исправляется добавлением нового файла,
// а не правкой уже применённого.
func Migrate(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	migrations, err := loadMigrations(migrationsFS)
	if err != nil {
		return err
	}

	// Advisory-блокировка живёт на одном конкретном соединении, поэтому её
	// нельзя взять из пула для одного запроса, а снять на другом соединении.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		if _, err := conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLockID); err != nil {
			logger.Error("release migration lock", "error", err)
		}
	}()

	if _, err := conn.Exec(ctx, createMigrationsTable); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := appliedMigrations(ctx, conn)
	if err != nil {
		return err
	}

	for _, m := range migrations {
		checksum, ok := applied[m.version]
		if ok {
			if checksum != m.checksum {
				return fmt.Errorf(
					"migration %s changed after it was applied (checksum %s, want %s); add a new migration instead of editing this one",
					m.version, m.checksum, checksum)
			}

			continue
		}

		if err := applyMigration(ctx, conn, m); err != nil {
			return err
		}

		logger.Info("migration applied", "version", m.version)
	}

	return nil
}

func applyMigration(ctx context.Context, conn interface {
	Begin(context.Context) (pgx.Tx, error)
}, m migration) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", m.version, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, m.body); err != nil {
		return fmt.Errorf("apply migration %s: %w", m.version, err)
	}

	const insertVersion = `INSERT INTO schema_migrations (version, checksum) VALUES ($1, $2)`
	if _, err := tx.Exec(ctx, insertVersion, m.version, m.checksum); err != nil {
		return fmt.Errorf("record migration %s: %w", m.version, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %s: %w", m.version, err)
	}

	return nil
}

func appliedMigrations(ctx context.Context, conn interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}) (map[string]string, error) {
	rows, err := conn.Query(ctx, "SELECT version, checksum FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]string)
	for rows.Next() {
		var version, checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[version] = checksum
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}

	return applied, nil
}

// loadMigrations читает и сортирует встроенные файлы. Имена начинаются с
// числа с ведущими нулями, поэтому лексикографический порядок совпадает с
// хронологическим.
func loadMigrations(files fs.FS) ([]migration, error) {
	entries, err := fs.Glob(files, "migrations/*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	if len(entries) == 0 {
		return nil, errors.New("no migrations found")
	}

	sort.Strings(entries)

	migrations := make([]migration, 0, len(entries))
	for _, name := range entries {
		body, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}

		sum := sha256.Sum256(body)
		migrations = append(migrations, migration{
			version:  strings.TrimSuffix(strings.TrimPrefix(name, "migrations/"), ".sql"),
			checksum: hex.EncodeToString(sum[:]),
			body:     string(body),
		})
	}

	return migrations, nil
}
