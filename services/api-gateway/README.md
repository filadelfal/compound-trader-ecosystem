# api-gateway

REST API gateway

Operator requests under `/api/v1/operator/*` require an existing user-management Bearer access token. The gateway accepts only `admin` or `super_admin`, maps the role to fixed PAPER permissions, creates a one-use short-lived assertion, and forwards it to the internal trading engine. Client-supplied identity and permission headers are never trusted.

## Endpoints

- `GET /health`
- `GET /ready`
- `GET /metrics`
- `GET /api/v1/ping`
- `/api/v1/operator/*` (authenticated PAPER operator proxy)
