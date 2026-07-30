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
- PostgreSQL-backed end-to-end API coverage for registration, verification
  resend and invalidation, login, profile read/update, refresh rotation,
  logout, logout-all, password recovery, account lockout, login-attempt
  persistence, and authentication audit events.
- PostgreSQL concurrency coverage proving that only one competing refresh
  rotation succeeds and the replayed session and replacement token are revoked.

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

1. Push `codex/production-completion`, run the CI PostgreSQL job, and retain the
   successful workflow URL and commit SHA as evidence.
2. Repeat the complete authentication API suite in staging with the selected
   email provider and retain the test, outbox, login-attempt, and audit evidence.
3. Validate sender-domain DNS, bounce/suppression handling, and delivery alerts.
4. Run Docker/Helm/Kubernetes, load, backup-restore, rollback, and failure tests.
5. Supply real infrastructure, production secrets, TLS/DNS, monitoring, and
   security approval.

The service is not a public production release until these blockers have
objective test evidence.

## Release-candidate verification matrix

| Control | Repository evidence | External evidence still required |
|---|---|---|
| Authentication routes | Unit/API tests plus PostgreSQL end-to-end suite | Successful CI and staging run |
| Session security | Atomic rotation, replay and concurrent-race tests | Successful CI PostgreSQL run |
| Database changes | Ordered SQL migrations and migration command | Staging migration and rollback exercise |
| Email reliability | Transactional outbox, worker, retry and alerts | Verified provider, sender DNS and webhooks |
| API security | Validation, Helmet, request IDs, rate limits and RBAC | Ingress/TLS and security review |
| Supply chain | Locked dependencies; production audit has zero findings | Container-image scan and signed release |
| Operations | Health, readiness, metrics, alerts and operations guide | Load, failure, backup/restore and rollback evidence |

## Handoff commands

Run from `services/user-management` with an isolated PostgreSQL database:

```bash
npm ci
npm run lint
npm run build
npm test
npm run migrate
npm run test:integration
npm audit --omit=dev --audit-level=high
```

Required environment variables are documented in `.env.example`. Never point
`TEST_DATABASE_URL` at a shared, staging, or production database because the
integration suite creates and deletes test records.
# Release readiness

## Email outbox monitoring

The service exports claimed, delivered, failed, exhausted, and batch-duration
metrics from `/metrics`. Load `observability/user-management-alerts.yml` into
Prometheus and route warning and critical alerts to the on-call channel.

Provider bounce and complaint webhooks remain a release blocker until a real
provider is selected: webhook signatures and event schemas are provider-specific
and must not be invented. Before production, implement a suppression table and
verified webhook adapter for the selected provider, then test replay protection.
