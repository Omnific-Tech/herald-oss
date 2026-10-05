-- Herald version 1 schema.

BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- A product. VAPID keys are per application, so rotating one product's key
-- cannot invalidate another product's browser subscriptions.
CREATE TABLE applications (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug               text NOT NULL UNIQUE,
    display_name       text NOT NULL,
    -- Trusted origin. Notification links are resolved against it.
    origin             text NOT NULL,
    vapid_public_key   text NOT NULL,
    vapid_private_key  text NOT NULL,
    vapid_subject      text NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

-- An authenticated publisher, bound to exactly one application.
CREATE TABLE producers (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id  uuid NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    name            text NOT NULL,
    token_hash      text NOT NULL UNIQUE,
    revoked_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX producers_application_idx ON producers (application_id);

-- One installed client of one application.
CREATE TABLE devices (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id     uuid NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    platform           text NOT NULL CHECK (platform IN ('android', 'web')),
    -- Stable client-generated identifier. Re-registering rotates the token and
    -- keeps the delivery history.
    install_id         text NOT NULL,
    display_name       text NOT NULL DEFAULT '',
    token_hash         text NOT NULL UNIQUE,
    -- Web Push subscription, required for platform 'web' only.
    webpush_endpoint   text,
    webpush_p256dh     text,
    webpush_auth       text,
    -- Highest sequence allocated to this device. Allocation happens inside the
    -- publish transaction.
    last_sequence      bigint NOT NULL DEFAULT 0,
    last_seen_at       timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (application_id, install_id),
    CONSTRAINT devices_webpush_required CHECK (
        platform <> 'web'
        OR (webpush_endpoint IS NOT NULL AND webpush_p256dh IS NOT NULL AND webpush_auth IS NOT NULL)
    )
);

CREATE INDEX devices_application_idx ON devices (application_id);

-- Routing. An empty topic subscribes the device to the whole channel.
CREATE TABLE subscriptions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id   uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    channel     text NOT NULL,
    topic       text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (device_id, channel, topic)
);

CREATE INDEX subscriptions_routing_idx ON subscriptions (channel, topic);

-- A stored publication.
CREATE TABLE messages (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id   uuid NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    producer_id      uuid NOT NULL REFERENCES producers (id) ON DELETE RESTRICT,
    idempotency_key  text NOT NULL,
    channel          text NOT NULL,
    topic            text NOT NULL DEFAULT '',
    priority         text NOT NULL DEFAULT 'normal' CHECK (priority IN ('normal', 'high')),
    title            text NOT NULL,
    body             text NOT NULL,
    -- Path on the application's own origin. Must start with '/'.
    link             text NOT NULL DEFAULT '' CHECK (link = '' OR link LIKE '/%'),
    data             jsonb NOT NULL DEFAULT '{}'::jsonb,
    collapse_key     text NOT NULL DEFAULT '',
    -- After this instant the message is recorded but never displayed.
    expires_at       timestamptz NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    -- Producer retries with the same key create no additional deliveries.
    UNIQUE (application_id, idempotency_key)
);

CREATE INDEX messages_expiry_idx ON messages (expires_at);

-- One message addressed to one device.
CREATE TABLE deliveries (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id       uuid NOT NULL REFERENCES messages (id) ON DELETE CASCADE,
    device_id        uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    sequence         bigint NOT NULL,
    state            text NOT NULL DEFAULT 'pending'
                     CHECK (state IN ('pending', 'sent', 'acknowledged', 'expired', 'failed')),
    attempts         integer NOT NULL DEFAULT 0,
    last_attempt_at  timestamptz,
    next_attempt_at  timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    -- Fan-out is idempotent per device.
    UNIQUE (message_id, device_id),
    -- Sequence is monotonic per device.
    UNIQUE (device_id, sequence)
);

-- The replay query: everything a device has not acknowledged, in order.
CREATE INDEX deliveries_replay_idx ON deliveries (device_id, sequence)
    WHERE state <> 'acknowledged';

-- Client confirmation. At most one per delivery, so repeating an ack is a no-op.
CREATE TABLE acknowledgements (
    delivery_id      uuid PRIMARY KEY REFERENCES deliveries (id) ON DELETE CASCADE,
    status           text NOT NULL CHECK (status IN ('delivered', 'displayed', 'suppressed')),
    acknowledged_at  timestamptz NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);

COMMIT;
