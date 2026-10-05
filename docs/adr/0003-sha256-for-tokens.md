# 3. SHA-256 for token storage

Accepted.

Producer and device tokens are stored as a plain SHA-256 hash, not bcrypt,
scrypt, or argon2.

These are 256 bits of `crypto/rand` output, not passwords. There is no
dictionary to attack and no reuse across services to protect, so a work factor
buys nothing — and it would be paid on every authenticated request, including
every acknowledgement and subscription update.

This reasoning holds only while tokens stay machine-generated and
high-entropy. Anything user-chosen must not use this path.
