# trading-engine

Risk approval and idempotent paper execution. There is no live broker adapter.

Position sizing is fail closed. It supports EURUSD, GBPUSD, and USDJPY, risks
at most 1% of equity, validates stop placement, floors size to the broker lot
step, and enforces daily-loss, drawdown, open-position, pair-exposure, trading
status, and kill-switch gates. The supplied pip value must already be converted
to the account currency by the broker adapter. Approval does not place an order.

Paper execution accepts only explicit `PAPER` requests that already have risk
approval. It rejects stale quotes, invalid price structure, reward-to-risk below
1:2, active kill switches, unsupported instruments or strategies, and malformed
input. Redis idempotency records are retained for seven days, so retrying the
same request ID returns the original immutable paper order instead of creating
another one.

Paper positions can be opened only from a previously accepted paper order.
Every opening and quote event requires a unique event ID. Exact retries are
idempotent; conflicting reuse is rejected. Quote processing uses bid to close
BUY positions and ask to close SELL positions, rejects stale or out-of-order
quotes and spreads above five pips, applies stop-loss before take-profit, and
calculates deterministic realized or unrealized P&L using the supplied
account-currency pip value.
Position snapshots are appended to a 30-day immutable paper journal.

Deterministic backtests accept only closed chronological H1/H4 candles and
explicit spread, slippage, and commission assumptions. Invalid, duplicate,
unordered, gapped, or future data fails closed. Runs are isolated by pair,
timeframe, strategy, and strategy version and include SHA-256 configuration,
dataset, and result fingerprints. Same-candle stop/target ambiguity is resolved
to the adverse stop-loss outcome. Backtest evidence is historical simulation,
not a promise of future performance.

Read-only performance evidence verifies the immutable run fingerprints before
reporting trade count, wins, losses, breakeven trades, win rate, gross profit
and loss, costs, net P&L, profit factor, averages, expectancy, drawdown,
streaks, and return-to-drawdown. Evidence is grouped by pair, strategy version,
timeframe, month, and in-sample/out-of-sample classification. Rejected
`NO_TRADE` runs remain visible. Small samples and undefined ratios are reported

Sensitive PAPER operations and runtime evidence require a verified gateway operator assertion. Administrative commands are permission-scoped, replay-protected, idempotent, rate-limited, and immutably audited. `/health` is process liveness; `/ready` additionally requires authentication and replay protection. No operator command grants live execution authority.
explicitly and never converted into trading approval.

Walk-forward validation consumes only fingerprint-verified backtest runs and
valid performance evidence. It requires contiguous, chronological,
non-overlapping training/test folds with isolated strategy, version, pair and
timeframe identity. Configurable readiness gates evaluate out-of-sample sample
size, fold profitability, expectancy, profit factor, drawdown, degradation and
loss streaks. Every missing, undefined or failed gate returns `NOT_READY`.
`READY` means paper-strategy evidence passed the configured historical gates;
it never authorizes live trading or guarantees future results.

The paper-automation coordinator accepts only explicit `PAPER` requests. It
requires one conflict-free setup, a fresh fingerprint-valid matching `READY`
decision, current closed H1/H4 candles, a bounded-spread quote, and a fresh pass
through every account risk gate. Accepted orders and positions use the existing
paper stores and an immutable audit. No live-order interface exists.

## Endpoints

- `GET /health`
- `GET /ready`
- `GET /metrics`
- `GET /api/v1/ping`
- `POST /api/v1/risk/position-size`
- `POST /api/v1/paper/orders`
- `POST /api/v1/paper/positions`
- `POST /api/v1/paper/positions/quotes`
- `GET /api/v1/paper/journal?orderId=...`
- `POST /api/v1/backtests`
- `POST /api/v1/backtests/performance`
- `POST /api/v1/backtests/walk-forward`
- `POST /api/v1/paper-automation/evaluate`

## Resilient PAPER automation runtime

The background runtime is disabled by default and can only run in `PAPER` mode.
It schedules UTC-aligned, fully closed H1/H4 candles after a settlement delay,
then obtains a complete fingerprinted request and passes it to the Milestone D
coordinator. It does not construct orders or positions and exposes no broker or
live-execution interface.

Redis provides renewable single-leader leasing, monotonically increasing
fencing tokens, and deterministic per-cycle locks. The worker queue and worker
count are bounded. Lease, lock, request-provider, storage, stale-state, and
configuration uncertainty fail closed. Read-only operational endpoints are:

- `GET /api/v1/paper-automation/runtime`
- `GET /api/v1/paper-automation/runtime/cycles`

The runtime is enabled only when `PAPER_AUTOMATION_ENABLED=true`,
`PAPER_AUTOMATION_MODE=PAPER`, and `PAPER_AUTOMATION_REQUEST_URL` points to the
internal service that assembles the complete Milestone D request. Invalid
provider identity is rejected.

Optional conservative settings include `PAPER_AUTOMATION_PAIRS`,
`PAPER_AUTOMATION_TIMEFRAMES`, `PAPER_AUTOMATION_SETTLEMENT_DELAY`,
`PAPER_AUTOMATION_READINESS_MAX_AGE`, `PAPER_AUTOMATION_RECOVERY_WINDOW`,
`PAPER_AUTOMATION_WORKERS`, `PAPER_AUTOMATION_QUEUE_CAPACITY`,
`PAPER_AUTOMATION_CYCLE_TIMEOUT`, `PAPER_AUTOMATION_MAX_RETRIES`,
`PAPER_AUTOMATION_INITIAL_RETRY_DELAY`, `PAPER_AUTOMATION_MAX_RETRY_DELAY`,
`PAPER_AUTOMATION_LEADER_LEASE`, `PAPER_AUTOMATION_LEADER_RENEWAL`, and
`PAPER_AUTOMATION_CLOCK_SKEW`. Invalid values prevent startup.

Historical readiness and PAPER results do not guarantee future performance.
