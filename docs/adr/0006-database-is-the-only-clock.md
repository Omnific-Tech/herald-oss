# 6. The database is the only clock

Accepted.

Every scheduling and expiry comparison is made against the database clock.
No `time.Time` crosses the `store` interface for these decisions.

This was a bug before it was a decision. Scheduling originally passed the
application server's `time.Now()` into queries that compared it with timestamps
the database had written. The two machines here differ by about six
milliseconds, which was enough to leave due Web Push deliveries unclaimed and
overdue deliveries unexpired. Across real hosts the skew is larger.

So: `now()` in SQL, intervals rather than absolute instants for scheduling
(`now() + make_interval(...)`), and remaining TTL computed by the query that
claims the work rather than by the process that sends it.
