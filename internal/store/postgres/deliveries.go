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

type deliveries struct {
	store *Store
}

// Pending is the replay query. afterSequence is the last sequence the client
// acknowledged, so anything it merely received is returned again.
func (d *deliveries) Pending(ctx context.Context, deviceID string, afterSequence int64, limit int) ([]model.StreamEvent, error) {
	query := `
		SELECT d.id, d.sequence,` + messageColumns + `
		FROM deliveries d
		JOIN messages m ON m.id = d.message_id
		WHERE d.device_id = $1
		  AND d.sequence > $2
		  AND d.state IN ('pending', 'sent')
		  AND m.expires_at > now()
		ORDER BY d.sequence
		LIMIT $3`

	rows, err := d.store.db.QueryContext(ctx, query, deviceID, afterSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("select pending deliveries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	events := []model.StreamEvent{}
	for rows.Next() {
		var event model.StreamEvent
		if err := scanStreamEvent(rows, &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (d *deliveries) MarkSent(ctx context.Context, deliveryID string) error {
	const query = `
		UPDATE deliveries
		SET state = 'sent', last_attempt_at = now(), last_error = NULL
		WHERE id = $1 AND state = 'pending'`

	if _, err := d.store.db.ExecContext(ctx, query, deliveryID); err != nil {
		return fmt.Errorf("mark delivery sent: %w", err)
	}
	return nil
}

// Acknowledge is idempotent: the acknowledgements primary key is the delivery,
// so a repeated ack keeps the first recorded status and returns it.
func (d *deliveries) Acknowledge(ctx context.Context, deviceID, deliveryID string, status model.AckStatus) (model.Acknowledgement, error) {
	var acknowledgement model.Acknowledgement

	err := d.store.inTransaction(ctx, func(tx *sql.Tx) error {
		const owned = `SELECT id FROM deliveries WHERE id = $1 AND device_id = $2 FOR UPDATE`
		var id string
		err := tx.QueryRowContext(ctx, owned, deliveryID, deviceID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return store.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("select delivery: %w", err)
		}

		const insert = `
			INSERT INTO acknowledgements (delivery_id, status, acknowledged_at)
			VALUES ($1, $2, now())
			ON CONFLICT (delivery_id) DO NOTHING`
		if _, err := tx.ExecContext(ctx, insert, id, status); err != nil {
			return fmt.Errorf("insert acknowledgement: %w", err)
		}

		const mark = `UPDATE deliveries SET state = 'acknowledged', last_error = NULL WHERE id = $1`
		if _, err := tx.ExecContext(ctx, mark, id); err != nil {
			return fmt.Errorf("mark delivery acknowledged: %w", err)
		}

		const read = `SELECT delivery_id, status, acknowledged_at FROM acknowledgements WHERE delivery_id = $1`
		if err := tx.QueryRowContext(ctx, read, id).Scan(
			&acknowledgement.DeliveryID,
			&acknowledgement.Status,
			&acknowledgement.AcknowledgedAt,
		); err != nil {
			return fmt.Errorf("select acknowledgement: %w", err)
		}
		return nil
	})
	if err != nil {
		return model.Acknowledgement{}, err
	}
	return acknowledgement, nil
}

// ClaimWebPush leases due Web Push work. SKIP LOCKED plus pushing
// next_attempt_at forward is what keeps two dispatchers from sending twice.
func (d *deliveries) ClaimWebPush(ctx context.Context, limit int, lease time.Duration) ([]store.WebPushJob, error) {
	if d.store.cipher == nil {
		return nil, errors.New("web push requires an encryption key to recover VAPID private keys")
	}

	query := `
		WITH claimed AS (
			SELECT d.id, d.device_id, d.message_id, d.sequence
			FROM deliveries d
			JOIN devices dev ON dev.id = d.device_id
			JOIN messages m ON m.id = d.message_id
			WHERE d.state = 'pending'
			  AND dev.platform = 'web'
			  AND dev.webpush_disabled_at IS NULL
			  AND d.next_attempt_at <= now()
			  AND m.expires_at > now()
			ORDER BY d.next_attempt_at
			LIMIT $1
			FOR UPDATE OF d SKIP LOCKED
		),
		leased AS (
			UPDATE deliveries d
			SET attempts = d.attempts + 1,
			    last_attempt_at = now(),
			    next_attempt_at = now() + make_interval(secs => $2)
			FROM claimed c
			WHERE d.id = c.id
			RETURNING d.id, d.device_id, d.message_id, d.sequence, d.attempts
		)
		SELECT l.id, l.device_id, l.sequence, l.attempts,
		       dev.webpush_endpoint, dev.webpush_p256dh, dev.webpush_auth,
		       a.vapid_public_key, a.vapid_private_key, a.vapid_subject,
		       greatest(0, floor(extract(epoch FROM m.expires_at - now())))::int,` + messageColumns + `
		FROM leased l
		JOIN devices dev ON dev.id = l.device_id
		JOIN messages m ON m.id = l.message_id
		JOIN applications a ON a.id = m.application_id
		ORDER BY l.id`

	rows, err := d.store.db.QueryContext(ctx, query, limit, lease.Seconds())
	if err != nil {
		return nil, fmt.Errorf("claim web push deliveries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	jobs := []store.WebPushJob{}
	for rows.Next() {
		var (
			job                          store.WebPushJob
			endpoint, p256dh, auth       sql.NullString
			topic, link, collapseKey     sql.NullString
			data                         []byte
			encryptedVAPIDPrivateKeyText string
		)

		err := rows.Scan(
			&job.DeliveryID, &job.DeviceID, &job.Sequence, &job.Attempts,
			&endpoint, &p256dh, &auth,
			&job.VAPIDPublicKey, &encryptedVAPIDPrivateKeyText, &job.VAPIDSubject,
			&job.TTLSeconds,
			&job.Message.ID, &job.Message.ApplicationID, &job.Message.ProducerID,
			&job.Message.AdminUsername,
			&job.Message.IdempotencyKey, &job.Message.Channel, &topic,
			&job.Message.Priority, &job.Message.Title, &job.Message.Body, &link,
			&data, &collapseKey, &job.Message.DeviceCount,
			&job.Message.ExpiresAt, &job.Message.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan web push job: %w", err)
		}

		// The lease is already committed, so a key this process cannot read must
		// travel with the job. Returning an error here would throw away every
		// other delivery in the batch while leaving all of them leased.
		privateKey, err := d.store.cipher.Decrypt(encryptedVAPIDPrivateKeyText, job.Message.ApplicationID)
		if err != nil {
			job.DecryptErr = fmt.Errorf("decrypt vapid private key: %w", err)
		}

		job.Endpoint, job.P256DH, job.Auth = endpoint.String, p256dh.String, auth.String
		job.VAPIDPrivateKey = privateKey
		job.Message.Topic, job.Message.Link, job.Message.CollapseKey = topic.String, link.String, collapseKey.String
		job.Message.Data = append([]byte(nil), data...)
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// ClaimFCM leases due Android deliveries and computes TTL from the database
// clock in the same statement that claims them.
func (d *deliveries) ClaimFCM(ctx context.Context, limit int, lease time.Duration) ([]store.FCMJob, error) {
	query := `
		WITH claimed AS (
			SELECT d.id, d.device_id, d.message_id, d.sequence
			FROM deliveries d
			JOIN devices dev ON dev.id = d.device_id
			JOIN messages m ON m.id = d.message_id
			WHERE d.state = 'pending'
			  AND dev.platform = 'android'
			  AND dev.fcm_fid IS NOT NULL
			  AND dev.fcm_disabled_at IS NULL
			  AND d.next_attempt_at <= now()
			  AND m.expires_at > now()
			ORDER BY d.next_attempt_at
			LIMIT $1
			FOR UPDATE OF d SKIP LOCKED
		),
		leased AS (
			UPDATE deliveries d
			SET attempts = d.attempts + 1,
			    last_attempt_at = now(),
			    next_attempt_at = now() + make_interval(secs => $2)
			FROM claimed c
			WHERE d.id = c.id
			RETURNING d.id, d.device_id, d.message_id, d.sequence, d.attempts
		)
		SELECT l.id, l.device_id, l.sequence, l.attempts, dev.fcm_fid,
		       greatest(0, floor(extract(epoch FROM m.expires_at - now())))::int,` + messageColumns + `
		FROM leased l
		JOIN devices dev ON dev.id = l.device_id
		JOIN messages m ON m.id = l.message_id
		ORDER BY l.id`

	rows, err := d.store.db.QueryContext(ctx, query, limit, lease.Seconds())
	if err != nil {
		return nil, fmt.Errorf("claim fcm deliveries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	jobs := []store.FCMJob{}
	for rows.Next() {
		var (
			job                      store.FCMJob
			topic, link, collapseKey sql.NullString
			data                     []byte
		)
		if err := rows.Scan(
			&job.DeliveryID, &job.DeviceID, &job.Sequence, &job.Attempts, &job.FCMFID,
			&job.TTLSeconds,
			&job.Message.ID, &job.Message.ApplicationID, &job.Message.ProducerID,
			&job.Message.AdminUsername,
			&job.Message.IdempotencyKey, &job.Message.Channel, &topic,
			&job.Message.Priority, &job.Message.Title, &job.Message.Body, &link,
			&data, &collapseKey, &job.Message.DeviceCount,
			&job.Message.ExpiresAt, &job.Message.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan fcm job: %w", err)
		}
		job.Message.Topic, job.Message.Link, job.Message.CollapseKey = topic.String, link.String, collapseKey.String
		job.Message.Data = append([]byte(nil), data...)
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (d *deliveries) Retry(ctx context.Context, deliveryID string, delay time.Duration, reason string) error {
	const query = `UPDATE deliveries SET next_attempt_at = now() + make_interval(secs => $2), last_error = NULLIF(left($3, 512), '') WHERE id = $1 AND state = 'pending'`
	if _, err := d.store.db.ExecContext(ctx, query, deliveryID, delay.Seconds(), reason); err != nil {
		return fmt.Errorf("reschedule delivery: %w", err)
	}
	return nil
}

// Fail only moves a delivery that is still pending: an ack may have landed
// while the dispatcher was mid-send, and that record wins.
func (d *deliveries) Fail(ctx context.Context, deliveryID string, reason string) error {
	const query = `UPDATE deliveries SET state = 'failed', last_attempt_at = now(), last_error = NULLIF(left($2, 512), '') WHERE id = $1 AND state = 'pending'`
	if _, err := d.store.db.ExecContext(ctx, query, deliveryID, reason); err != nil {
		return fmt.Errorf("fail delivery: %w", err)
	}
	return nil
}

// ExpireOverdue records expiry rather than deleting, so a stale alert is
// auditable but never surfaced on reconnect.
//
// It works in ordered, locked batches: one unbounded UPDATE would hold locks
// across the whole table and can deadlock against a concurrent publish taking
// the same delivery locks in scan order.
func (d *deliveries) ExpireOverdue(ctx context.Context) (int, error) {
	const query = `
		WITH overdue AS (
			SELECT d.id
			FROM deliveries d
			JOIN messages m ON m.id = d.message_id
			WHERE d.state IN ('pending', 'sent')
			  AND m.expires_at <= now()
			ORDER BY d.id
			LIMIT $1
			FOR UPDATE OF d SKIP LOCKED
		)
		UPDATE deliveries d
		SET state = 'expired'
		FROM overdue
		WHERE d.id = overdue.id`

	const batchSize = 1000

	total := 0
	for {
		result, err := d.store.db.ExecContext(ctx, query, batchSize)
		if err != nil {
			return total, fmt.Errorf("expire deliveries: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("expired rows: %w", err)
		}

		total += int(affected)
		if int(affected) < batchSize {
			return total, nil
		}
	}
}

// FailUndecryptable retires a delivery whose application VAPID key this process
// cannot read. Without it the row is re-claimed every lease forever, inflating
// attempts and starving healthy deliveries claimed in the same batch.
func (d *deliveries) FailUndecryptable(ctx context.Context, deliveryID string) error {
	return d.Fail(ctx, deliveryID, "VAPID key unavailable")
}

func scanStreamEvent(row scanner, event *model.StreamEvent) error {
	var (
		message                  model.Message
		topic, link, collapseKey sql.NullString
		data                     []byte
	)

	err := row.Scan(
		&event.DeliveryID,
		&event.Sequence,
		&message.ID,
		&message.ApplicationID,
		&message.ProducerID,
		&message.AdminUsername,
		&message.IdempotencyKey,
		&message.Channel,
		&topic,
		&message.Priority,
		&message.Title,
		&message.Body,
		&link,
		&data,
		&collapseKey,
		&message.DeviceCount,
		&message.ExpiresAt,
		&message.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("scan stream event: %w", err)
	}

	message.Topic = topic.String
	message.Link = link.String
	message.CollapseKey = collapseKey.String
	message.Data = append([]byte(nil), data...)
	event.Message = message.Delivery()
	return nil
}
