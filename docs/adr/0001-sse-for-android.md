# 1. SSE for the Android delivery stream

Superseded by [ADR 7](0007-fcm-for-android.md).

Android product APKs receive deliveries over Server-Sent Events, with
acknowledgements sent as separate HTTP POSTs.

The stream is one-way in practice: the server pushes deliveries, and the only
thing flowing back is an ack that is cheap as its own request. WebSocket would
add ping/pong, close codes, and a reconnect protocol for traffic that is
essentially unidirectional. SSE is proxyable, testable with curl, and needs no
dependency.

The costs are real and accepted: the HTTP write timeout must stay zero for the
connection to live, so each write carries its own deadline, and the server polls
storage rather than being pushed to. If polling becomes the bottleneck, Postgres
`LISTEN/NOTIFY` replaces the poll without changing the wire format.
