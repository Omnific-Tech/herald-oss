# Versioning

The HTTP surface is versioned in the path: everything under `/v1` is the version
1 contract described in [protocol-v1.md](protocol-v1.md).

## What is breaking

Within `/v1` Herald will not:

- remove or rename a route, request field, or response field;
- narrow a field's accepted values, or tighten a length limit;
- change the meaning of a status code;
- change the delivery semantics clients depend on: at-least-once, per-device
  monotonic sequences, idempotent acks, and expiry.

## What is not breaking

- Adding a route, an optional request field, or a response field. Clients must
  ignore response fields they do not recognise.
- Adding an allowed topic to an application's routing catalog.
- Loosening a limit.
- Any change to internal storage, including the schema, that keeps the above.

## Changing the contract

A breaking change means a new `/v2` prefix, served alongside `/v1`. Both run
until every client of `/v1` has moved; Herald has few and known clients, so the
overlap is negotiated rather than fixed by policy.

`herald-android` pins the version it speaks. A product APK built against `/v1`
keeps working when `/v2` ships.

The routing catalog introduced a deliberate exception to the earlier open-ended
`channel` and `topic` behavior. Existing topic IDs were imported into the
catalog during migration; new IDs must be created before clients subscribe or
producers publish. See [ADR 0009](adr/0009-routing-catalog.md).

The push delivery envelope no longer carries `application_id`, `producer_id`
or `idempotency_key`. They exposed producer identifiers to every device, and
neither client read them: `herald-android` and `herald-web` ignore unknown
fields and never referenced these. The envelope shrank, which loosens the
effective room left for `data` under the 3500-byte limit.
