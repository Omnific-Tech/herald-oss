BEGIN;

-- Herald issues install_id now, so it is never a value an attacker can guess
-- to have a device token minted for someone else's device.
ALTER TABLE devices ALTER COLUMN install_id SET DEFAULT encode(gen_random_bytes(16), 'hex');

-- A push service reporting the subscription gone must not destroy the device,
-- its sequence, or its delivery history.
ALTER TABLE devices ADD COLUMN webpush_disabled_at timestamptz;
ALTER TABLE devices DROP CONSTRAINT devices_webpush_required;
ALTER TABLE devices ADD CONSTRAINT devices_webpush_required CHECK (
    platform <> 'web'
    OR webpush_disabled_at IS NOT NULL
    OR (webpush_endpoint IS NOT NULL AND webpush_p256dh IS NOT NULL AND webpush_auth IS NOT NULL)
);

-- Replay reads the last acknowledged sequence on every registration.
CREATE INDEX deliveries_acknowledged_idx ON deliveries (device_id, sequence)
    WHERE state = 'acknowledged';

-- device_count must mean the same thing on a first publish and on a replay.
ALTER TABLE messages ADD COLUMN device_count integer NOT NULL DEFAULT 0;

-- next_attempt_at is now the Web Push queue marker alone. Android deliveries
-- leave it NULL so they stay out of the dispatch index entirely; the old index
-- scanned every unacknowledged Android delivery on every dispatch tick.
UPDATE deliveries d SET next_attempt_at = NULL
FROM devices dev
WHERE dev.id = d.device_id AND dev.platform <> 'web';

DROP INDEX deliveries_dispatch_idx;
CREATE INDEX deliveries_dispatch_idx ON deliveries (next_attempt_at)
    WHERE state = 'pending' AND next_attempt_at IS NOT NULL;

COMMIT;

BEGIN;

-- "//host" and "/\host" also start with "/" and a browser resolves both to a
-- different origin, so the first-character check was not an origin control.
ALTER TABLE messages DROP CONSTRAINT messages_link_check;
ALTER TABLE messages ADD CONSTRAINT messages_link_check
    CHECK (link = '' OR link ~ '^/([^/\\].*)?$');

COMMIT;
