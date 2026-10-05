package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Omnific-Tech/herald/internal/model"
	"github.com/lib/pq"
)

type messages struct {
	store *Store
}

const messageColumns = `
	m.id, m.application_id, COALESCE(m.producer_id::text, ''), COALESCE(m.admin_username, ''),
	m.idempotency_key, m.channel, m.topic,
	m.priority, m.title, m.body, m.link, m.data, m.collapse_key, m.device_count,
	m.expires_at, m.created_at`

// Publish stores the message and fans it out in one transaction.
func (m *messages) Publish(ctx context.Context, producer model.Producer, request model.PublishRequest) (model.Message, bool, int, error) {
	return m.publish(ctx, producer.ApplicationID, producer.ID, "", request)
}

func (m *messages) PublishAsAdmin(ctx context.Context, application model.Application, username string, request model.PublishRequest) (model.Message, bool, int, error) {
	return m.publish(ctx, application.ID, "", username, request)
}

func (m *messages) publish(ctx context.Context, applicationID, producerID, adminUsername string, request model.PublishRequest) (model.Message, bool, int, error) {
	priority := request.Priority
	if priority == "" {
		priority = model.PriorityNormal
	}
	data := request.Data
	if len(data) == 0 {
		data = json.RawMessage(`{}`)
	}

	var (
		message      model.Message
		deduplicated bool
		deviceCount  int
	)

	err := m.store.inTransaction(ctx, func(tx *sql.Tx) error {
		const insert = `
			INSERT INTO messages AS m (
				application_id, producer_id, admin_username, idempotency_key, channel, topic,
				priority, title, body, link, data, collapse_key, expires_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			ON CONFLICT (application_id, idempotency_key) DO NOTHING
			RETURNING ` + messageColumns

		err := scanMessage(tx.QueryRowContext(ctx, insert,
			applicationID, nullable(producerID), nullable(adminUsername), request.IdempotencyKey,
			request.Channel, request.Topic, priority, request.Title, request.Body,
			request.Link, []byte(data), request.CollapseKey, request.ExpiresAt,
		), &message)

		// ON CONFLICT DO NOTHING with RETURNING yields no row, not an error.
		if errors.Is(err, sql.ErrNoRows) {
			deduplicated = true
			return m.loadExisting(ctx, tx, applicationID, request.IdempotencyKey, &message, &deviceCount)
		}
		if err != nil {
			return fmt.Errorf("insert message: %w", err)
		}
		if err := validRouting(ctx, tx, applicationID, request.Channel, request.Topic); err != nil {
			return err
		}

		targets, err := lockMatchingDevices(ctx, tx, applicationID, request.Channel, request.Topic)
		if err != nil {
			return err
		}

		deviceCount = len(targets)
		message.DeviceCount = deviceCount
		if err := recordDeviceCount(ctx, tx, message.ID, deviceCount); err != nil {
			return err
		}
		if deviceCount == 0 {
			return nil
		}

		deviceIDs := make([]string, len(targets))
		for index, target := range targets {
			deviceIDs[index] = target.id
		}

		if err := supersede(ctx, tx, deviceIDs, applicationID, request.CollapseKey, message.ID); err != nil {
			return err
		}

		sequences, err := allocateSequences(ctx, tx, deviceIDs)
		if err != nil {
			return err
		}

		return insertDeliveries(ctx, tx, message.ID, deviceIDs, sequences)
	})
	if err != nil {
		return model.Message{}, false, 0, err
	}

	return message, deduplicated, deviceCount, nil
}

func (m *messages) loadExisting(ctx context.Context, tx *sql.Tx, applicationID, idempotencyKey string, message *model.Message, deviceCount *int) error {
	const query = `
		SELECT ` + messageColumns + `
		FROM messages m
		WHERE m.application_id = $1 AND m.idempotency_key = $2`

	if err := scanMessage(tx.QueryRowContext(ctx, query, applicationID, idempotencyKey), message); err != nil {
		return fmt.Errorf("select existing message: %w", err)
	}

	// Read the count stored at fan-out rather than counting delivery rows, which
	// disappear with their devices and would make a replay report a smaller
	// fan-out than the original publish.
	*deviceCount = message.DeviceCount
	return nil
}

// ListHistory pages only the chosen application's messages before aggregating
// delivery outcomes. The extra row lets the HTTP layer issue a next cursor.
func (m *messages) ListHistory(ctx context.Context, applicationID string, beforeAt *time.Time, beforeID string, limit int) ([]model.AdminMessageHistory, error) {
	const query = `
		WITH page AS (
			SELECT m.id, m.producer_id, m.admin_username, m.channel, m.topic,
			       m.priority, m.title, m.body, m.link, m.device_count,
			       m.created_at, m.expires_at
			FROM messages m
			WHERE m.application_id = $1
			  AND ($2::timestamptz IS NULL OR (m.created_at, m.id) < ($2::timestamptz, $3::uuid))
			ORDER BY m.created_at DESC, m.id DESC
			LIMIT $4
		)
		SELECT page.id, page.channel, page.topic, page.priority, page.title,
		       page.body, page.link, page.device_count, page.created_at,
		       page.expires_at, COALESCE(page.admin_username, ''),
		       COALESCE(p.name, ''),
		       counts.pending, counts.sent, counts.displayed, counts.delivered,
		       counts.suppressed, counts.failed, counts.expired, counts.superseded,
		       COALESCE(errors.details, '[]'::jsonb)
		FROM page
		LEFT JOIN producers p ON p.id = page.producer_id
		CROSS JOIN LATERAL (
			SELECT count(*) FILTER (WHERE d.state = 'pending') AS pending,
			       count(*) FILTER (WHERE d.state = 'sent') AS sent,
			       count(*) FILTER (WHERE d.state = 'acknowledged' AND a.status = 'displayed') AS displayed,
			       count(*) FILTER (WHERE d.state = 'acknowledged' AND a.status = 'delivered') AS delivered,
			       count(*) FILTER (WHERE d.state = 'acknowledged' AND a.status = 'suppressed') AS suppressed,
			       count(*) FILTER (WHERE d.state = 'failed') AS failed,
			       count(*) FILTER (WHERE d.state = 'expired') AS expired,
			       count(*) FILTER (WHERE d.state = 'superseded') AS superseded
			FROM deliveries d
			LEFT JOIN acknowledgements a ON a.delivery_id = d.id
			WHERE d.message_id = page.id
		) counts
		LEFT JOIN LATERAL (
			SELECT jsonb_agg(jsonb_build_object(
				'state', ranked.state, 'reason', ranked.last_error, 'count', ranked.total
			) ORDER BY ranked.total DESC, ranked.state, ranked.last_error) AS details
			FROM (
				SELECT d.state, d.last_error, count(*) AS total
				FROM deliveries d
				WHERE d.message_id = page.id AND d.last_error IS NOT NULL
				GROUP BY d.state, d.last_error
				ORDER BY total DESC, d.state, d.last_error
				LIMIT 5
			) ranked
		) errors ON true
		ORDER BY page.created_at DESC, page.id DESC`
	var at any
	var id any
	if beforeAt != nil {
		at, id = *beforeAt, beforeID
	}
	rows, err := m.store.db.QueryContext(ctx, query, applicationID, at, id, limit)
	if err != nil {
		return nil, fmt.Errorf("list message history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	history := []model.AdminMessageHistory{}
	for rows.Next() {
		var item model.AdminMessageHistory
		var adminUsername, producerName string
		var errorDetails []byte
		if err := rows.Scan(
			&item.ID, &item.Channel, &item.Topic, &item.Priority, &item.Title,
			&item.Body, &item.Link, &item.DeviceCount, &item.CreatedAt,
			&item.ExpiresAt, &adminUsername, &producerName,
			&item.DeliveryCounts.Pending, &item.DeliveryCounts.Sent,
			&item.DeliveryCounts.Displayed, &item.DeliveryCounts.Delivered,
			&item.DeliveryCounts.Suppressed, &item.DeliveryCounts.Failed,
			&item.DeliveryCounts.Expired, &item.DeliveryCounts.Superseded,
			&errorDetails,
		); err != nil {
			return nil, fmt.Errorf("scan message history: %w", err)
		}
		if err := json.Unmarshal(errorDetails, &item.DeliveryErrors); err != nil {
			return nil, fmt.Errorf("decode delivery errors: %w", err)
		}
		item.Status = "published"
		if adminUsername != "" {
			item.Publisher = model.AdminMessagePublisher{Type: "admin", Name: adminUsername}
		} else {
			item.Publisher = model.AdminMessagePublisher{Type: "producer", Name: producerName}
		}
		history = append(history, item)
	}
	return history, rows.Err()
}

func recordDeviceCount(ctx context.Context, tx *sql.Tx, messageID string, deviceCount int) error {
	const query = `UPDATE messages SET device_count = $2 WHERE id = $1`
	if _, err := tx.ExecContext(ctx, query, messageID, deviceCount); err != nil {
		return fmt.Errorf("record device count: %w", err)
	}
	return nil
}

// deviceTarget is one fan-out destination. The platform travels with the id so
// each delivery row can be written with the right transport marker.
type deviceTarget struct {
	id       string
	platform model.Platform
}

// lockMatchingDevices locks the fan-out target set in a stable id order.
//
// The ordered lock matters: two concurrent publishes to overlapping device sets
// would otherwise take row locks in opposite orders and deadlock.
func lockMatchingDevices(ctx context.Context, tx *sql.Tx, applicationID, channel, topic string) ([]deviceTarget, error) {
	const query = `
		SELECT d.id, d.platform
		FROM devices d
		WHERE d.application_id = $1
		  AND ((d.platform = 'web' AND d.webpush_disabled_at IS NULL)
		       OR (d.platform = 'android' AND d.fcm_fid IS NOT NULL AND d.fcm_disabled_at IS NULL))
		  AND EXISTS (
			SELECT 1 FROM subscriptions s
			WHERE s.device_id = d.id
			  AND s.channel = $2
			  AND (s.topic = '' OR s.topic = $3)
		  )
		ORDER BY d.id
		FOR UPDATE OF d`

	rows, err := tx.QueryContext(ctx, query, applicationID, channel, topic)
	if err != nil {
		return nil, fmt.Errorf("select matching devices: %w", err)
	}
	defer func() { _ = rows.Close() }()

	targets := []deviceTarget{}
	for rows.Next() {
		var target deviceTarget
		if err := rows.Scan(&target.id, &target.platform); err != nil {
			return nil, fmt.Errorf("scan device: %w", err)
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

// supersede retires this device's earlier deliveries sharing the collapse key,
// including ones already transmitted but not yet acknowledged.
func supersede(ctx context.Context, tx *sql.Tx, deviceIDs []string, applicationID, collapseKey, messageID string) error {
	if collapseKey == "" {
		return nil
	}

	const query = `
		UPDATE deliveries
		SET state = 'superseded'
		WHERE device_id = ANY($1)
		  AND state IN ('pending', 'sent')
		  AND message_id IN (
			SELECT id FROM messages
			WHERE application_id = $2 AND collapse_key = $3 AND id <> $4
		  )`

	if _, err := tx.ExecContext(ctx, query, pq.Array(deviceIDs), applicationID, collapseKey, messageID); err != nil {
		return fmt.Errorf("supersede deliveries: %w", err)
	}
	return nil
}

// allocateSequences returns the new sequences in the same order as deviceIDs;
// insertDeliveries zips the two positionally.
func allocateSequences(ctx context.Context, tx *sql.Tx, deviceIDs []string) ([]int64, error) {
	const query = `
		UPDATE devices
		SET last_sequence = last_sequence + 1
		WHERE id = ANY($1)
		RETURNING id, last_sequence`

	rows, err := tx.QueryContext(ctx, query, pq.Array(deviceIDs))
	if err != nil {
		return nil, fmt.Errorf("allocate sequences: %w", err)
	}
	defer func() { _ = rows.Close() }()

	allocated := make(map[string]int64, len(deviceIDs))
	for rows.Next() {
		var (
			id       string
			sequence int64
		)
		if err := rows.Scan(&id, &sequence); err != nil {
			return nil, fmt.Errorf("scan sequence: %w", err)
		}
		allocated[id] = sequence
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sequences := make([]int64, len(deviceIDs))
	for index, id := range deviceIDs {
		sequence, ok := allocated[id]
		if !ok {
			// Unreachable: the ids came from a FOR UPDATE select in this same
			// transaction, so the UPDATE cannot have missed one.
			return nil, fmt.Errorf("no sequence allocated for device %s", id)
		}
		sequences[index] = sequence
	}
	return sequences, nil
}

// insertDeliveries writes one queued push per target.
func insertDeliveries(ctx context.Context, tx *sql.Tx, messageID string, deviceIDs []string, sequences []int64) error {
	const query = `
		INSERT INTO deliveries (message_id, device_id, sequence, next_attempt_at)
		SELECT $1, target.device_id, target.sequence, now()
		FROM unnest($2::uuid[], $3::bigint[]) AS target(device_id, sequence)`

	if _, err := tx.ExecContext(ctx, query, messageID, pq.Array(deviceIDs), pq.Array(sequences)); err != nil {
		return fmt.Errorf("insert deliveries: %w", err)
	}
	return nil
}

type scanner interface {
	Scan(destination ...any) error
}

func scanMessage(row scanner, message *model.Message) error {
	var (
		topic       sql.NullString
		link        sql.NullString
		collapseKey sql.NullString
		data        []byte
	)

	err := row.Scan(
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
		return err
	}

	message.Topic = topic.String
	message.Link = link.String
	message.CollapseKey = collapseKey.String
	message.Data = json.RawMessage(data)
	return nil
}
