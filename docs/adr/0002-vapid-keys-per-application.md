# 2. VAPID keys per application

Accepted.

Each application has its own VAPID keypair rather than Herald sharing one.

A browser `PushSubscription` is pinned to the `applicationServerKey` it was
created with. With one shared key, rotating it would invalidate every
subscription of every product at once, and a compromise would span all of them.
Per-application keys make both blast radii one product.

The cost is a key per application to provision and rotate, and a public endpoint
per application for the PWA to fetch. Private keys are encrypted at rest and
tagged with the id of the key that wrote them, so `ENCRYPTION_KEY` can rotate
without losing them — see docs/operations.md.
