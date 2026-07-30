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

## Production email configuration

Set these through the deployment secret/configuration system:

```text
EMAIL_PROVIDER=http
EMAIL_FROM=no-reply@your-verified-domain.example
EMAIL_API_URL=https://your-provider.example/v1/send
EMAIL_API_KEY=<secret>
EMAIL_TIMEOUT_MS=5000
EMAIL_MAX_ATTEMPTS=3
WEB_APP_URL=https://app.your-domain.example
```

Production rejects the development provider, and the API URL must use HTTPS.
Never commit provider keys or place them in a container image. Confirm the
provider payload contract in staging.

## Remaining release blockers

1. Add a transactional email outbox and worker so messages survive provider
   outages after a database transaction commits.
2. Run the CI PostgreSQL job and retain successful evidence.
3. Exercise all authentication routes against PostgreSQL in staging, including
   concurrent refresh rotation and replay.
4. Validate sender-domain DNS, bounce/suppression handling, and delivery alerts.
5. Run Docker/Helm/Kubernetes, load, backup-restore, rollback, and failure tests.
6. Supply real infrastructure, production secrets, TLS/DNS, monitoring, and
   security approval.

The service is not a public production release until these blockers have
objective test evidence.
