from __future__ import annotations

from datetime import UTC, datetime

import httpx

from app.config import settings
from app.db.models import CandleInterval
from app.providers.base import MarketDataProvider, ProviderCandle, ProviderError, ProviderPrice
from app.providers.http_base import decimal_from_payload, get_json


class MT5Provider(MarketDataProvider):
    name = "mt5"

    def __init__(self, client: httpx.AsyncClient) -> None:
        self.client = client

    async def fetch_latest_price(self, symbol: str) -> ProviderPrice:
        if not settings.mt5_base_url:
            raise ProviderError("mt5_base_url not configured")
        payload = await get_json(self.client, f"{settings.mt5_base_url.rstrip('/')}/latest", {"symbol": symbol})
        timestamp = payload.get("timestamp")
        ts = datetime.fromisoformat(str(timestamp).replace("Z", "+00:00")).astimezone(UTC)
        return ProviderPrice(
            symbol=symbol,
            price=decimal_from_payload(payload.get("price"), "price"),
            timestamp=ts,
            provider=self.name,
        )

    async def fetch_historical(self, symbol: str, interval: CandleInterval, limit: int = 100) -> list[ProviderCandle]:
        if not settings.mt5_base_url:
            raise ProviderError("mt5_base_url not configured")
        payload = await get_json(
            self.client,
            f"{settings.mt5_base_url.rstrip('/')}/historical",
            {"symbol": symbol, "interval": interval.value, "limit": str(limit)},
        )
        values = payload.get("candles")
        if not isinstance(values, list):
            raise ProviderError("missing candles")
        rows: list[ProviderCandle] = []
        for item in values:
            if not isinstance(item, dict):
                continue
            ts = datetime.fromisoformat(str(item["timestamp"]).replace("Z", "+00:00")).astimezone(UTC)
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
