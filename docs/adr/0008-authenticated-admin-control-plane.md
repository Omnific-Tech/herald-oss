# 8. Authenticated admin control plane

Accepted. Supersedes ADR 0004.

Normal application and producer provisioning is available through `/api/*` so
the private Herald admin UI can manage it without database or host access. The
existing CLI remains a break-glass operations path.

The V1 control plane has one operator configured by username and an Argon2id
password hash. A successful login creates a random opaque session cookie; only
its SHA-256 hash and expiry are stored in PostgreSQL. Cookies are host-only,
Secure, HttpOnly, SameSite=Strict, and fixed-lived.

The UI and API are different origins but the same site. `/api/*` therefore has
an exact `ADMIN_ORIGIN` credentialed CORS policy, and every mutation separately
requires that exact `Origin`. This second check is required because any sibling
`*.example.com` origin is same-site and would otherwise receive the Strict
cookie. Responses are never cached, login attempts are rate limited, and
producer credentials are returned once before only their hash is retained.

Producer revocation uses immutable ids. A partial unique database index permits
one active producer name per application while retaining revoked history and
allowing the name to be reused later.
