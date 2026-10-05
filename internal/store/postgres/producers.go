package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Omnific-Tech/herald/internal/crypto"
	"github.com/Omnific-Tech/herald/internal/model"
	"github.com/Omnific-Tech/herald/internal/store"
)

type producers struct {
	store *Store
}

// ByToken looks the producer up by token hash. The application_id it returns is
// the only application the caller may publish for.
func (p *producers) ByToken(ctx context.Context, token string) (model.Producer, error) {
	const query = `
		SELECT id, application_id, name, revoked_at, created_at
		FROM producers
		WHERE token_hash = $1 AND revoked_at IS NULL`

	var producer model.Producer
	err := p.store.db.QueryRowContext(ctx, query, crypto.HashToken(token)).Scan(
		&producer.ID,
		&producer.ApplicationID,
		&producer.Name,
		&producer.RevokedAt,
		&producer.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Producer{}, store.ErrNotFound
	}
	if err != nil {
		return model.Producer{}, fmt.Errorf("select producer: %w", err)
	}
	return producer, nil
}
