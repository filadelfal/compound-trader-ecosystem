# User-management operations

## Release validation

Run from `services/user-management`:

```bash
npm ci
npm run lint
npm run build
npm test
npm audit --omit=dev
```

Use a disposable PostgreSQL database for integration validation:

```bash
export TEST_DATABASE_URL=postgresql://user:password@localhost:5432/compound_test
npm run test:integration
```

The integration suite applies its required schema and truncates authentication
tables. Never point `TEST_DATABASE_URL` at staging or production.

## Migrations

Set `DATABASE_URL` to the target database and run:

```bash
npm run migrate
```

The runner records each filename and SHA-256 checksum in `schema_migrations`.
It refuses to continue if an already-applied migration was edited. Published
migrations are immutable; corrections must be new, forward-only migration files.

Before deployment, back up PostgreSQL and test restoration in an isolated
environment. Apply migrations before switching application traffic, then verify:

```bash
curl --fail http://localhost:3002/health
curl --fail http://localhost:3002/ready
curl --fail http://localhost:3002/openapi.json
```

## Rollback

Application rollback uses the previously approved container image. Database
rollback is restore-based because migrations are forward-only:

1. Stop traffic to the new application revision.
2. Redeploy the previous immutable image.
3. If a migration caused incompatible data changes, restore the verified
   pre-deployment backup.
4. Run health, readiness, login, refresh, and logout smoke tests.
5. Preserve logs and audit events for incident review.

## Secrets

Supply database, Redis, JWT, and mail-provider credentials through the deployment
secret manager. JWT access and refresh secrets must be different, random, and at
least 64 characters. Do not place production values in images, manifests, logs,
Git history, or `.env.example`.

## Observability and incident response

Scrape `/metrics`, retain structured logs, and propagate `X-Request-Id` through
the ingress. Alert on readiness failure, elevated 5xx responses, login lockouts,
refresh-token replay events, database connection exhaustion, and unusual
password-reset volume.

If refresh-token replay or credential compromise is suspected, revoke all user
sessions, rotate affected secrets, preserve audit logs, and require password
reset before restoring access.
