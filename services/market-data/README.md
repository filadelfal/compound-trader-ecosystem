# Market Data

Validated, provider-neutral **PAPER-only** market-data boundary for Compound
Trader. Supported instruments are `EURUSD`, `GBPUSD`, and `USDJPY`; derived
candles are restricted to UTC-aligned `H1` and `H4`.

## Endpoints

- `GET /health` — process liveness only
- `GET /ready` — dependency readiness and, when required, feed freshness
- `GET /metrics`
- `GET /api/v1/ping`
- `POST /api/v1/quotes`
- `GET /api/v1/quotes/:symbol/latest`
- `GET /api/v1/candles/:symbol/:timeframe`

Quote ingestion validates a strict JSON shape, allowed symbol, finite positive
bid/ask values, crossed markets, observation timestamps, freshness/future
skew, spread limits, ordering and deterministic duplicate/conflict behavior.
Redis preserves the latest accepted event across restarts. Accepted and
idempotent duplicate events feed the existing H1/H4 candle aggregator.

## PAPER feed runner

`MARKET_DATA_FEED_MODE=fixture` enables the deterministic local fixture
provider. It is intentionally not a broker, broker SDK or execution adapter.
The provider-neutral `PaperQuoteProvider` interface can later be implemented by
an approved PAPER data source without granting order authority.

The runner adds:

- bounded queue/backpressure;
- bounded retries and exponential backoff with jitter;
- per-request timeout;
- circuit breaker/cooldown;
- feed freshness tracking;
- graceful shutdown;
- bounded Prometheus metrics without provider/token/request identifiers.

Development defaults keep the feed disabled. Staging requires the deterministic
fixture and removes the market-data host port.

## Safety configuration

Existing integrity controls:

- `MAX_QUOTE_AGE_SECONDS` (default `30`)
- `MAX_QUOTE_FUTURE_SKEW_SECONDS` (default `5`)
- `MAX_SPREAD_PIPS` (default `5`)

Feed controls:

- `MARKET_DATA_FEED_MODE=disabled|fixture`
- `MARKET_DATA_FEED_REQUIRED` (default `false`)
- `MARKET_DATA_FEED_FRESHNESS_SECONDS` (default `15`)
- `MARKET_DATA_FEED_INTERVAL_MS` (default `1000`)
- `MARKET_DATA_FEED_QUEUE_CAPACITY` (default `64`)
- `MARKET_DATA_FEED_REQUEST_TIMEOUT_MS` (default `2000`)
- `MARKET_DATA_FEED_MAX_RETRIES` (default `3`)
- `MARKET_DATA_FEED_BACKOFF_BASE_MS` / `MARKET_DATA_FEED_BACKOFF_MAX_MS`
- `MARKET_DATA_FEED_CIRCUIT_FAILURES` / `MARKET_DATA_FEED_CIRCUIT_COOLDOWN_MS`

If a feed is required but disabled, startup configuration fails. If the
required feed is stale, unavailable or its circuit is open, `/ready` returns
503 and downstream PAPER automation must remain fail-closed/NO_TRADE.

## Local staging

From the repository root, supply temporary validation secrets and validate:

```bash
docker compose config --quiet
docker compose -f compose.yaml -f compose.staging.yaml config --quiet
```

Then start only the relevant stack:

```bash
docker compose -f compose.yaml -f compose.staging.yaml up -d \
  postgres redis market-data trading-engine prometheus
```

Check `market-data` liveness/readiness from inside the Compose network or with
`docker compose exec market-data` because staging removes its host port.

## Validate

```bash
npm ci
npm test
npm run build
npm run lint
```

This milestone remains PAPER-only. It adds no broker credentials, broker SDK,
LIVE mode, live-order endpoint, or paid infrastructure dependency.
