-- Supersession and the Web Push dispatch queue.

BEGIN;

-- A collapse_key lets a newer message retire an older undelivered one. That
-- outcome is neither an expiry nor a failure, so it gets its own state.
ALTER TABLE deliveries DROP CONSTRAINT deliveries_state_check;
ALTER TABLE deliveries ADD CONSTRAINT deliveries_state_check
    CHECK (state IN ('pending', 'sent', 'acknowledged', 'expired', 'failed', 'superseded'));

-- The Web Push dispatcher claims due deliveries across every device, so it
-- needs a queue-shaped index rather than the per-device replay index.
CREATE INDEX deliveries_dispatch_idx ON deliveries (next_attempt_at)
    WHERE state = 'pending';

-- Collapse supersession looks messages up by key within an application.
CREATE INDEX messages_collapse_idx ON messages (application_id, collapse_key)
    WHERE collapse_key <> '';

COMMIT;
