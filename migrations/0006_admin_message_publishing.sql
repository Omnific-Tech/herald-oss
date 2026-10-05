BEGIN;

ALTER TABLE messages
    ALTER COLUMN producer_id DROP NOT NULL,
    ADD COLUMN admin_username text,
    ADD CONSTRAINT messages_exactly_one_publisher CHECK (
        (producer_id IS NOT NULL AND admin_username IS NULL)
        OR (producer_id IS NULL AND admin_username IS NOT NULL AND admin_username <> '')
    );

COMMIT;
