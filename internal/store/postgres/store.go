// Package postgres is the Postgres implementation of the store interfaces.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Omnific-Tech/herald/internal/crypto"
	"github.com/Omnific-Tech/herald/internal/store"
	_ "github.com/lib/pq"
)

// Options tunes the connection pool.
type Options struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// Open dials Postgres and verifies the connection before returning it.
func Open(ctx context.Context, dsn string, options Options) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	db.SetMaxOpenConns(options.MaxOpenConns)
	db.SetMaxIdleConns(options.MaxIdleConns)
	db.SetConnMaxLifetime(options.ConnMaxLifetime)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return db, nil
}

// Store implements store.Store against Postgres. The cipher recovers VAPID
// private keys, which are held encrypted at rest.
type Store struct {
	db     *sql.DB
	cipher *crypto.Cipher

	applications *applications
	routing      *routing
	producers    *producers
	devices      *devices
	messages     *messages
	deliveries   *deliveries
}

// New builds a Store over an open connection.
func New(db *sql.DB, cipher *crypto.Cipher) *Store {
	store := &Store{db: db, cipher: cipher}
	store.applications = &applications{store: store}
	store.routing = &routing{store: store}
	store.producers = &producers{store: store}
	store.devices = &devices{store: store}
	store.messages = &messages{store: store}
	store.deliveries = &deliveries{store: store}
	return store
}

func (s *Store) Applications() store.Applications { return s.applications }
func (s *Store) Routing() store.Routing           { return s.routing }
func (s *Store) Producers() store.Producers       { return s.producers }
func (s *Store) Devices() store.Devices           { return s.devices }
func (s *Store) Messages() store.Messages         { return s.messages }
func (s *Store) Deliveries() store.Deliveries     { return s.deliveries }
func (s *Store) Administration() store.Administration {
	return NewAdmin(s)
}

// inTransaction runs fn in a transaction, rolling back on error or panic.
func (s *Store) inTransaction(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			_ = tx.Rollback()
			panic(recovered)
		}
	}()

	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
