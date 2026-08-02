# market-data

Production-grade async Market Data Service for Compound Trader Ecosystem.

## Stack

- FastAPI
- Python 3.13+
- SQLAlchemy 2.x + Alembic
- PostgreSQL
- Redis
- Pydantic v2
- Structlog JSON logging
- Prometheus metrics

## Features

- Symbol CRUD for forex, crypto, commodities, and indices
- Multi-provider market feed with automatic failover (Twelve Data, Alpha Vantage, Polygon, Binance, MT5)
- Historical OHLCV persistence and query APIs
- Latest price cache and provider status cache in Redis
- WebSocket price broadcasting
- JWT + API key auth and Redis-backed rate limiting
- Health/readiness/liveness/metrics endpoints

## Endpoints

- `GET /health`
- `GET /ready`
- `GET /live`
- `GET /metrics`
- `GET /api/v1/ping`
- `POST /api/v1/symbols`
- `GET /api/v1/symbols`
- `GET /api/v1/symbols/{symbol}`
- `PUT /api/v1/symbols/{symbol}`
- `DELETE /api/v1/symbols/{symbol}`
- `GET /api/v1/prices/latest/{symbol}`
- `POST /api/v1/historical/{symbol}`
- `GET /api/v1/historical/{symbol}/{interval}`
- `POST /api/v1/historical/fetch/{symbol}/{interval}`
- `GET /api/v1/providers/status`
- `GET /api/v1/active-symbols`
- `WS /api/v1/ws/prices`

## Environment

- `PORT`
- `SERVICE_NAME`
- `DATABASE_URL`
- `REDIS_URL`
- `JWT_SECRET`
- `JWT_ALGORITHM`
- `API_KEYS` (comma-separated)
- `RATE_LIMIT_REQUESTS`
- `RATE_LIMIT_WINDOW_SECONDS`
- `CORS_ALLOW_ORIGINS`
- `CORS_ALLOW_METHODS`
- `CORS_ALLOW_HEADERS`
- `PROVIDER_PRIORITY` (comma-separated)
- `TWELVEDATA_API_KEY`
- `ALPHAVANTAGE_API_KEY`
- `POLYGON_API_KEY`
- `MT5_BASE_URL`

## Local run

```bash
python -m venv .venv
source .venv/bin/activate
pip install -r requirements-dev.txt
alembic upgrade head
uvicorn app.main:app --host 0.0.0.0 --port 3004
```

## Test

```bash
pytest
```
