# 4. Provisioning is CLI-only

Superseded by [ADR 0008](0008-authenticated-admin-control-plane.md).

Creating an application and minting a producer token are `heraldctl` commands.
There is no HTTP route for either, and `Admin` sits outside the `store.Store`
interface the HTTP layer is given.

Minting a producer token is the capability `POST /v1/messages` exists to check.
Exposing it over HTTP would mean building an admin authentication scheme whose
compromise grants the ability to publish as any application — a larger surface
than the one it protects.

The cost is that provisioning needs database access, so it happens from a deploy
host or a migration job rather than a UI. Structurally keeping `Admin` out of
`store.Store` is what stops a future refactor from quietly reversing this.
