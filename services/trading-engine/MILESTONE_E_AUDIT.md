# Milestone E repair audit

This branch remains PAPER-only and delegates every evaluation to the Milestone D
coordinator. It contains no broker dependency, live-order endpoint, or live
execution interface.

## Deterministic-clock repair

The checkpoint constructed `paperRuntime.now` with `time.Now` even when the
application/coordinator had an injected clock. The end-to-end fixture was based
on `2026-08-20T12:00:00Z`, while runtime evidence used the Docker host clock near
`2026-08-20T21:49:14Z`. The runtime now obtains time exclusively through
`app.currentTime`, and the provider fixture derives readiness, quote, H1, H4,
cycle, started, and completed timestamps from that same clock.

Regression coverage requires both a fixed-clock accepted flow and fail-closed
`STALE_QUOTE` rejection. No market-data freshness, future-time, closed-candle,
risk, readiness, idempotency, or exposure gate was loosened.

A second, earlier contract defect was then isolated: `newRuntimeCycle` prefixed
the output of `fingerprint`, which is formatted as `sha256:<hex>`. The resulting
request ID contained `:`, a character forbidden by `validPaperIdentifier`, so
Milestone D returned `INVALID_MARKET_DATA` at its first input gate before quote
or candle validation. Runtime IDs now use `paper-cycle-<hex>` while retaining
the same SHA-256 identity material.

The Milestone D request schema has no separate market-data-validated flag,
quote symbol, supplied spread, or candle fingerprint fields. Pair identity is
bound by the selected setup and readiness decision; spread is derived from
bid/ask; candle integrity is established by strict chronological closed-candle
validation. The runtime provider does not invent or broadly accept additional
fields because strict JSON decoding would correctly reject them.

## Implemented checkpoint capabilities

- Disabled-by-default, explicit PAPER configuration and allowlists.
- Deterministic UTC H1/H4 boundary calculations and configuration fingerprints.
- Deterministic cycle IDs.
- Bounded queue and worker count.
- Redis leader lease, monotonically increasing fencing token, and cycle locks.
- Redis same-pair locks with owner-checked release.
- Redis server-time clock-skew detection.
- Chronologically sorted, recovery-window-bounded missed-cycle enumeration.
- Pre-order fencing verification inside the Milestone D coordinator.
- Fencing verification immediately after Milestone D evaluation.
- Bounded retry and cycle timeout.
- Bounded-cardinality Prometheus event, queue, leader, and duration metrics.
- Read-only runtime status and recent-cycle endpoints.
- Runtime readiness separated from process liveness.
- Conservative Kubernetes and Helm defaults.

## Known gaps against the complete Milestone E specification

The following remain blockers before Milestone E can be considered complete or
published:

- Cycle snapshots are appended to the bounded recent-event list but a dedicated
  durable, immutable audit stream remains an operational follow-up.
- Deterministic retry jitter is not added; bounded exponential backoff is used.
- Exact next-boundary timers use bounded lease-renewal scheduling rather than a
  separate timer per pair/timeframe.
- Authenticated and audited pause/resume is intentionally absent because no
  suitable operational authorization boundary has been established.
- London Breakout session eligibility remains owned by the strategy-engine
  contract (08:00-10:59 UTC entry candles); the runtime never fabricates or
  overrides a Strategy Manager decision.

Authoritative Docker validation (`go build -o /tmp/trading-engine ./`, tests,
race tests, and vet) is required after this repair. Do not publish or merge this
checkpoint until that validation passes and the remaining blockers are closed.
