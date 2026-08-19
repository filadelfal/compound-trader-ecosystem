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
