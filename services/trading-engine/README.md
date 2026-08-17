# trading-engine

Risk approval and idempotent paper execution. There is no live broker adapter.

Position sizing is fail closed. It supports EURUSD, GBPUSD, and USDJPY, risks
at most 1% of equity, validates stop placement, floors size to the broker lot
step, and enforces daily-loss, drawdown, open-position, pair-exposure, trading
status, and kill-switch gates. The supplied pip value must already be converted
to the account currency by the broker adapter. Approval does not place an order.

Paper execution accepts only explicit `PAPER` requests that already have risk
approval. It rejects stale quotes, invalid price structure, reward-to-risk below
1:2, active kill switches, unsupported instruments or strategies, and malformed
input. Redis idempotency records are retained for seven days, so retrying the
same request ID returns the original immutable paper order instead of creating
another one.

## Endpoints

- `GET /health`
- `GET /ready`
- `GET /metrics`
- `GET /api/v1/ping`
- `POST /api/v1/risk/position-size`
- `POST /api/v1/paper/orders`
