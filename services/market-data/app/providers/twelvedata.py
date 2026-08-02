from __future__ import annotations

from datetime import UTC, datetime

import httpx

from app.config import settings
from app.db.models import CandleInterval
from app.providers.base import MarketDataProvider, ProviderCandle, ProviderError, ProviderPrice, utcnow
from app.providers.http_base import decimal_from_payload, get_json

_INTERVAL_MAP = {
    CandleInterval.M1: "1min",
    CandleInterval.M5: "5min",
    CandleInterval.M15: "15min",
    CandleInterval.M30: "30min",
    CandleInterval.H1: "1h",
    CandleInterval.H4: "4h",
    CandleInterval.D1: "1day",
    CandleInterval.W1: "1week",
    CandleInterval.MO1: "1month",
}


class TwelveDataProvider(MarketDataProvider):
    name = "twelvedata"

    def __init__(self, client: httpx.AsyncClient) -> None:
        self.client = client

    async def fetch_latest_price(self, symbol: str) -> ProviderPrice:
        if not settings.twelvedata_api_key:
            raise ProviderError("twelvedata_api_key not configured")
        payload = await get_json(
            self.client,
            "https://api.twelvedata.com/price",
            {"symbol": symbol, "apikey": settings.twelvedata_api_key},
        )
        if "price" not in payload:
            raise ProviderError(payload.get("message", "missing price"))
        return ProviderPrice(
            symbol=symbol,
            price=decimal_from_payload(payload.get("price"), "price"),
            timestamp=utcnow(),
            provider=self.name,
        )

    async def fetch_historical(self, symbol: str, interval: CandleInterval, limit: int = 100) -> list[ProviderCandle]:
        if not settings.twelvedata_api_key:
            raise ProviderError("twelvedata_api_key not configured")
        payload = await get_json(
            self.client,
            "https://api.twelvedata.com/time_series",
            {
                "symbol": symbol,
                "interval": _INTERVAL_MAP[interval],
                "apikey": settings.twelvedata_api_key,
                "outputsize": str(limit),
            },
        )
        values = payload.get("values")
        if not isinstance(values, list):
            raise ProviderError(payload.get("message", "missing values"))

        rows: list[ProviderCandle] = []
        for item in values:
            if not isinstance(item, dict):
                continue
            ts = datetime.fromisoformat(str(item["datetime"]).replace("Z", "+00:00")).astimezone(UTC)
            rows.append(
                ProviderCandle(
                    symbol=symbol,
                    interval=interval,
                    open=decimal_from_payload(item.get("open"), "open"),
                    high=decimal_from_payload(item.get("high"), "high"),
                    low=decimal_from_payload(item.get("low"), "low"),
                    close=decimal_from_payload(item.get("close"), "close"),
                    volume=decimal_from_payload(item.get("volume"), "volume"),
                    timestamp=ts,
                    provider=self.name,
                )
            )
        return rows
