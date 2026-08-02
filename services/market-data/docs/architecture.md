# Market Data Service Architecture

## Layers

- **API Layer** (`app/api/routes.py`): FastAPI routing and validation
- **Service Layer** (`app/services/*`): business logic, caching, failover orchestration
- **Repository Layer** (`app/repositories/*`): persistence operations
- **Provider Layer** (`app/providers/*`): market data provider adapters
- **Infrastructure Layer** (`app/db/*`, `app/cache.py`, middleware): DB, Redis, observability, security

## Data Flow

1. Request authenticated via JWT/API key.
2. Rate limit enforced in Redis.
3. Service layer performs operation.
4. Provider service fails over across configured providers.
5. Results persisted in PostgreSQL and cached in Redis.
6. Latest price updates broadcast over WebSocket subscriptions.

## Storage

- `symbols` table for tradable instruments
- `ohlcv` table for candle history at supported intervals

## Caching

- `latest_price:{symbol}` (short TTL)
- `active_symbols` (set)
- `provider_status:{provider}` (hash)
- `rate_limit:{identity}:{endpoint}:{window}` (counter)
