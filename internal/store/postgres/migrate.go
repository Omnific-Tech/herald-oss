package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/Omnific-Tech/herald/migrations"
)

// Migrate applies every unapplied migration in filename order and returns the
// versions it ran. Applying an already-applied version is a no-op, so it is
// safe to run on every deploy.
func Migrate(ctx context.Context, db *sql.DB) ([]string, error) {
	entries, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(entries)

	applied := []string{}
	for _, entry := range entries {
		version := strings.TrimSuffix(entry, ".sql")

		body, err := fs.ReadFile(migrations.Files, entry)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", entry, err)
		}

		ran, err := applyOnce(ctx, db, version, string(body))
		if err != nil {
			return nil, fmt.Errorf("apply %s: %w", entry, err)
		}
		if ran {
			applied = append(applied, version)
		}
	}
	return applied, nil
}

// applyOnce runs one migration and records it in the same transaction, so a
// failure halfway leaves neither the schema change nor the record.
func applyOnce(ctx context.Context, db *sql.DB, version, body string) (bool, error) {
	// The bootstrap migration creates the ledger it would be recorded in, so it
	// runs before any lookup is possible. It is written to be idempotent.
	if strings.HasSuffix(version, "schema_migrations") {
		if _, err := db.ExecContext(ctx, body); err != nil {
			return false, err
		}
		_, err := db.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING`, version)
		return false, err
	}

	var exists bool
	err := db.QueryRowContext(ctx, `SELECT true FROM schema_migrations WHERE version = $1`, version).Scan(&exists)
	switch {
	case err == nil:
		return false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return false, err
	}

	// Each migration file opens its own BEGIN/COMMIT, so it cannot be wrapped in
	// an outer transaction here; the ledger insert follows immediately.
	if _, err := db.ExecContext(ctx, body); err != nil {
		return false, err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
		return false, err
	}
	return true, nil
}
