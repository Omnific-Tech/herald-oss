BEGIN;

-- A producer name identifies one active credential. Revoked history remains,
-- and the name may be reused after revocation.
CREATE UNIQUE INDEX producers_active_name_unique
    ON producers (application_id, name)
    WHERE revoked_at IS NULL;

-- Browser sessions contain only a hash of the opaque cookie value.
CREATE TABLE admin_sessions (
    token_hash  text PRIMARY KEY,
    username    text NOT NULL,
    expires_at  timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX admin_sessions_expiry_idx ON admin_sessions (expires_at);

COMMIT;
