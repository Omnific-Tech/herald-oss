BEGIN;

-- Android delivery uses Firebase Installation IDs. A disabled installation is
-- retained so its sequence and acknowledgement history remain auditable.
ALTER TABLE devices ADD COLUMN fcm_fid text;
ALTER TABLE devices ADD COLUMN fcm_disabled_at timestamptz;

CREATE UNIQUE INDEX devices_fcm_fid_idx
    ON devices (application_id, fcm_fid)
    WHERE fcm_fid IS NOT NULL;

COMMIT;
