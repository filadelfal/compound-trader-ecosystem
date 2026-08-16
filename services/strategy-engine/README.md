# strategy-engine

Indicators, strategies, and the fail-closed trading decision boundary.

The decision boundary returns `NO_TRADE` unless market-data integrity,
timeframe alignment, strategy conditions, price structure, reward-to-risk, and
every mandatory risk gate pass. A `TRADE_SETUP` is an eligible setup for later
risk sizing and execution approval; it is not an instruction to place an order.

## Endpoints

- `GET /health`
- `GET /ready`
- `GET /metrics`
- `GET /api/v1/ping`
- `POST /api/v1/decisions/evaluate`
