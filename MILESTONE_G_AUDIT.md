# Milestone G — Secure Operator Authentication and Staging Readiness

## Safety boundary

Milestone G remains PAPER-only. `NO_TRADE` remains the default for uncertain trading state. Operator authority changes administrative state only and never bypasses market-data, strategy, readiness, reconciliation, exposure, daily-loss, drawdown, position-count, or reward-to-risk gates.

There is no broker credential, broker SDK, broker adapter, `LIVE` mode, live-order endpoint, or operator permission capable of trading. No paid infrastructure was provisioned or deployed.

## Architecture and trust boundaries

User-management remains the identity issuer. The API gateway verifies its existing HS256 access token: signature, fixed algorithm, issuer, audience, access-token type, subject, issued/expiry times, and roles. Only `admin` and `super_admin` are recognized. The gateway maps them to a fixed permission set and signs a short-lived, audience-restricted assertion for the trading engine. Client identity, email, role, and permission headers are ignored.

The trading engine independently verifies HS256, `operator-assertion.v1`, issuer `compound-api-gateway`, audience `compound-trading-engine`, a safe subject, known role, fixed permissions, `iat`, `nbf`, `exp`, and unique `jti`. `none`, unsupported algorithms, confusion, invalid signatures, missing claims, future issuance, premature use, expiry, excessive lifetime, and unknown roles/permissions fail closed. Tokens and secrets are never logged or returned.

Redis atomically consumes each `jti` with bounded retention. Redis outage or duplicate consumption rejects the request and makes readiness false.

## Permissions and separation of duties

- `PAPER_AUTOMATION_PAUSE`
- `PAPER_AUTOMATION_RESUME`
- `PAPER_KILL_SWITCH_ACTIVATE`
- `PAPER_KILL_SWITCH_RELEASE_REQUEST`
- `PAPER_RECONCILIATION_RUN`
- `PAPER_REPAIR_PREVIEW`
- `PAPER_REPAIR_APPLY`
- `PAPER_OPERATIONS_READ`

Authorization is default-deny and endpoint-specific. `admin` cannot apply repairs or request kill-switch release; those permissions require `super_admin`, represented downstream as `security_operator`. This is stronger single-operator authorization, not simulated dual approval. True multi-person approval remains a limitation.

## Protected endpoints

The gateway exposes `/api/v1/operator/*` and maps it to internal PAPER operations after authentication. The trading engine protects POST pause, resume, kill-switch activate, kill-switch release request, reconciliation run, repair preview, and repair apply; GET status, findings, ledger, rollup, and lifecycle journal; plus PAPER runtime status and recent cycles.

The assertion is required even if an internal client reaches the trading engine directly. Public `/health` reports process liveness only. Sensitive responses expose no Redis keys, secrets, or stack traces. Existing stores cap reads at 100 records and use stable ordering.

## Commands, replay, idempotency, and rate limiting

Mutation bodies are limited to 16 KiB, decoded as one strict JSON value, and reject unknown fields. A safe request ID, safe idempotency key, non-empty bounded reason, and timestamp within configured skew are required. Identity comes only from verified claims.

Redis stores immutable command results for 365 days. An identical retry with a fresh assertion returns the original result marked `idempotentReplay`; changed key reuse conflicts. Assertions are one-use. Calls are limited per verified operator in a bounded fixed window. IDs never appear as metric labels.

Pause and kill-switch activation are deterministic. Activation also pauses and makes operations unready. Release is rejected while blocking/critical reconciliation state exists and never resumes automation. Resume requires ready, consistent operations and an inactive kill switch. Reconciliation reuses existing bounded evidence and locking.

Repair preview fingerprints active-finding evidence. Apply requires the strongest permission and exact current preview fingerprint. It records a derived-state application without changing canonical orders, positions, prices, stops, targets, size, exit price, or realised P&L.

## Audit, health, metrics, and alerts

Accepted actions append immutable operator-attributed ledger events. Rejected authorization, malformed mutation, timestamp, rate-limit, unsafe-state, evidence mismatch, and idempotency attempts append bounded rejection events; ledger failure fails operations closed.

`paper_operator_security_events_total{event,reason}` has fixed bounded values and never labels identities, request/assertion IDs, idempotency keys, timestamps, paths, permissions, or raw errors. `paper_operator_security_ready` reports the authentication/replay boundary. Alerts cover outages, invalid signatures, replay, authorization denial, abuse, reconciliation/kill-switch state, and runtime readiness.

`/health` means liveness. `/ready` separately requires PostgreSQL, Redis, valid authentication configuration, replay storage, enabled-runtime readiness, and consistent/non-blocking operations. Security outage does not make liveness false. Existing graceful shutdown prevents readiness after process termination.

## Secrets and local staging

The gateway and trading engine require a distinct minimum-64-byte assertion secret; only HS256 is accepted. Lifetime is bounded to 5–300 seconds, clock skew to 0–30 seconds, replay retention to lifetime plus skew through 10 minutes, and rate settings to safe bounds. Startup errors do not print secrets.

`compose.staging.yaml` adds production secret requirements, dependency ordering, restart policy, read-only filesystems, `/tmp`, no-new-privileges, and bounded logs. Existing local Redis/PostgreSQL volumes remain. Changed images run non-root. This is local/staging configuration only; no deployment occurred.

## Known limitations

- HS256 shares a distinct gateway/trading secret; asymmetric rotation by key ID is future hardening.
- There is no true dual-person approval workflow.
- Repair apply is limited to evidence-matched derived-state acknowledgement because Milestone F has no independent derived-state materialization contract. It cannot alter canonical facts.
- No general open/closed-position listing or risk-summary HTTP endpoint exists. Operational status, findings, ledger, rollups, lifecycle journal, and runtime evidence are protected. Staging must keep the trading-engine port internal.

Historical or simulated PAPER evidence is not proof of future performance.
