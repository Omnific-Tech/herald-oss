package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Omnific-Tech/herald/internal/model"
	"github.com/Omnific-Tech/herald/internal/store"
)

type routing struct{ store *Store }

func (r *routing) ListTopics(ctx context.Context, applicationID string) ([]model.RoutingTopic, error) {
	rows, err := r.store.db.QueryContext(ctx, `
		SELECT id, label, channel, archived_at
		FROM routing_topics WHERE application_id = $1 ORDER BY channel, id`, applicationID)
	if err != nil {
		return nil, fmt.Errorf("list topics: %w", err)
	}
	defer func() { _ = rows.Close() }()
	topics := []model.RoutingTopic{}
	for rows.Next() {
		var topic model.RoutingTopic
		if err := rows.Scan(&topic.ID, &topic.Label, &topic.Channel, &topic.ArchivedAt); err != nil {
			return nil, fmt.Errorf("scan topic: %w", err)
		}
		topics = append(topics, topic)
	}
	return topics, rows.Err()
}

func (r *routing) CreateTopic(ctx context.Context, applicationSlug string, topic model.RoutingTopic) (model.RoutingTopic, error) {
	const query = `
		INSERT INTO routing_topics (application_id, id, label, channel)
		SELECT id, $2, $3, $4 FROM applications WHERE slug = $1
		RETURNING id, label, channel, archived_at`
	err := r.store.db.QueryRowContext(ctx, query, applicationSlug, topic.ID, topic.Label, topic.Channel).Scan(
		&topic.ID, &topic.Label, &topic.Channel, &topic.ArchivedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.RoutingTopic{}, store.ErrNotFound
	}
	if uniqueViolation(err, "routing_topics_pkey") {
		return model.RoutingTopic{}, store.ErrConflict
	}
	if err != nil {
		return model.RoutingTopic{}, fmt.Errorf("create topic: %w", err)
	}
	return topic, nil
}

func (r *routing) RenameTopic(ctx context.Context, applicationSlug, topicID, label string) (model.RoutingTopic, error) {
	var topic model.RoutingTopic
	err := r.store.inTransaction(ctx, func(tx *sql.Tx) error {
		const selectTopic = `
			SELECT t.id, t.label, t.channel, t.archived_at
			FROM routing_topics t JOIN applications a ON a.id = t.application_id
			WHERE a.slug = $1 AND t.id = $2 FOR UPDATE OF t`
		err := tx.QueryRowContext(ctx, selectTopic, applicationSlug, topicID).Scan(
			&topic.ID, &topic.Label, &topic.Channel, &topic.ArchivedAt,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return store.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("select topic for rename: %w", err)
		}
		if topic.ArchivedAt != nil {
			return store.ErrAlreadyArchived
		}
		return tx.QueryRowContext(ctx, `
			UPDATE routing_topics SET label = $3
			WHERE application_id = (SELECT id FROM applications WHERE slug = $1) AND id = $2
			RETURNING label`, applicationSlug, topicID, label).Scan(&topic.Label)
	})
	if err != nil {
		return model.RoutingTopic{}, fmt.Errorf("rename topic: %w", err)
	}
	return topic, nil
}

func (r *routing) ArchiveTopic(ctx context.Context, applicationSlug, topicID string) (model.RoutingTopic, error) {
	var result model.RoutingTopic
	err := r.store.inTransaction(ctx, func(tx *sql.Tx) error {
		const selectTopic = `
			SELECT t.id, t.label, t.channel, t.archived_at
			FROM routing_topics t JOIN applications a ON a.id = t.application_id
			WHERE a.slug = $1 AND t.id = $2 FOR UPDATE OF t`
		err := tx.QueryRowContext(ctx, selectTopic, applicationSlug, topicID).Scan(
			&result.ID, &result.Label, &result.Channel, &result.ArchivedAt,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return store.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("select topic for archive: %w", err)
		}
		if result.ArchivedAt != nil {
			return store.ErrAlreadyArchived
		}
		return tx.QueryRowContext(ctx, `
			UPDATE routing_topics SET archived_at = now()
			WHERE application_id = (SELECT id FROM applications WHERE slug = $1) AND id = $2
			RETURNING archived_at`, applicationSlug, topicID).Scan(&result.ArchivedAt)
	})
	if err != nil {
		return model.RoutingTopic{}, err
	}
	return result, nil
}

// validRouting is called inside publication/subscription transactions, so
// archiving cannot race past a new delivery or subscription.
func validRouting(ctx context.Context, tx *sql.Tx, applicationID, channel, topic string) error {
	if !model.ValidChannel(channel) {
		return store.ErrInvalidChannel
	}
	if topic == "" {
		return nil
	}
	var id string
	err := tx.QueryRowContext(ctx, `
		SELECT id FROM routing_topics
		WHERE application_id = $1 AND channel = $2 AND id = $3 AND archived_at IS NULL
		FOR SHARE`, applicationID, channel, topic).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrInvalidTopic
	}
	if err != nil {
		return fmt.Errorf("validate topic: %w", err)
	}
	return nil
}
