# Operating Herald

## Configuration

Every setting is an environment variable; `.env.example` lists them with
defaults. Three deserve attention.

`ENCRYPTION_KEY` protects VAPID private keys at rest. Web Push is disabled while
it is unset, and applications cannot be created. Generate one with
`heraldctl keygen`.

`ADMIN_USERNAME` and `ADMIN_PASSWORD_HASH` enable the private control plane.
Generate the Argon2id hash with `heraldctl hash-password`, store it as a
single-quoted value in Compose env files, and set `ADMIN_ORIGIN` to the exact
admin UI origin. Sessions expire after `ADMIN_SESSION_TTL` and are stored only
as hashes.

`MAX_BODY_BYTES` caps every request body. The publish handler additionally caps
the complete delivery envelope at 3500 bytes so it fits FCM and Web Push.

`FCM_PROJECT_ID` enables native Android delivery. Authentication uses Google
Application Default Credentials; on a self-hosted VM, mount a Firebase service
account JSON file and set `GOOGLE_APPLICATION_CREDENTIALS` to its path.
Alternatively, set `OBJECT_STORAGE_PROVIDER=s3` and the S3-compatible settings;
Herald fetches `FCM_CREDENTIALS_OBJECT` once at startup and keeps it in memory.

## Schema

`heraldctl migrate` applies unapplied migrations and records them in
`schema_migrations`. It is idempotent and safe to run on every deploy. There are
no down migrations: roll forward.

## Rotating the encryption key

The ciphertext of each VAPID private key is tagged with the id of the key that
wrote it, so a rotation is not destructive.

1. Generate a new key: `heraldctl keygen`.
2. Set `ENCRYPTION_KEY` to the new key and `ENCRYPTION_KEYS_RETIRED` to the
   previous one. Herald now writes with the new key and reads with either.
3. Deploy.
4. Run `heraldctl rewrap`. It re-encrypts every application key still written
   with a retired key and reports how many it changed. It is safe to re-run.
5. Once `rewrap` reports `0`, remove `ENCRYPTION_KEYS_RETIRED` and deploy.

Losing every key with no retired key configured makes the VAPID private keys
unrecoverable. Recovery then means issuing new VAPID keypairs, which invalidates
every existing browser subscription: each affected PWA has to re-subscribe with
the new application server key.

## What to look at when something is wrong

**No Web Push is going out.** Check the logs for `web push dispatch failed` and
`vapid key unreadable`. The usual causes are an `ENCRYPTION_KEY` that does not
match what wrote the rows (see rotation above) and outbound HTTPS being blocked.
Confirm deliveries are actually queued:

```sql
SELECT count(*) FROM deliveries WHERE state = 'pending' AND next_attempt_at IS NOT NULL;
```

**The dispatcher is behind.** That query is the queue depth. If it grows while
`attempts` climbs, the push services are refusing; the logs carry the status
code. Raise `WEBPUSH_BATCH_SIZE` only after confirming the sends are succeeding.

**A device says it receives nothing.** Check it has subscriptions, that its
deliveries exist, and that its Web Push subscription has not been disabled:

```sql
SELECT platform, webpush_disabled_at, last_sequence, last_seen_at
FROM devices WHERE install_id = '…';
```

A non-null `webpush_disabled_at` means the push service reported the
subscription gone; the client has to re-register to supply a new one.

**No Android push is going out.** Check `FCM_PROJECT_ID`,
`GOOGLE_APPLICATION_CREDENTIALS`, and logs for `fcm dispatch failed`. A device
whose Firebase installation no longer exists is disabled without deleting its
delivery history; launching the app registers its current installation again.

**A producer is publishing but nothing fans out.** `device_count: 0` on the
publish response means no device matched the channel and topic. Subscriptions
are exact-match on channel, and on topic unless the subscription's topic is
empty.

## Retention

Nothing deletes `messages` or `deliveries`. Both grow without bound, and
`acknowledgements` grows with them. There is no retention job yet; until there
is, prune expired and acknowledged rows older than your audit window:

```sql
DELETE FROM messages
WHERE expires_at < now() - interval '30 days';
```

Deliveries and acknowledgements cascade from that. Take the usual care: run it
in batches on a large table.

## Running more than one instance

Herald is safe to run as several instances against one database. Web Push claims
are leased with `SKIP LOCKED`, publish locks its fan-out targets in a stable
order, and the expiry sweep works in bounded batches. Each instance runs its own
dispatcher and sweeper; that is expected and costs only the empty polls.
