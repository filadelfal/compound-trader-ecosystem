# User Management

Production authentication, session, profile, and role-authorization service for
the Compound Trader Ecosystem.

## API

The complete OpenAPI 3.1 contract is served at `GET /openapi.json`.

Authentication:

- `POST /api/v1/auth/register`
- `POST /api/v1/auth/verify-email`
- `POST /api/v1/auth/resend-verification`
- `POST /api/v1/auth/login`
- `POST /api/v1/auth/refresh`
- `POST /api/v1/auth/logout`
- `POST /api/v1/auth/logout-all`
- `POST /api/v1/auth/forgot-password`
- `POST /api/v1/auth/reset-password`

Users:

- `GET /api/v1/users/me`
- `PATCH /api/v1/users/me`

Operations:

- `GET /health`
- `GET /ready`
- `GET /metrics`
- `GET /api/v1/ping`

## Local validation

Copy `.env.example` to a local `.env` and replace every development-only value.
Never commit `.env`.

```bash
npm ci
npm run lint
npm run build
npm test
npm audit --omit=dev
```

PostgreSQL integration tests require an isolated disposable database:

```bash
TEST_DATABASE_URL=postgresql://user:password@localhost:5432/compound_test \
  npm run test:integration
```

See [operations.md](docs/operations.md) for migrations, deployment checks,
rollback, and incident procedures.
