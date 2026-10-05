BEGIN;

CREATE INDEX messages_history_idx
    ON messages (application_id, created_at DESC, id DESC);

COMMIT;
