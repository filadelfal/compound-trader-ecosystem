# trading-engine

Order execution service

## Endpoints

- `GET /health`
- `GET /ready`
- `GET /metrics`
- `GET /api/v1/ping`
- `POST /api/v1/orders`
- `GET /api/v1/orders?limit=50`
- `GET /api/v1/orders/{orderId}`
- `POST /api/v1/orders/{orderId}/cancel`
- `GET /api/v1/orders/{orderId}/fills`
- `GET /api/v1/orders/{orderId}/events`
- `POST /internal/v1/orders/{orderId}/fills`
- `GET /api/v1/portfolio`
- `GET /api/v1/portfolio/ledger?limit=100`
- `POST /internal/v1/accounts/{userId}/cash-adjustments`
- `GET|POST /api/v1/paper-account`
- `GET|PATCH /api/v1/risk`
- `GET /api/v1/portfolio/valuation`

Order endpoints require the authenticated user identifier forwarded by the API
gateway in the `X-User-ID` header. Order creation accepts an
`Idempotency-Key` header (or `clientOrderId` in the JSON body), so retries
cannot create duplicate orders for the same user. An identical replay returns
the original order with `200` and `Idempotent-Replayed: true`, even after its
quote or lifecycle state changes. Reusing the key with different order
economics returns `409 idempotency_conflict`.

The internal fill endpoint requires `X-Execution-Key` to match
`EXECUTION_API_KEY`. Execution reports are idempotent by `executionId`,
atomically update filled quantity and volume-weighted average price, reject
overfills, and enforce buy/sell limit prices.

Pre-trade controls use `MAX_ORDER_QUANTITY` and `MAX_ORDER_NOTIONAL`.
Notional checks use exact decimal arithmetic rather than floating point.

Confirmed fills write balanced security and cash legs to an immutable portfolio
ledger in the same PostgreSQL transaction as the order state transition.
Portfolio positions and settled USD cash are derived from that ledger. Buys
cannot exceed settled cash, and sells cannot create a negative position unless
`ALLOW_SHORT_SELLING=true`.

Cash adjustments are settlement-only, require the internal execution
credential, and are idempotent by `externalId`. Each adjustment records
balanced cash and external legs.

Portfolio valuation uses the latest quote for every open position from
`MARKET_DATA_URL`. Responses include market value, cost basis, realized and
unrealized P&L, plus `complete`, `missingSymbols`, and `staleSymbols` so
clients cannot mistake partial or stale pricing for a complete valuation.

When `PAPER_TRADING_ENABLED=true`, every accepted order is atomically queued
for durable paper execution. The worker buys at the fresh ask and sells at the
fresh bid, waits for limit orders to become marketable, retries missing or stale
quotes with bounded exponential backoff, and uses a deterministic execution ID
so recovery after a crash cannot duplicate a fill. Unfunded buys and uncovered
sells are permanently rejected.

Order acceptance also atomically reserves conservative buying power (the limit
notional, or fresh ask notional for a market buy) or the requested sell
quantity. Competing orders cannot overcommit cash or shares. Portfolio
responses expose settled cash, reserved cash, buying power, and reserved versus
available position quantity. Partial fills consume reservations proportionally;
cancellation, rejection, and completion release or settle the remainder.

Paper-account activation is authenticated and one-time. It creates an account
record and balanced initial-capital ledger entry in one transaction; concurrent
or repeated activation returns the original account without adding cash again.

Every order has an immutable, authenticated activity timeline. Database
triggers record creation, each broker routing attempt, every execution, and
status transitions in the same transaction as the state change, so clients can
reconstruct lifecycle history without relying on transient logs.

Each account has persistent safety controls for trading enablement, maximum
open orders, gross exposure, and daily realized loss. Enforcement runs inside
the same account-scoped transaction lock as order reservations, so concurrent
requests cannot race through a limit. Turning off `tradingEnabled` acts as a
kill switch: it cancels all working orders and releases their reservations in
the profile-update transaction.

Quantities and prices are JSON strings to preserve decimal precision:

```json
{
  "symbol": "AAPL",
  "side": "buy",
  "type": "limit",
  "quantity": "10",
  "limitPrice": "182.50"
}
```
