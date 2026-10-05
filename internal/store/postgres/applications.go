package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Omnific-Tech/herald/internal/model"
	"github.com/Omnific-Tech/herald/internal/store"
)

type applications struct {
	store *Store
}

func (a *applications) BySlug(ctx context.Context, slug string) (model.Application, error) {
	const query = `
		SELECT id, slug, display_name, origin, created_at
		FROM applications
		WHERE slug = $1`

	var application model.Application
	err := a.store.db.QueryRowContext(ctx, query, slug).Scan(
		&application.ID,
		&application.Slug,
		&application.DisplayName,
		&application.Origin,
		&application.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Application{}, store.ErrNotFound
	}
	if err != nil {
		return model.Application{}, fmt.Errorf("select application: %w", err)
	}
	return application, nil
}

func (a *applications) VAPIDPublicKey(ctx context.Context, slug string) (string, error) {
	const query = `SELECT vapid_public_key FROM applications WHERE slug = $1`

	var key string
	err := a.store.db.QueryRowContext(ctx, query, slug).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", store.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("select vapid public key: %w", err)
	}
	return key, nil
}
