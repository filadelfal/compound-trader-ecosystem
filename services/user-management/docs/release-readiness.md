# User-management release readiness

## Verified in this repository

- Complete authentication, session, current-user profile, and RBAC routes.
- Validation, request IDs, security headers, rate limits, centralized errors,
  structured logs, audit records, and OpenAPI.
- Argon2id, lockout, JWT access tokens, atomic refresh rotation, replay
  detection, and session revocation.
- Unit/API tests, isolated PostgreSQL CI, migrations, production dependency
  audit, Docker, Kubernetes, Helm, and operations guidance.
- Configurable HTTPS email provider with timeout, transient retries, redacted
  failure logging, and a development-only fallback.
- Transactional PostgreSQL email outbox with concurrent-safe claiming,
  asynchronous delivery, bounded retries, and exponential backoff.
- PostgreSQL-backed end-to-end API coverage for registration, verification,
  login, authenticated profile access, and refresh-token rotation.

## Production email configuration

Set these through the deployment secret/configuration system:

```text
EMAIL_PROVIDER=http
EMAIL_FROM=no-reply@your-verified-domain.example
EMAIL_API_URL=https://your-provider.example/v1/send
EMAIL_API_KEY=<secret>
EMAIL_TIMEOUT_MS=5000
EMAIL_MAX_ATTEMPTS=3
EMAIL_OUTBOX_POLL_MS=1000
EMAIL_OUTBOX_BATCH_SIZE=20
EMAIL_OUTBOX_MAX_ATTEMPTS=8
EMAIL_OUTBOX_RETRY_BASE_SECONDS=30
WEB_APP_URL=https://app.your-domain.example
```

Production rejects the development provider, and the API URL must use HTTPS.
Never commit provider keys or place them in a container image. Confirm the
provider payload contract in staging.

## Remaining release blockers

1. Run the CI PostgreSQL job and retain successful evidence.
2. Exercise all authentication routes against PostgreSQL in staging, including
   concurrent refresh rotation and replay.
3. Validate sender-domain DNS, bounce/suppression handling, and delivery alerts.
4. Run Docker/Helm/Kubernetes, load, backup-restore, rollback, and failure tests.
5. Supply real infrastructure, production secrets, TLS/DNS, monitoring, and
   security approval.

The service is not a public production release until these blockers have
objective test evidence.
# Release readiness

## Email outbox monitoring

The service exports claimed, delivered, failed, exhausted, and batch-duration
metrics from `/metrics`. Load `observability/user-management-alerts.yml` into
Prometheus and route warning and critical alerts to the on-call channel.

Provider bounce and complaint webhooks remain a release blocker until a real
provider is selected: webhook signatures and event schemas are provider-specific
and must not be invented. Before production, implement a suppression table and
verified webhook adapter for the selected provider, then test replay protection.
