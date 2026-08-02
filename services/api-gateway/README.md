# api-gateway

REST API gateway

## Customer trading desk

Open `http://localhost:3022/` after starting the stack. The same-origin web
client provides authenticated portfolio valuation, market quotes, market and
limit order entry, order cancellation, and account risk controls. Access
tokens are kept in browser session storage and are cleared when the trader
signs out or the browser session ends.

## Endpoints

- `GET /health`
- `GET /ready`
- `GET /metrics`
- `GET /api/v1/ping`

## Customer trading API

- `POST /api/v1/auth/{register|verify-email|login|refresh|logout|...}`
- `GET|PATCH /api/v1/users/me`
- `GET|POST /api/v1/paper-account`
- `GET|PATCH /api/v1/risk`
- `GET|POST /api/v1/orders[/{orderId}[/cancel|/fills]]`
- `GET /api/v1/portfolio[/valuation|/ledger]`
- `GET /api/v1/quotes[/{symbol}]`

Product routes require `Authorization: Bearer <access-token>`. The gateway
verifies the user-management access token and derives `X-User-ID` from its
subject, so callers cannot select another account through a header. It forwards
idempotency keys, preserves domain status codes, and maps unavailable or timed
out dependencies to `502` or `504`.
