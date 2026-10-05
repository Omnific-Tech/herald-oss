package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Omnific-Tech/herald/internal/model"
	"github.com/Omnific-Tech/herald/internal/store"
)

func (a *Admin) CreateSession(ctx context.Context, username, tokenHash string, ttl time.Duration) (model.AdminSession, error) {
	if _, err := a.store.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE expires_at <= now()`); err != nil {
		return model.AdminSession{}, fmt.Errorf("delete expired admin sessions: %w", err)
	}
	const query = `
		INSERT INTO admin_sessions (token_hash, username, expires_at)
		VALUES ($1, $2, now() + $3::interval)
		RETURNING username, expires_at`
	var session model.AdminSession
	if err := a.store.db.QueryRowContext(ctx, query, tokenHash, username, ttl.String()).Scan(&session.Username, &session.ExpiresAt); err != nil {
		return model.AdminSession{}, fmt.Errorf("create admin session: %w", err)
	}
	return session, nil
}

func (a *Admin) SessionByTokenHash(ctx context.Context, tokenHash string) (model.AdminSession, error) {
	const query = `
		SELECT username, expires_at
		FROM admin_sessions
		WHERE token_hash = $1 AND expires_at > now()`
	var session model.AdminSession
	if err := a.store.db.QueryRowContext(ctx, query, tokenHash).Scan(&session.Username, &session.ExpiresAt); errors.Is(err, sql.ErrNoRows) {
		return model.AdminSession{}, store.ErrNotFound
	} else if err != nil {
		return model.AdminSession{}, fmt.Errorf("select admin session: %w", err)
	}
	return session, nil
}

func (a *Admin) DeleteSession(ctx context.Context, tokenHash string) error {
	if _, err := a.store.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE token_hash = $1`, tokenHash); err != nil {
		return fmt.Errorf("delete admin session: %w", err)
	}
	return nil
}
