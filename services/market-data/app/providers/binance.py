from __future__ import annotations

from datetime import UTC, datetime

import httpx

from app.db.models import CandleInterval
from app.providers.base import MarketDataProvider, ProviderCandle, ProviderError, ProviderPrice, utcnow
from app.providers.http_base import decimal_from_payload, get_json

_INTERVAL_MAP = {
    CandleInterval.M1: "1m",
    CandleInterval.M5: "5m",
    CandleInterval.M15: "15m",
    CandleInterval.M30: "30m",
    CandleInterval.H1: "1h",
    CandleInterval.H4: "4h",
    CandleInterval.D1: "1d",
    CandleInterval.W1: "1w",
    CandleInterval.MO1: "1M",
}


class BinanceProvider(MarketDataProvider):
    name = "binance"

    def __init__(self, client: httpx.AsyncClient) -> None:
        self.client = client

    async def fetch_latest_price(self, symbol: str) -> ProviderPrice:
        payload = await get_json(self.client, "https://api.binance.com/api/v3/ticker/price", {"symbol": symbol})
        return ProviderPrice(
            symbol=symbol,
            price=decimal_from_payload(payload.get("price"), "price"),
            timestamp=utcnow(),
            provider=self.name,
        )

    async def fetch_historical(self, symbol: str, interval: CandleInterval, limit: int = 100) -> list[ProviderCandle]:
        response = await self.client.get(
            "https://api.binance.com/api/v3/klines",
            params={"symbol": symbol, "interval": _INTERVAL_MAP[interval], "limit": str(limit)},
        )
        response.raise_for_status()
        payload = response.json()
        if not isinstance(payload, list):
            raise ProviderError("invalid klines response")

        rows: list[ProviderCandle] = []
        for item in payload:
            if not isinstance(item, list) or len(item) < 6:
                continue
            ts = datetime.fromtimestamp(float(item[0]) / 1000, tz=UTC)
            rows.append(
                ProviderCandle(
                    symbol=symbol,
                    interval=interval,
                    open=decimal_from_payload(item[1], "open"),
                    high=decimal_from_payload(item[2], "high"),
                    low=decimal_from_payload(item[3], "low"),
                    close=decimal_from_payload(item[4], "close"),
                    volume=decimal_from_payload(item[5], "volume"),
                    timestamp=ts,
                    provider=self.name,
                )
            )
        return rows
