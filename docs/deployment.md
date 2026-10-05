# Deploying Herald

`compose.yaml` demonstrates an independent broker, database and migration job.
It binds HTTP to localhost by default and persists database data in a dedicated
Docker volume. The example does not configure public HTTPS.

## Development setup

1. Copy `.env.example` to `.env` and set `ENCRYPTION_KEY` using
   `go run ./cmd/heraldctl keygen`.
2. Run `docker compose up --build -d --wait`.
3. Check `http://localhost:8080/health` and provision an application/producer.
4. Configure your PWA/Android client for your broker and application slug.

## Self hosting

Use an immutable image revision, protected environment file, database backups,
HTTPS termination and your own network access policy. Replace development
credentials. A remote database should require TLS. Restrict the admin control
plane to the configured UI origin and operator credentials.

Set `CORS_ALLOWED_ORIGINS` to exact product origins. Admin UI and API should use
HTTPS origins on the same site for the Secure SameSite=Strict session cookie;
examples use `https://admin.example.com` and `https://api.example.com`. Avoid
redirecting authentication proxies on API requests; Herald checks admin sessions.

FCM is optional. Set your Firebase project ID and provide Application Default
Credentials through a securely mounted service-account file or your platform's
credential mechanism. S3-compatible credential storage is optional. An Android
app must use the Firebase project configured by the broker instance.

## Release and recovery

Public images use a fresh `herald-oss` package namespace and immutable source
commit tags, with AMD64 and ARM64 variants. Legacy private image packages are
not made public as part of this source publication.

Build for your host architecture or use the published image revision. Apply
migrations before starting the new broker. Verify health, provisioning and
delivery after deployment. Preserve the previous image/environment for rollback;
migrations have no down path, so confirm schema compatibility before rollback.

Deployment orchestration and access credentials belong to your private
operations environment. This repository has no production deployment workflow.
