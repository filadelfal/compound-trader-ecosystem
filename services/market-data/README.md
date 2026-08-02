# market-data

Market price feeds

## Endpoints

- `GET /health`
- `GET /ready`
- `GET /metrics`
- `GET /api/v1/ping`
- `POST /internal/v1/quotes`
- `GET /api/v1/quotes/{symbol}`
- `GET /api/v1/quotes?symbols=AAPL,MSFT`

Quote ingestion requires `X-Market-Data-Key` to match
`MARKET_DATA_API_KEY`. Provider events are immutable and idempotent by
`provider + externalId`; conflicting reuse is rejected.

Quote responses include `ageSeconds` and `stale`, using
`QUOTE_STALE_AFTER_SECONDS` as the freshness threshold.
