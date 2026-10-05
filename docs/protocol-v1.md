# Herald protocol v1

Herald accepts messages from authenticated producers, fans them out to
subscribed devices, and records acknowledgements. It contains no product rules.

## Delivery semantics

- **At-least-once.** A device may receive the same delivery more than once. The
  client deduplicates on `delivery_id`.
- **Producer idempotency.** `idempotency_key` is unique per application.
  Replaying it returns the original `message_id` with `deduplicated: true` and
  creates no additional deliveries.
- **Per-device sequence.** Every delivery gets a monotonic `sequence` for its
  device. Sequences are **not contiguous**: expired, superseded and failed
  deliveries are skipped, so a client must not treat a gap as a lost message.
- **Transport handoff.** FCM and Web Push acceptance marks a delivery `sent`;
  only an explicit client `POST .../ack` marks it acknowledged.
- **Idempotent acks.** Repeating an ack returns `200` with the stored record,
  including its original status and timestamp.
- **Expiry.** A delivery whose message is past `expires_at` is recorded as
  `expired` and never sent or re-sent. A client that receives an already-expired
  message suppresses it and acks with `status: "suppressed"`. Expiry does not
  remove a notification already visible on the device.
- **Collapsing.** A message carrying a `collapse_key` moves that device's
  earlier deliveries with the same key to `superseded`, including ones already
  transmitted but not yet acknowledged. The Android client also replaces an
  already visible notification bearing that key.
- **Subscription changes are not serialised against in-flight publishes.** A
  subscription committed while a publish is choosing its targets may miss that
  message, and a just-removed subscription may still receive one. There is no
  catch-up: deliveries are only created at fan-out.

## Authentication

| Credential | Header | Scope |
|---|---|---|
| Producer token | `Authorization: Bearer <producer-token>` | One application, publish only |
| Device token | `Authorization: Bearer <device-token>` | One device: ack, subscriptions, rotation |

Tokens are stored hashed. The plaintext device token is returned once per
registration or rotation; the plaintext producer token is returned once by the
admin control plane or break-glass CLI.

The target application is resolved from the producer credential. There is no
application field in the publish body, so a producer cannot publish for another
application.

There is no caller-supplied device identifier. Registration either creates a new
device or, when a device presents its own token, rotates that device's
credential. No caller can name a device that is not theirs.

## Routes

```text
GET  /health                                   open
GET  /v1/applications/:slug/vapid-public-key   open
POST /v1/devices/register                      open, or device token to rotate
POST /v1/messages                              producer token
PUT  /v1/subscriptions                         device token
PUT  /v1/device/fcm                            android device token
POST /v1/deliveries/:delivery_id/ack           device token
```

### `GET /health`

```json
{ "status": "ok", "service": "herald", "version": "1.4.0", "database": "ok" }
```

`200` when the database answers. `503` with `"status": "degraded"` when it does
not or when `DATABASE_URL` is not configured.

### `POST /v1/messages`

```json
{
  "idempotency_key": "sample-event-8f21-notify",
  "channel": "alerts",
  "topic": "earthquake.jakarta",
  "priority": "high",
  "title": "M5.1 earthquake, 42 km from Jakarta",
  "body": "Depth 10 km. Felt intensity III-IV.",
  "link": "/events/8f21",
  "data": { "event_id": "8f21", "magnitude": 5.1 },
  "collapse_key": "earthquake.8f21",
  "expires_at": "2026-09-12T04:31:00Z"
}
```

| Field | Required | Limit |
|---|---|---|
| `idempotency_key` | yes | 200 characters |
| `channel` | yes | 64 |
| `topic` | no | 200 |
| `priority` | no | `normal` (default) or `high` |
| `title` | yes | 200 |
| `body` | yes | 2000 |
| `link` | no | 500, and a path on the application's own origin |
| `data` | no | Complete delivery envelope must remain at or below 3500 bytes |
| `collapse_key` | no | 200 |
| `expires_at` | yes | must be in the future |

`link` must start with a single `/`. `https://host/x`, `//host/x` and `/\host/x`
are all rejected: a browser resolves the last two against another origin, so
accepting them would let a producer direct a device off its trusted origin.

`channel` must be a channel shipped in the product APK (`alerts` or `system`).
`topic` is empty for the whole channel, or an active topic ID in that
application's [routing catalog](admin-api.md). Unknown and archived topic IDs
are rejected on new publishes.

`title` and `body` are visible notification text. `data` is opaque JSON delivered
to the client for product-specific behavior; the current Android APK decodes it
but does not display or act on it. Do not place secrets in push payloads.

Response `202`:

```json
{ "message_id": "…", "deduplicated": false, "device_count": 3 }
```

`device_count` is the fan-out size recorded when the message was first
published. A replay reports that same number even if devices have since gone.

### `POST /v1/devices/register`

With no `Authorization` header, creates a device:

```json
{
  "application": "sample",
  "platform": "android",
  "display_name": "Pixel 10",
  "fcm_fid": "cYx…"
}
```

A Web Push registration instead supplies:

```json
{
  "application": "sample",
  "platform": "web",
  "display_name": "Safari on iPhone",
  "webpush": {
    "endpoint": "https://fcm.googleapis.com/fcm/send/…",
    "p256dh": "BN…",
    "auth": "k9…"
  }
}
```

With a device's own token in `Authorization`, rotates that device's credential
and keeps its sequence and delivery history. `application` is then ignored.

`fcm_fid` is required for `android`. `webpush` is required for `web`. The
Firebase Installation ID is not a secret; the returned device token protects
subscription and acknowledgement operations.
Rotating a `web` device re-enables a subscription Herald had disabled.

Response `200`:

```json
{
  "device_id": "…",
  "install_id": "…",
  "device_token": "…",
  "acknowledged_sequence": 417
}
```

`install_id` is generated by Herald and stable across rotations.
`acknowledged_sequence` is `0` on creation and retained for delivery auditing.

### `PUT /v1/subscriptions`

Replaces the calling device's full subscription set. An empty list unsubscribes
the device from everything. An entry with no `topic` matches the whole channel.
New subscriptions must name a shipped channel and, when nonempty, an active
topic in the device's application. Invalid updates leave the prior set intact.
If a device has both a whole-channel and an exact-topic subscription, it still
gets only one delivery per message.

```json
{
  "subscriptions": [
    { "channel": "alerts", "topic": "earthquake.jakarta" },
    { "channel": "system" }
  ]
}
```

Response `200` echoes the stored set in the same shape.

### `PUT /v1/device/fcm`

An Android app refreshes its Firebase Installation ID on every launch. This
also re-enables an installation that FCM previously reported missing.

```json
{ "fcm_fid": "cYx…" }
```

Returns `204`. Web devices receive `400`.

### `POST /v1/deliveries/:delivery_id/ack`

```json
{ "status": "displayed" }
```

`delivered` means durably received, `displayed` means a notification was
rendered, `suppressed` means it arrived already expired and was not shown.

Response `200`:

```json
{ "delivery_id": "…", "status": "displayed", "acknowledged_at": "2026-09-12T04:22:31Z" }
```

Repeating an ack returns the first record, not the new status.

### `GET /v1/applications/:slug/vapid-public-key`

```json
{ "application": "sample", "public_key": "BN…" }
```

Public by construction: a PWA needs the application server key before calling
`pushManager.subscribe()`. Keys are per application, so a rotation affects one
product only. Rotating one invalidates every existing browser subscription for
that application, because a `PushSubscription` is pinned to the
`applicationServerKey` it was created with.

## Push transports

Android devices receive a data-only FCM message whose `herald_delivery` value
is the JSON delivery envelope below. `priority: high` maps to FCM high priority;
normal messages use normal priority. FCM TTL is the message's remaining life.

Web devices receive the same envelope through standards-based Web Push.

A service worker decodes this Web Push body:

```json
{
  "delivery_id": "…",
  "sequence": 418,
  "message": {
    "id": "…",
    "channel": "alerts",
    "topic": "earthquake.jakarta",
    "priority": "high",
    "title": "M5.1 earthquake, 42 km from Jakarta",
    "body": "Depth 10 km. Felt intensity III-IV.",
    "link": "/events/8f21",
    "data": { "event_id": "8f21", "magnitude": 5.1 },
    "collapse_key": "earthquake.8f21",
    "expires_at": "2026-09-12T04:31:00Z",
    "created_at": "2026-09-12T04:21:00Z"
  }
}
```

`topic`, `link`, `data` and `collapse_key` are omitted when empty. Producer,
application and idempotency identifiers are never sent to devices.

The push TTL is the message's remaining lifetime, capped at 28 days, so an
offline device never wakes to an alert Herald would refuse to display. A
`collapse_key` is forwarded as the Push API `Topic` header when it fits that
header's format (up to 32 characters of `A-Za-z0-9_-`); one that does not fit is
dropped, since losing the collapse must not lose the notification.

Failure handling:

| Push service response | Herald |
|---|---|
| `2xx` | Delivery marked sent; the device must still ack |
| `410` | Subscription disabled, device and history kept |
| `404` | Retried; subscription disabled once attempts are spent |
| `429`, `5xx`, transport error | Retried, 30s doubling to 15 minutes, up to `WEBPUSH_MAX_ATTEMPTS` |
| anything else | Delivery failed immediately |

## Provisioning

Applications and producer tokens are normally managed through the authenticated
[admin API](admin-api.md). `heraldctl` remains available as a break-glass path:

```sh
heraldctl keygen
heraldctl migrate
heraldctl create-application -slug sample -name Sample \
    -origin https://sample.example -subject mailto:ops@sample.example
heraldctl create-producer -application sample -name sample-api
heraldctl revoke-producer -application sample -name sample-api
```

`create-application` generates the application's VAPID keypair and stores the
private key encrypted with `ENCRYPTION_KEY`. Web Push is disabled while that key
is unset.

## Transport per platform

| Platform | Transport |
|---|---|
| Android product APK | FCM data message, ack over HTTP POST |
| Desktop PWA | Web Push (VAPID) |
| iPhone / iPad PWA | Web Push through APNs |
| Garmin | Mirrored Android notification, no Herald involvement |

## Errors

Every non-2xx response is `{ "error": "…" }`.

| Status | Meaning |
|---|---|
| `400` | Malformed body, oversized body, an untrusted `link`, or `expires_at` in the past |
| `401` | Missing, malformed, or unknown token |
| `404` | Unknown application, or a delivery that is not the caller's |
| `500` | Storage failure |
| `503` | `DATABASE_URL` is unset |

A delivery belonging to another device is `404`, not `403`: the caller must not
learn that it exists. A malformed `delivery_id` is also `404`.

## Compatibility

See [versioning.md](versioning.md).
