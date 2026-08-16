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

`EMA_CROSSOVER` requires a fresh EMA 8/21 cross on the latest closed H1 candle,
agreement with the closed-candle H4 trend, and at least 0.5 pip separation to
filter numerical noise. It emits no repeated signal after the crossover candle.

`TREND_CONTINUATION` calculates Wilder ADX 14, +DI, and -DI from closed OHLC
candles. It requires H1 ADX >= 20, H4 ADX >= 25, matching directional movement,
and a latest-candle H1 breakout beyond the preceding candle's range.

## Endpoints

- `GET /health`
- `GET /ready`
- `GET /metrics`
- `GET /api/v1/ping`
- `POST /api/v1/decisions/evaluate`
- `POST /api/v1/strategies/ema-pullback/evaluate`
- `POST /api/v1/strategies/ema-crossover/evaluate`
- `POST /api/v1/strategies/trend-continuation/evaluate`
