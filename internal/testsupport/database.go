// Package testsupport provides the database fixture the integration tests share.
package testsupport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Omnific-Tech/herald/internal/store/postgres"
)

// Database returns a migrated, empty database private to the named suite.
//
// Each suite gets its own database so test binaries can run concurrently. They
// previously shared one and truncated it out from under each other, which is
// what `go test -p 1` was working around.
func Database(t *testing.T, suite string) *sql.DB {
	t.Helper()

	adminDSN := os.Getenv("HERALD_TEST_DATABASE_URL")
	if adminDSN == "" {
		t.Skip("HERALD_TEST_DATABASE_URL is not set")
	}

	name := "herald_test_" + suite
	suiteDSN, err := withDatabase(adminDSN, name)
	if err != nil {
		t.Fatalf("build suite dsn: %v", err)
	}

	ctx := context.Background()
	ensureDatabase(ctx, t, adminDSN, name)

	db, err := postgres.Open(ctx, suiteDSN, postgres.Options{MaxOpenConns: 8, MaxIdleConns: 4, ConnMaxLifetime: time.Minute})
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := postgres.Migrate(ctx, db); err != nil {
		t.Fatalf("migrate %s: %v", name, err)
	}
	Truncate(t, db)

	return db
}

// Truncate empties every data table, leaving the schema and its migration
// ledger in place.
func Truncate(t *testing.T, db *sql.DB) {
	t.Helper()

	const query = `TRUNCATE applications, producers, devices, subscriptions, messages, deliveries, acknowledgements, admin_sessions, routing_topics RESTART IDENTITY CASCADE`
	if _, err := db.Exec(query); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func ensureDatabase(ctx context.Context, t *testing.T, adminDSN, name string) {
	t.Helper()

	admin, err := sql.Open("postgres", adminDSN)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	defer func() { _ = admin.Close() }()

	var exists bool
	if err := admin.QueryRowContext(ctx, `SELECT true FROM pg_database WHERE datname = $1`, name).Scan(&exists); err == nil {
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("look up %s: %v", name, err)
	}

	// CREATE DATABASE takes no parameters and cannot run in a transaction.
	if _, err := admin.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %q", name)); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
}

func withDatabase(dsn, name string) (string, error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	parsed.Path = "/" + name
	return parsed.String(), nil
}
