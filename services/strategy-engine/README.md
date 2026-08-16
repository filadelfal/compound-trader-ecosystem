# strategy-engine

Indicators, strategies, and the fail-closed trading decision boundary.

The decision boundary returns `NO_TRADE` unless market-data integrity,
timeframe alignment, strategy conditions, price structure, reward-to-risk, and
every mandatory risk gate pass. A `TRADE_SETUP` is an eligible setup for later
risk sizing and execution approval; it is not an instruction to place an order.

The first deterministic strategy is `EMA_PULLBACK`. It uses EMA 8 and EMA 21
on closed H1 and H4 candles, requires both timeframes to agree, and requires a
completed H1 pullback-and-recovery pattern. Missing, open, or unordered candles
fail closed without producing a setup.

## Endpoints

- `GET /health`
- `GET /ready`
- `GET /metrics`
- `GET /api/v1/ping`
- `POST /api/v1/decisions/evaluate`
- `POST /api/v1/strategies/ema-pullback/evaluate`
