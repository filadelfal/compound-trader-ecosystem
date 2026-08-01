# api-gateway

REST API gateway

## Endpoints

- `GET /health`
- `GET /ready`
- `GET /metrics`
- `GET /api/v1/ping`
- `GET /api/v1/me` (valid access token required)
- `GET /api/v1/admin/ping` (`admin` role required)

## Authentication boundary

The gateway validates user-management access tokens with HS256, the configured
issuer and audience, and explicit access-token claims. Refresh tokens are
rejected. Trusted user identity and roles come only from the verified token.

Set `JWT_ACCESS_SECRET` to the same secret used by user-management. Production
startup rejects the built-in development secret.

## Service routing

Only explicit route families are proxied. `/api/v1/auth/*` reaches user
management for registration and session workflows. `/api/v1/users/*` requires
a valid gateway access token. The proxy applies a five-second timeout, limits
response size, does not follow redirects, filters response headers, and replaces
caller-supplied identity headers with identity derived from verified claims.
