package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Omnific-Tech/herald/internal/crypto"
	"github.com/Omnific-Tech/herald/internal/model"
	"github.com/Omnific-Tech/herald/internal/store"
	push "github.com/SherClockHolmes/webpush-go"
	"github.com/lib/pq"
)

// Admin holds the authenticated control-plane and break-glass CLI operations.
type Admin struct {
	store *Store
}

func NewAdmin(store *Store) *Admin { return &Admin{store: store} }

// CreateApplication provisions a product and its own VAPID keypair. The private
// key is encrypted before it reaches the database.
func (a *Admin) CreateApplication(ctx context.Context, slug, displayName, origin, vapidSubject string) (model.Application, string, error) {
	if a.store.cipher == nil {
		return model.Application{}, "", errors.New("an encryption key is required to store a VAPID private key")
	}

	privateKey, publicKey, err := push.GenerateVAPIDKeys()
	if err != nil {
		return model.Application{}, "", fmt.Errorf("generate vapid keys: %w", err)
	}

	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Application{}, "", fmt.Errorf("begin application transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// The row id is the ciphertext's associated data, so the application is
	// inserted first and its key written once the id exists. The transaction
	// prevents a failed encryption/update from leaving an unusable row.
	const insert = `
		INSERT INTO applications (slug, display_name, origin, vapid_public_key, vapid_private_key, vapid_subject)
		VALUES ($1, $2, $3, $4, '', $5)
		RETURNING id, slug, display_name, origin, created_at`

	var application model.Application
	err = tx.QueryRowContext(ctx, insert, slug, displayName, origin, publicKey, vapidSubject).Scan(
		&application.ID,
		&application.Slug,
		&application.DisplayName,
		&application.Origin,
		&application.CreatedAt,
	)
	if uniqueViolation(err, "applications_slug_key") {
		return model.Application{}, "", store.ErrConflict
	}
	if err != nil {
		return model.Application{}, "", fmt.Errorf("insert application: %w", err)
	}

	encryptedPrivateKey, err := a.store.cipher.Encrypt(privateKey, application.ID)
	if err != nil {
		return model.Application{}, "", err
	}
	const store = `UPDATE applications SET vapid_private_key = $2 WHERE id = $1`
	if _, err := tx.ExecContext(ctx, store, application.ID, encryptedPrivateKey); err != nil {
		return model.Application{}, "", fmt.Errorf("store vapid private key: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return model.Application{}, "", fmt.Errorf("commit application: %w", err)
	}

	return application, publicKey, nil
}

// CreateProducer mints a publisher token. The plaintext is returned once and
// never stored.
func (a *Admin) CreateProducer(ctx context.Context, applicationSlug, name string) (model.Producer, string, error) {
	token, err := crypto.NewToken()
	if err != nil {
		return model.Producer{}, "", err
	}

	const query = `
		INSERT INTO producers (application_id, name, token_hash)
		SELECT id, $2, $3 FROM applications WHERE slug = $1
		RETURNING id, application_id, name, revoked_at, created_at`

	var producer model.Producer
	err = a.store.db.QueryRowContext(ctx, query, applicationSlug, name, crypto.HashToken(token)).Scan(
		&producer.ID,
		&producer.ApplicationID,
		&producer.Name,
		&producer.RevokedAt,
		&producer.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Producer{}, "", store.ErrNotFound
	}
	if uniqueViolation(err, "producers_active_name_unique") {
		return model.Producer{}, "", store.ErrConflict
	}
	if err != nil {
		return model.Producer{}, "", fmt.Errorf("insert producer: %w", err)
	}

	return producer, token, nil
}

// ListApplications returns every provisioned product.
func (a *Admin) ListApplications(ctx context.Context) ([]model.Application, error) {
	const query = `SELECT id, slug, display_name, origin, created_at FROM applications ORDER BY slug`

	rows, err := a.store.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("select applications: %w", err)
	}
	defer func() { _ = rows.Close() }()

	applications := []model.Application{}
	for rows.Next() {
		var application model.Application
		if err := rows.Scan(&application.ID, &application.Slug, &application.DisplayName, &application.Origin, &application.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan application: %w", err)
		}
		applications = append(applications, application)
	}
	return applications, rows.Err()
}

// ListApplicationProducers returns active and revoked credentials for one app.
func (a *Admin) ListApplicationProducers(ctx context.Context, applicationSlug string) ([]model.Producer, error) {
	var applicationID string
	if err := a.store.db.QueryRowContext(ctx, `SELECT id FROM applications WHERE slug = $1`, applicationSlug).Scan(&applicationID); errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("select application: %w", err)
	}

	const query = `
		SELECT id, application_id, name, revoked_at, created_at
		FROM producers
		WHERE application_id = $1
		ORDER BY created_at DESC, id`
	rows, err := a.store.db.QueryContext(ctx, query, applicationID)
	if err != nil {
		return nil, fmt.Errorf("select application producers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	producers := []model.Producer{}
	for rows.Next() {
		var producer model.Producer
		if err := rows.Scan(&producer.ID, &producer.ApplicationID, &producer.Name, &producer.RevokedAt, &producer.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan application producer: %w", err)
		}
		producers = append(producers, producer)
	}
	return producers, rows.Err()
}

// RevokeProducerByID revokes exactly one immutable producer identity.
func (a *Admin) RevokeProducerByID(ctx context.Context, applicationSlug, producerID string) (model.Producer, error) {
	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Producer{}, fmt.Errorf("begin producer revocation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const selectQuery = `
		SELECT p.id, p.application_id, p.name, p.revoked_at, p.created_at
		FROM producers p
		JOIN applications app ON app.id = p.application_id
		WHERE app.slug = $1 AND p.id = $2
		FOR UPDATE`
	var producer model.Producer
	err = tx.QueryRowContext(ctx, selectQuery, applicationSlug, producerID).Scan(
		&producer.ID, &producer.ApplicationID, &producer.Name, &producer.RevokedAt, &producer.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Producer{}, store.ErrNotFound
	}
	if err != nil {
		return model.Producer{}, fmt.Errorf("select producer for revocation: %w", err)
	}
	if producer.RevokedAt != nil {
		return model.Producer{}, store.ErrAlreadyRevoked
	}

	const update = `
		UPDATE producers SET revoked_at = now() WHERE id = $1
		RETURNING revoked_at`
	if err := tx.QueryRowContext(ctx, update, producer.ID).Scan(&producer.RevokedAt); err != nil {
		return model.Producer{}, fmt.Errorf("revoke producer by id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return model.Producer{}, fmt.Errorf("commit producer revocation: %w", err)
	}
	return producer, nil
}

// RevokeProducer disables a token without deleting the producer, so published
// messages keep a valid producer_id.
func (a *Admin) RevokeProducer(ctx context.Context, applicationSlug, name string) error {
	const query = `
		UPDATE producers p
		SET revoked_at = now()
		FROM applications app
		WHERE app.id = p.application_id
		  AND app.slug = $1
		  AND p.name = $2
		  AND p.revoked_at IS NULL`

	result, err := a.store.db.ExecContext(ctx, query, applicationSlug, name)
	if err != nil {
		return fmt.Errorf("revoke producer: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoked rows: %w", err)
	}
	if affected == 0 {
		return store.ErrNotFound
	}
	return nil
}

// ProducerSummary is one row of `heraldctl list-producers`.
type ProducerSummary struct {
	Application string
	Name        string
	Revoked     bool
	CreatedAt   time.Time
}

func (a *Admin) ListProducers(ctx context.Context) ([]ProducerSummary, error) {
	const query = `
		SELECT app.slug, p.name, p.revoked_at IS NOT NULL, p.created_at
		FROM producers p
		JOIN applications app ON app.id = p.application_id
		ORDER BY app.slug, p.name`

	rows, err := a.store.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("select producers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	summaries := []ProducerSummary{}
	for rows.Next() {
		var summary ProducerSummary
		if err := rows.Scan(&summary.Application, &summary.Name, &summary.Revoked, &summary.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan producer: %w", err)
		}
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}

// RewrapVAPIDKeys re-encrypts every application key that was written with a
// retired encryption key. It is how ENCRYPTION_KEY is rotated without losing
// the VAPID private keys, and it is safe to re-run.
func (a *Admin) RewrapVAPIDKeys(ctx context.Context) (int, error) {
	if a.store.cipher == nil {
		return 0, errors.New("an encryption key is required to rewrap")
	}

	rows, err := a.store.db.QueryContext(ctx, `SELECT id, slug, vapid_private_key FROM applications ORDER BY slug`)
	if err != nil {
		return 0, fmt.Errorf("select applications: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type pending struct{ id, slug, ciphertext string }
	candidates := []pending{}
	for rows.Next() {
		var candidate pending
		if err := rows.Scan(&candidate.id, &candidate.slug, &candidate.ciphertext); err != nil {
			return 0, fmt.Errorf("scan application: %w", err)
		}
		if a.store.cipher.NeedsRewrap(candidate.ciphertext) {
			candidates = append(candidates, candidate)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	rewrapped := 0
	for _, candidate := range candidates {
		privateKey, err := a.store.cipher.Decrypt(candidate.ciphertext, candidate.id)
		if err != nil {
			return rewrapped, fmt.Errorf("application %s: %w", candidate.slug, err)
		}
		reencrypted, err := a.store.cipher.Encrypt(privateKey, candidate.id)
		if err != nil {
			return rewrapped, err
		}
		if _, err := a.store.db.ExecContext(ctx, `UPDATE applications SET vapid_private_key = $2, updated_at = now() WHERE id = $1`, candidate.id, reencrypted); err != nil {
			return rewrapped, fmt.Errorf("application %s: %w", candidate.slug, err)
		}
		rewrapped++
	}

	return rewrapped, nil
}

func uniqueViolation(err error, constraint string) bool {
	var databaseError *pq.Error
	return errors.As(err, &databaseError) && string(databaseError.Code) == "23505" && databaseError.Constraint == constraint
}
