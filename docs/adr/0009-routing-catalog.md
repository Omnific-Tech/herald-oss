# 9. Application-scoped routing catalog

Accepted.

The Other and Sample APKs ship Android channels `alerts` and `system`. Herald
exposes those fixed IDs and an application-scoped topic catalog to the admin UI.
Topic IDs are permanent; labels can change, and archiving blocks new publishes
and subscriptions without deleting history.

Earlier `/v1` clients could use arbitrary channel and topic strings. This is a
deliberate compatibility change: migration `0007` imports topic IDs found in
existing messages and subscriptions, but producers that invent new IDs must
register them in the catalog first. The change prevents mistyped routing keys
from silently reaching zero devices.
