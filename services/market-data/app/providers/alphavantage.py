from __future__ import annotations

from datetime import UTC, datetime

import httpx

from app.config import settings
from app.db.models import CandleInterval
from app.providers.base import MarketDataProvider, ProviderCandle, ProviderError, ProviderPrice, utcnow
from app.providers.http_base import decimal_from_payload, get_json


class AlphaVantageProvider(MarketDataProvider):
    name = "alphavantage"

    def __init__(self, client: httpx.AsyncClient) -> None:
        self.client = client

    async def fetch_latest_price(self, symbol: str) -> ProviderPrice:
        if not settings.alphavantage_api_key:
            raise ProviderError("alphavantage_api_key not configured")
        payload = await get_json(
            self.client,
            "https://www.alphavantage.co/query",
            {
                "function": "GLOBAL_QUOTE",
                "symbol": symbol,
                "apikey": settings.alphavantage_api_key,
            },
        )
        quote = payload.get("Global Quote")
        if not isinstance(quote, dict):
            raise ProviderError(payload.get("Note", "missing Global Quote"))
        return ProviderPrice(
            symbol=symbol,
            price=decimal_from_payload(quote.get("05. price"), "05. price"),
            timestamp=utcnow(),
            provider=self.name,
        )

    async def fetch_historical(self, symbol: str, interval: CandleInterval, limit: int = 100) -> list[ProviderCandle]:
        if not settings.alphavantage_api_key:
            raise ProviderError("alphavantage_api_key not configured")

        function = "TIME_SERIES_DAILY"
        params = {
            "function": function,
            "symbol": symbol,
            "apikey": settings.alphavantage_api_key,
            "outputsize": "compact",
        }
        payload = await get_json(self.client, "https://www.alphavantage.co/query", params)
        series = payload.get("Time Series (Daily)")
        if not isinstance(series, dict):
            raise ProviderError(payload.get("Note", "daily series unavailable"))

        if interval not in {CandleInterval.D1, CandleInterval.W1, CandleInterval.MO1}:
            raise ProviderError("alphavantage historical supports daily/weekly/monthly in this service")

        rows: list[ProviderCandle] = []
        for timestamp, item in list(series.items())[:limit]:
            if not isinstance(item, dict):
                continue
            ts = datetime.fromisoformat(f"{timestamp}T00:00:00+00:00").astimezone(UTC)
            rows.append(
                ProviderCandle(
                    symbol=symbol,
                    interval=interval,
                    open=decimal_from_payload(item.get("1. open"), "1. open"),
                    high=decimal_from_payload(item.get("2. high"), "2. high"),
                    low=decimal_from_payload(item.get("3. low"), "3. low"),
                    close=decimal_from_payload(item.get("4. close"), "4. close"),
                    volume=decimal_from_payload(item.get("5. volume"), "5. volume"),
                    timestamp=ts,
                    provider=self.name,
                )
            )
        return rows
