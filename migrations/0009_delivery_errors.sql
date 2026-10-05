-- Keep a bounded, admin-visible reason for the latest failed send attempt.
ALTER TABLE deliveries ADD COLUMN last_error varchar(512);
