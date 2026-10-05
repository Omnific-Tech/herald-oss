# 5. Expired deliveries are recorded, not deleted

Accepted.

A delivery past its message's `expires_at` moves to state `expired`. It is not
removed.

Expiry is enforced at read time: both FCM and Web Push claims
filter on it, so an expired delivery is never sent regardless of when the sweep
runs. The sweep is bookkeeping. Keeping the row means "this alert existed and
was withheld" is answerable afterwards, which for hazard alerts is the question
that gets asked.

The cost is unbounded growth of `deliveries` and `messages`. There is no
retention job yet; docs/operations.md carries the manual prune until there is.
