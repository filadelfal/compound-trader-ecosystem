# Milestone F audit: PAPER operations control plane

Status: implementation candidate; authoritative Go 1.23 Docker validation is required before publication.

## Implemented

- PAPER-only append ledger with schema version, deterministic payload/state fingerprints, correlation and cycle identity, actor type, reason codes, exact replay, changed-reuse rejection, chain verification, bounded reads, and 365-day Redis retention.
- Startup and periodic reconciliation across paper orders, positions, lifecycle journals, runtime cycles, exposure markers, risk-derived state, and the operations ledger.
- Findings for missing/orphan/duplicate records, invalid lifecycle transitions, conflicting closures, pair exposure and position-count drift, realized P&L/daily-loss/drawdown drift, unsupported identity, live-mode evidence, and broken ledger chains.
- Critical evidence activates an in-process PAPER kill switch. Blocking or unavailable reconciliation makes the operations gate and service readiness fail closed.
- A distributed Redis reconciliation lock prevents concurrent replicas from reconciling simultaneously. Lock contention and Redis errors fail closed.
- Redis evidence indexes are maintained for paper orders and positions; journal, ledger, index, and cycle reads are bounded.
- Deterministic daily PAPER rollups include equity/P&L, drawdown, trade outcome, exit-reason, NO_TRADE/rejection, grouping, sample-window, sample-size, and evidence fingerprint fields.
- Read-only status, findings, ledger, and dated-rollup endpoints. Methods other than GET are rejected.
- Bounded-cardinality readiness/event metrics and the pre-existing runtime metrics remain available at `/metrics`.
- Accepted Milestone D flows remain behind the Milestone E fencing gate and additionally require successful operations reconciliation. They record PAPER order and position events; no coordinator rule is duplicated or bypassed.
- Explicit source-level guard and tests reject live operations event types. No broker adapter, credential, order route, or live authorization was added.

## Authentication boundary decision

Pause/resume, finding resolution, kill-switch release, and derived repair application are deliberately not exposed as HTTP endpoints. The trading-engine is not currently behind a verified boundary that propagates a cryptographically authenticated operator identity and role from user-management. Trusting client-supplied headers would be unsafe. The internal command contract therefore defaults to `OPERATOR_AUTHENTICATION_BOUNDARY_UNAVAILABLE`.

A future milestone may expose these commands only after the gateway verifies a signed identity, enforces an operator role, and passes an auditable actor identifier. Uncertain authorization must continue to default to paused/denied.

## Retention and boundedness

- Operations records use a 365-day Redis TTL.
- One reconciliation pass accepts at most 1,000 order IDs, 1,000 position IDs, 1,000 ledger events, 1,000 lifecycle events per position, and 100 recent runtime cycles.
- Public list endpoints return at most 50 items; store APIs reject limits above 100.
- Prometheus labels use fixed event/severity/reason vocabularies; pair/timeframe labels remain allowlisted by the Milestone E runtime.

## Known limitations / blockers

- Authenticated operational mutations are intentionally unexposed for the reason above.
- Derived repair application is intentionally unexposed; automatic rewriting of authoritative orders, positions, journals, or historical evidence is prohibited.
- This workspace has neither Go nor Docker. Formatting, build, unit tests, race tests, and vet have not been executed here. Do not treat this checkpoint as publishable until the authoritative Docker command passes.

## Required authoritative validation

From the repository root:

```powershell
docker run --rm `
  -v "${PWD}:/workspace" `
  -v "compound-trader-go-mod:/go/pkg/mod" `
  -v "compound-trader-go-build:/root/.cache/go-build" `
  -w /workspace/services/trading-engine `
  golang:1.23-bookworm `
  bash -c "gofmt -w operations.go operations_redis.go operations_test.go main.go automation.go paper.go lifecycle.go && go version && go build -o /tmp/trading-engine ./ && go test ./... && go test -race ./... && go vet ./... && git diff --check"
```

Review the formatting diff before committing it. Do not generate or commit `services/trading-engine/trading-engine`.
