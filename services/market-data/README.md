# Market Data

Validated quote-ingestion boundary for the Compound Trader automated paper
trading pipeline. The first supported instruments are `EURUSD`, `GBPUSD`, and
`USDJPY`.

## Endpoints

- `GET /health`
- `GET /ready`
- `GET /metrics`
- `GET /api/v1/ping`
- `POST /api/v1/quotes`
- `GET /api/v1/quotes/:symbol/latest`
- `GET /api/v1/candles/:symbol/:timeframe`

Accepted quotes contain provider, symbol, bid, ask, and the provider's ISO-8601
observation time. The service records its own receipt time, calculates spread
and spread in pips, and persists the latest accepted quote in Redis. It rejects
unsupported symbols, crossed markets, stale/future quotes, duplicate events,
out-of-order events, and spreads outside the configured safety limit.

Each newly accepted quote also updates UTC-aligned `H1` and `H4` midpoint
candles. Completed candles and detected missing candle intervals are retained in
Redis. Candle queries return the current candle, recent completed candles, and
gap records; `limit` is capped at 1,000 records.

## Safety configuration

- `MAX_QUOTE_AGE_SECONDS` (default `30`)
- `MAX_QUOTE_FUTURE_SKEW_SECONDS` (default `5`)
- `MAX_SPREAD_PIPS` (default `5`)

These defaults are development safeguards. Production values must be versioned
and validated against the selected provider and broker before live execution.

## Validate

```bash
npm ci
npm test
npm run build
```
