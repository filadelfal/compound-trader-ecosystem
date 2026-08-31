# Milestone H Local Staging Guide

Milestone H is a local, PAPER-only market-data staging stack. It does not contain broker credentials, a broker SDK, LIVE mode, or order-execution authority.

## Safety baseline

- Branch: `feat/mvp-paper-market-data-staging`
- Base commit: `dd4d79421b12c5bc8d4b7f15999bb5ce9686165b`
- Symbols: `EURUSD`, `GBPUSD`, `USDJPY`
- Derived candle timeframes: `H1`, `H4`
- Feed mode in staging: deterministic local fixture only

## Staging configuration

The staging overlay sets `MARKET_DATA_FEED_MODE=fixture` and `MARKET_DATA_FEED_REQUIRED=true`. The fixture emits deterministic quotes through the same validation/Redis/candle-ingestion boundary as HTTP quotes.

`/health` is process liveness. `/ready` requires service dependencies and a fresh feed when the feed is required. The Docker health check targets `/ready`, so staging dependencies do not treat a stale PAPER feed as usable market data.

## Validate the Compose model

Use temporary validation secrets in your shell; do not write them into tracked files. Then run:

```powershell
docker compose config --quiet
docker compose -f compose.yaml -f compose.staging.yaml config --quiet
```

## Start the local staging subset

After authoritative tests pass, the staging subset may be started locally with:

```powershell
docker compose -f compose.yaml -f compose.staging.yaml up -d postgres redis market-data trading-engine
```

Check readiness:

```powershell
docker compose -f compose.yaml -f compose.staging.yaml ps
curl.exe -i http://localhost:3004/health
```

The staging overlay intentionally removes the market-data host port, so direct host access may be unavailable under the combined staging model. Use Compose service health/`docker compose exec` or temporarily run the base development model when inspecting its HTTP endpoints.

## Fail-closed checks

A required feed that is disabled is rejected at startup. A stale feed, open provider circuit, Redis/Postgres outage, malformed quote, stale/future quote, excessive spread, out-of-order event, same-timestamp conflict, or candle-store failure prevents the market-data path from being considered ready/accepted.

The trading engine retains its own quote/candle freshness, readiness, reconciliation, risk, exposure and kill-switch gates. Market-data readiness does not grant permission to trade.

## Shutdown

Use:

```powershell
docker compose -f compose.yaml -f compose.staging.yaml down
```

The market-data process handles `SIGTERM`/`SIGINT`, stops the feed, closes dependencies, and has a bounded shutdown timeout.
