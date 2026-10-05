# Publishing to Herald

A producer needs one token and one HTTP call. This page is what an integrating
backend needs; the full contract is [protocol-v1.md](protocol-v1.md).

## Getting a token

An operator normally creates the producer in the private admin UI. The
break-glass CLI equivalent is:

```sh
heraldctl create-producer -application sample -name sample-api
```

The token is shown once and stored hashed. It binds to one application: the
publish body has no application field, so a leaked token cannot reach another
product's devices.

## The call

```sh
curl -X POST "$HERALD_URL/v1/messages" \
  -H "Authorization: Bearer $HERALD_PRODUCER_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "idempotency_key": "sample-event-8f21-notify",
    "channel": "alerts",
    "topic": "earthquake.jakarta",
    "priority": "high",
    "title": "M5.1 earthquake, 42 km from Jakarta",
    "body": "Depth 10 km. Felt intensity III-IV.",
    "link": "/events/8f21",
    "data": {"event_id": "8f21", "magnitude": 5.1},
    "expires_at": "2026-09-12T04:31:00Z"
  }'
```

`202`:

```json
{ "message_id": "…", "deduplicated": false, "device_count": 3 }
```

## The four fields that matter

**`idempotency_key`** is unique per application and is what makes retrying safe.
Derive it from the thing you are notifying about plus what changed —
`sample-event-8f21-notify`, not a UUID generated at call time, or a retry creates a
second notification. Replaying a key returns the original `message_id` with
`deduplicated: true` and fans out nothing.

**`expires_at`** is required. Past it, Herald records the delivery and never
shows it for the first time. It does not remove an already displayed notification.
Set it to when the alert stops being worth waking someone for: minutes
for a hazard alert, hours for a daily digest. A device offline past the expiry
sees nothing on reconnect, which is the point.

**`link`** is a path on your own origin, starting with a single `/`. Absolute
URLs, `//host/x` and `/\host/x` are rejected. The client resolves it against the
application's trusted origin.

**`channel`** and **`topic`** are the routing keys. A device subscribed to a
channel with no topic receives every message on it; with a topic, only exact
matches. Nothing is created implicitly — a channel nobody subscribes to returns
`device_count: 0`; an unknown channel or topic is rejected. Create topic IDs in
the application's admin routing catalog before publishing to them.

**`priority`** controls FCM delivery urgency. Use `high` for time-sensitive,
user-visible alerts such as earthquakes; use `normal` for routine updates. FCM
can downgrade repeated high-priority messages that do not display a notification.
Herald currently has no per-producer publish rate limit.

Keep the notification compact. Herald rejects a complete delivery envelope over
3500 bytes so it remains inside the 4096-byte limits of FCM and Web Push. Put
large detail in the linked page or API, not in `data`.

## Retrying

Herald owns delivery once it answers `202`. Before that, the producer owns it.

If saving your event and notifying about it must be atomic, write an outbox row
in the same transaction as the event, and have a worker publish from the outbox:

```go
// Publish returns nil when Herald has durably accepted the message. The caller
// keeps the outbox row until then and retries with the same idempotency key.
func (p *HeraldPublisher) Publish(ctx context.Context, row OutboxRow) error {
    body, err := json.Marshal(row.Envelope)
    if err != nil {
        return err
    }

    request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url+"/v1/messages", bytes.NewReader(body))
    if err != nil {
        return err
    }
    request.Header.Set("Authorization", "Bearer "+p.token)
    request.Header.Set("Content-Type", "application/json")

    response, err := p.client.Do(request)
    if err != nil {
        return err // network failure: retry, the key makes it safe
    }
    defer func() { _ = response.Body.Close() }()

    switch {
    case response.StatusCode == http.StatusAccepted:
        return nil
    case response.StatusCode >= 500, response.StatusCode == http.StatusTooManyRequests:
        return fmt.Errorf("herald unavailable: %d", response.StatusCode)
    default:
        // 400 and 401 will not improve on retry. Park the row and alert.
        return backoff.Permanent(fmt.Errorf("herald rejected: %d", response.StatusCode))
    }
}
```

Retry `5xx`, `429`, and transport errors. Do not retry `400` or `401`: the first
means the envelope is wrong, the second means the token is wrong or revoked.

## Developing before Herald exists

Put publication behind a small interface and use a logging implementation. The
whole ingestion, rules, and persistence path can be built and tested without
Herald running; swap in the HTTP publisher at integration time.

```go
type NotificationPublisher interface {
    Publish(ctx context.Context, row OutboxRow) error
}
```
