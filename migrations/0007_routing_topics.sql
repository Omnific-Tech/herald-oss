BEGIN;

CREATE TABLE routing_topics (
    application_id uuid NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    id             text NOT NULL,
    label          text NOT NULL,
    channel        text NOT NULL CHECK (channel IN ('alerts', 'system')),
    archived_at    timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (application_id, id)
);

-- Preserve any existing topic subscriptions and published topic IDs so the
-- catalog can be introduced without invalidating already configured clients.
INSERT INTO routing_topics (application_id, id, label, channel)
SELECT application_id, topic, topic, min(channel)
FROM (
    SELECT d.application_id, s.topic, s.channel
    FROM subscriptions s JOIN devices d ON d.id = s.device_id
    WHERE s.topic <> '' AND s.channel IN ('alerts', 'system')
    UNION ALL
    SELECT application_id, topic, channel FROM messages
    WHERE topic <> '' AND channel IN ('alerts', 'system')
) existing
GROUP BY application_id, topic;

COMMIT;
