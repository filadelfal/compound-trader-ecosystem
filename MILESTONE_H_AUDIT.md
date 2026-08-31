# Milestone H Audit — Reliable PAPER Market-Data Integration and Local Staging Stack

## Baseline and scope

Milestone H starts from the independently validated Milestone G commit:

`dd4d79421b12c5bc8d4b7f15999bb5ce9686165b`

Branch:

`feat/mvp-paper-market-data-staging`

This milestone extends the existing market-data integrity service. It does not
replace the prior quote validation, Redis storage, H1/H4 candle aggregation, or
trading-engine safety gates.

## PAPER boundary

The implementation is PAPER-only.

- No broker SDK.
- No broker credentials.
- No LIVE mode.
- No live-order endpoint.
- No execution authority is granted to the market-data service.
- No paid infrastructure or paid provider is required.
- NO_TRADE/fail-closed remains the safe downstream behavior when data is not
  fresh and ready.

Supported symbols remain exactly `EURUSD`, `GBPUSD`, and `USDJPY`. Derived
timeframes remain exactly `H1` and `H4`. London Breakout remains limited by the
trading-engine strategy layer to EURUSD and GBPUSD.

## Provider-neutral trust boundary

`PaperQuoteProvider` is a narrow ingestion interface that can only yield quote
data. It has no order, position, account, credential, or execution methods.

Milestone H ships only `DeterministicFixtureProvider`, a local simulator. Its
sequence is deterministic by symbol/price progression while timestamps are
provided by an injected clock so tests remain reproducible and staging events
remain fresh.

Provider output is not trusted. It is passed through `QuoteIngestor`, the same
strict validation boundary used by the HTTP quote endpoint.

## Validation and integrity

The quote boundary enforces:

- strict JSON fields;
- allowlisted symbols;
- finite positive bid/ask;
- ask > bid;
- ISO-8601 observation timestamps;
- maximum age and future skew;
- maximum spread;
- deterministic duplicate detection;
- out-of-order rejection;
- conflicting same-timestamp event rejection;
- Redis failure => 503/fail closed;
- candle-store failure => 503/fail closed.

Redis persists the latest accepted quote, so a replay of the latest provider
event after process restart is idempotently recognized. This milestone does not
claim multi-region distributed serialization; staging uses one market-data
writer. A future scale-out change must introduce an atomic Redis transaction or
Lua compare-and-set before enabling multiple concurrent writers.

## Reliability controls

The feed runner adds:

- bounded queue capacity and explicit backpressure;
- bounded retry count;
- exponential backoff with bounded jitter;
- provider request timeout and abort propagation;
- consecutive-failure circuit breaker and cooldown;
- feed health/freshness state;
- graceful abort and provider close on SIGTERM/SIGINT;
- bounded shutdown timeout.

All values have startup-validated ranges. If `MARKET_DATA_FEED_REQUIRED=true`
while feed mode is disabled, configuration is rejected at startup.

## Readiness vs liveness

`/health` remains process liveness only.

`/ready` continues to require Redis/Postgres dependencies and, when the PAPER
feed is required, additionally requires a recent successful ingestion and a
closed provider circuit. Stale/degraded feed state returns HTTP 503. This
prevents process liveness from being mistaken for trading-data readiness.

## Observability

Prometheus metrics cover:

- accepted/idempotent ingest results;
- provider/validation/storage failures;
- duplicate count;
- bounded-queue backpressure;
- queue depth;
- circuit-open state;
- feed readiness.

Labels use bounded result/reason vocabularies only. Provider names, tokens,
request IDs, timestamps, symbols supplied by arbitrary clients, raw errors and
secrets are not metric labels.

`observability/market-data-alerts.yml` defines alerts for stale/not-ready feed,
backpressure, repeated provider failures and open circuit.

## Local staging

`compose.staging.yaml`:

- removes the market-data host port;
- requires the local deterministic fixture feed;
- makes market-data read-only with `/tmp` tmpfs;
- enables `no-new-privileges`;
- bounds Docker logs;
- depends on healthy Redis/Postgres;
- keeps restart policy `unless-stopped`;
- makes trading-engine depend on healthy market-data in staging.

The market-data image runs as the unprivileged Node user. Its container health check targets `/ready`, while `/health` remains a separate process-liveness endpoint.

Development behavior is preserved: feed mode defaults to `disabled` and feed
freshness is not required unless explicitly enabled.

## Required authoritative validation before commit/push

From `services/market-data`:

- `npm ci` (or `npm install` if no lockfile is present)
- `npm test`
- `npm run build`
- `npm run lint`

From `services/trading-engine`:

- `go build -o trading-engine.exe ./` on Windows (remove binary afterward)
- `go test ./...`
- `go test -race ./...` using Linux/Docker if Windows CGO is unavailable
- `go vet ./...`

From repository root:

- `docker compose config --quiet`
- `docker compose -f compose.yaml -f compose.staging.yaml config --quiet`
- `git diff --check`
- `git status --short`

Only commands actually executed should be reported as PASS. No publish step is
authorized until all mandatory gates pass.

## Known limitation

The Redis quote store preserves deterministic restart deduplication for the
latest event but is not advertised as a multi-writer distributed consensus
mechanism. Milestone H staging intentionally runs a single market-data writer.
This limitation is explicit rather than hidden behind a false exactly-once
claim.
