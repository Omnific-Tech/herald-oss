# Admin API contract

The admin UI in `admin/` and the backend are built against this contract.

Provisioning over HTTP supersedes
[ADR 0004](adr/0004-provisioning-is-cli-only.md) through
[ADR 0008](adr/0008-authenticated-admin-control-plane.md).

## Topology

| Role     | Origin                             |
| -------- | ---------------------------------- |
| Admin UI | `https://admin.herald.example.com` |
| Admin API | `https://herald.example.com/api`  |

The two are different origins but the same site (`example.com`), so a
`SameSite=Strict` cookie set by the API is sent on the UI's credentialed
cross-origin requests. The UI reads the base URL from `VITE_ADMIN_API_URL`,
which is public configuration, not a secret.

Cloudflare Access may gate `admin.herald.example.com`. It must not gate
`herald.example.com/api/*`: an Access redirect on a cross-origin `fetch` fails
CORS. Access is never the authorization layer; every `/api/*` route enforces the
admin session itself.

## Transport rules

- The UI sends every request with `credentials: "include"`.
- Mutations (`POST`, `PATCH`, `DELETE`) carry `Content-Type: application/json`, which
  forces a CORS preflight and rules out form-encoded cross-site posts.
- `/api/*` uses its own CORS policy, separate from the `/v1` one:
  - `Access-Control-Allow-Origin` is exactly `ADMIN_ORIGIN`, never `*`, never
    an origin from `CORS_ALLOWED_ORIGINS`.
  - `Access-Control-Allow-Credentials: true`, `Vary: Origin`.
  - `Access-Control-Allow-Methods: GET, POST, PATCH, DELETE`.
  - `Access-Control-Allow-Headers: Content-Type`.
  - `Access-Control-Expose-Headers: Retry-After`, or the UI cannot read the
    rate-limit delay cross-origin.
  - `OPTIONS` preflight is answered before authentication runs.
- Every mutation, including `POST /api/session` and `DELETE /api/session`,
  rejects a request whose `Origin` is not exactly `ADMIN_ORIGIN` with `403
  origin_rejected`. Every other `*.example.com` host is same-site and would
  otherwise have the cookie attached.
- Every `/api/*` response carries `Cache-Control: no-store`.
- Timestamps are RFC 3339 strings. Absent optional timestamps are `null`.

## Session cookie

- Name `__Host-herald_admin`: `Secure`, `HttpOnly`, `SameSite=Strict`,
  `Path=/`, no `Domain` attribute (host-only).
- Value is a random opaque token. The server stores only its hash.
- Fixed expiry (eight hours). No sliding renewal.
- One admin username with an Argon2id password hash from protected
  environment configuration.
- Login attempts are rate limited.

## Errors

Every error body is:

```json
{ "error": "human readable message", "code": "machine_code" }
```

A `422` adds per-field messages:

```json
{ "error": "invalid request", "code": "invalid", "fields": { "slug": "must be lowercase" } }
```

On every authenticated route:

| Status | Meaning | UI behaviour |
| ------ | ------- | ------------ |
| `401 unauthenticated` | No session, or it expired | Return to login, "session expired" |
| `403 origin_rejected` / `forbidden` | Authenticated but refused | Return to login, "not authorized" |

## Routes

All routes require a valid session except `POST /api/session`.

### `POST /api/session`

Request:

```json
{ "username": "admin", "password": "…" }
```

`200`, with `Set-Cookie`:

```json
{ "username": "admin", "expires_at": "2026-09-24T16:00:00Z" }
```

Errors: `401 invalid_credentials` (same response for an unknown username and a
wrong password), `429 rate_limited` with `Retry-After`, `403 origin_rejected`.

### `GET /api/session`

`200 { "username", "expires_at" }`. `401 unauthenticated` is the normal "not
signed in" answer.

### `DELETE /api/session`

`204`. Deletes the session row when present and always clears the cookie. This
is idempotent, including when the session already expired.

### `GET /api/health`

Same body as the public `/health`. `200` when healthy, `503` when degraded:

```json
{ "status": "ok", "service": "herald", "version": "v1.4.0", "database": "ok" }
```

### `GET /api/applications`

`200`:

```json
{
  "applications": [
    {
      "id": "uuid",
      "slug": "sample",
      "display_name": "Sample",
      "origin": "https://sample.example",
      "created_at": "2026-09-01T00:00:00Z"
    }
  ]
}
```

Ordered by `slug`.

### `POST /api/applications`

Request:

```json
{
  "slug": "sample",
  "display_name": "Sample",
  "origin": "https://sample.example",
  "vapid_subject": "mailto:ops@sample.example"
}
```

`201`:

```json
{ "application": { "id": "…", "slug": "…", "display_name": "…", "origin": "…", "created_at": "…" }, "vapid_public_key": "…" }
```

Errors: `409 slug_taken`, `422 invalid_fields`.

### `GET /api/applications/:slug/producers`

`200`:

```json
{
  "producers": [
    { "id": "uuid", "name": "sample-api", "created_at": "…", "revoked_at": null }
  ]
}
```

Ordered by `created_at`. Errors: `404 application_not_found`.

### `POST /api/applications/:slug/producers`

Request:

```json
{ "name": "sample-api" }
```

`201`:

```json
{ "producer": { "id": "…", "name": "sample-api", "created_at": "…", "revoked_at": null }, "token": "…" }
```

`token` appears in this response only. It is never logged and cannot be
recovered. Errors: `404 application_not_found`, `409 name_taken` (an active
producer with that name already exists), `422 invalid_fields`.

A partial unique index on `producers (application_id, name) WHERE revoked_at IS
NULL` backs `name_taken`, while revoked history remains addressable by id.

### `POST /api/applications/:slug/producers/:id/revoke`

Request body `{}`. Revocation is by immutable producer `id`, not name.

`200 { "producer": { … "revoked_at": "…" } }`.

Errors: `404 producer_not_found` (also when the producer belongs to another
application), `409 already_revoked`.

### `POST /api/applications/:slug/messages`

Publishes a notification as the signed-in admin. It uses the same validation
and delivery pipeline as `POST /v1/messages` (field limits in
[protocol-v1.md](protocol-v1.md)), records the admin username as the
publisher atomically with the message, does not impersonate a producer, and
reaches only devices of `:slug`. No producer token is involved.

Request:

```json
{
  "idempotency_key": "admin-<uuid>",
  "channel": "alerts",
  "topic": "",
  "priority": "high",
  "title": "Test notification",
  "body": "Herald delivery test",
  "link": "/",
  "data": {},
  "collapse_key": "",
  "expires_at": "2026-09-24T16:00:00Z"
}
```

Required: `idempotency_key`, `channel`, `title`, `body`, `expires_at`.

`202`:

```json
{ "message_id": "uuid", "deduplicated": false, "device_count": 1 }
```

A repeated `idempotency_key` returns the original message with
`deduplicated: true` and its original `device_count`, and sends nothing. The
UI generates one key per draft and reuses it on every retry, so a retry after
a lost response cannot send twice.

`topic` must be `""` (whole channel) or the id of an active topic on
`channel` in this application; anything else is `422 invalid_fields` on
`topic`. The UI only offers valid choices, but the server is the enforcement
point.

Errors: `404 application_not_found`, `422 invalid_fields`, `500
internal_error`.

### `GET /api/applications/:slug/messages`

Read-only publication history for one application, newest first. All V1
messages are published immediately; `status` is always `published`. The
`device_count` is the fan-out count recorded at publication time.

Optional query parameters: `limit` defaults to 20 and must be 1–100; `cursor`
is the opaque `next_cursor` from the previous page. An invalid parameter returns
`400 invalid_query`.

`200`:

```json
{
  "messages": [
    {
      "id": "uuid",
      "status": "published",
      "channel": "alerts",
      "topic": "earthquake.jakarta",
      "priority": "high",
      "title": "Earthquake alert",
      "body": "Magnitude 5.1",
      "link": "/events/123",
      "device_count": 2,
      "publisher": { "type": "admin", "name": "operator" },
      "delivery_counts": {
        "pending": 1, "sent": 0, "displayed": 1,
        "delivered": 0, "suppressed": 0,
        "failed": 0, "expired": 0, "superseded": 0
      },
      "delivery_errors": [],
      "created_at": "2026-09-25T08:00:00Z",
      "expires_at": "2026-09-25T08:10:00Z"
    }
  ],
  "next_cursor": null
}
```

`publisher.type` is `admin` or `producer`; `name` is the admin username or
producer name. `pending` is queued for delivery. `sent` means FCM/Web Push
accepted the send, not that the device displayed it. `displayed`, `delivered`,
and `suppressed` are device acknowledgments. Counts reflect retained delivery
rows, so they can become smaller than `device_count` if devices are deleted.
`delivery_errors` lists up to five grouped reasons as
`{state, reason, count}`, ordered by count. It includes pending retries and
failed sends, but no device identifiers. Reasons are bounded to 512 characters
and contain only provider error messages or generic transport categories, not
raw provider responses. A successful send or device acknowledgement clears
the prior error. Deliveries failed before this schema change have no recorded
reason.

Errors: `404 application_not_found`, `400 invalid_query`. There are no V1
history edit, cancel, or scheduling routes.

### `GET /api/applications/:slug/routing`

Channels and topics of one application. Applications never share topics.

`200`:

```json
{
  "channels": [
    { "id": "alerts", "label": "Alerts" },
    { "id": "system", "label": "System" }
  ],
  "topics": [
    {
      "id": "earthquake.jakarta",
      "label": "Jakarta earthquakes",
      "channel": "alerts",
      "archived_at": null
    }
  ]
}
```

`topics` includes archived topics. Channels ship inside each Android APK, so
there is no channel create, edit, or delete route.

Errors: `404 application_not_found`.

### `POST /api/applications/:slug/topics`

```json
{ "id": "earthquake.jakarta", "label": "Jakarta earthquakes", "channel": "alerts" }
```

`201 { "topic": { … } }`. Topic ids are permanent and never reused.

Errors: `404 application_not_found`, `409 topic_exists` (including an
archived topic with that id), `422 invalid_fields` (for example a `channel`
that is not in this application's routing).

### `PATCH /api/applications/:slug/topics/:id`

```json
{ "label": "Jakarta area earthquakes" }
```

`200 { "topic": { … } }`. Only the label changes.

Errors: `404 topic_not_found`, `409 already_archived`, `422 invalid_fields`.

### `POST /api/applications/:slug/topics/:id/archive`

Request body `{}`. `200 { "topic": { … "archived_at": "…" } }`. Archived
topics leave the send form but stay listed.

Errors: `404 topic_not_found`, `409 already_archived`.

## Future work (not implemented)

Aggregate analytics and scheduled publishing are not available in V1. V1
history contains only immediately published messages and cannot edit or cancel
them.

The proposed V2 scheduling model would store a future `scheduled_at` and track
`scheduled`, `publishing`, `published`, or `canceled`. Editing, publishing now,
and cancellation would be allowed only before publishing begins. Once FCM or a
Web Push service has accepted a delivery, Herald cannot reliably retract it;
corrections would be sent as a new message, optionally with the same collapse
key.
