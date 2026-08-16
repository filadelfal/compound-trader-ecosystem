# trading-engine

Risk approval and, in a later milestone, broker order execution.

Position sizing is fail closed. It supports EURUSD, GBPUSD, and USDJPY, risks
at most 1% of equity, validates stop placement, floors size to the broker lot
step, and enforces daily-loss, drawdown, open-position, pair-exposure, trading
status, and kill-switch gates. The supplied pip value must already be converted
to the account currency by the broker adapter. Approval does not place an order.

## Endpoints

- `GET /health`
- `GET /ready`
- `GET /metrics`
- `GET /api/v1/ping`
- `POST /api/v1/risk/position-size`
