from __future__ import annotations

from datetime import UTC, datetime

import httpx

from app.config import settings
from app.db.models import CandleInterval
from app.providers.base import MarketDataProvider, ProviderCandle, ProviderError, ProviderPrice, utcnow
from app.providers.http_base import decimal_from_payload, get_json

_TIMESPAN_MAP = {
    CandleInterval.M1: (1, "minute"),
    CandleInterval.M5: (5, "minute"),
    CandleInterval.M15: (15, "minute"),
    CandleInterval.M30: (30, "minute"),
    CandleInterval.H1: (1, "hour"),
    CandleInterval.H4: (4, "hour"),
    CandleInterval.D1: (1, "day"),
    CandleInterval.W1: (1, "week"),
    CandleInterval.MO1: (1, "month"),
}


class PolygonProvider(MarketDataProvider):
    name = "polygon"

    def __init__(self, client: httpx.AsyncClient) -> None:
        self.client = client

    async def fetch_latest_price(self, symbol: str) -> ProviderPrice:
        if not settings.polygon_api_key:
            raise ProviderError("polygon_api_key not configured")
        payload = await get_json(
            self.client,
            f"https://api.polygon.io/v2/last/trade/{symbol}",
            {"apiKey": settings.polygon_api_key},
        )
        result = payload.get("results")
        if not isinstance(result, dict):
            raise ProviderError("missing results")
        return ProviderPrice(
            symbol=symbol, price=decimal_from_payload(result.get("p"), "p"), timestamp=utcnow(), provider=self.name
        )

    async def fetch_historical(self, symbol: str, interval: CandleInterval, limit: int = 100) -> list[ProviderCandle]:
        if not settings.polygon_api_key:
            raise ProviderError("polygon_api_key not configured")
        multiplier, span = _TIMESPAN_MAP[interval]
        payload = await get_json(
            self.client,
            f"https://api.polygon.io/v2/aggs/ticker/{symbol}/range/{multiplier}/{span}/2020-01-01/2100-01-01",
            {"apiKey": settings.polygon_api_key, "limit": str(limit), "sort": "desc"},
        )
        results = payload.get("results")
        if not isinstance(results, list):
            raise ProviderError("missing results")
        rows: list[ProviderCandle] = []
        for item in results:
            if not isinstance(item, dict):
                continue
            ts_ms = item.get("t")
            if ts_ms is None:
                continue
            ts = datetime.fromtimestamp(float(ts_ms) / 1000, tz=UTC)
            rows.append(
                ProviderCandle(
                    symbol=symbol,
                    interval=interval,
                    open=decimal_from_payload(item.get("o"), "o"),
                    high=decimal_from_payload(item.get("h"), "h"),
                    low=decimal_from_payload(item.get("l"), "l"),
                    close=decimal_from_payload(item.get("c"), "c"),
                    volume=decimal_from_payload(item.get("v"), "v"),
                    timestamp=ts,
                    provider=self.name,
                )
            )
        return rows
