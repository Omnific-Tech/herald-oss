# 7. FCM for Android delivery

Accepted.

Android product APKs receive data-only Firebase Cloud Messaging notifications
addressed by Firebase Installation ID. Herald still owns routing, expiry,
idempotency, collapse, and acknowledgements; FCM replaces only the transport.

The previous persistent SSE connection used a `dataSync` foreground service.
Android limits that service type to six background hours per day for apps
targeting Android 15 or newer, so it could not provide continuous alerts.

High-priority FCM is used only for messages whose Herald priority is `high`.
Every FCM message contains the normal Herald delivery envelope, and the Android
client renders and acknowledges it. Desktop and Apple clients continue to use
standards-based Web Push.
