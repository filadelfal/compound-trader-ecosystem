from __future__ import annotations

from datetime import UTC, datetime
from time import perf_counter

import httpx

from app.cache import redis_client
from app.config import settings
from app.db.models import CandleInterval
from app.providers.alphavantage import AlphaVantageProvider
from app.providers.base import MarketDataProvider, ProviderCandle, ProviderError, ProviderPrice
from app.providers.binance import BinanceProvider
from app.providers.mt5 import MT5Provider
from app.providers.polygon import PolygonProvider
from app.providers.twelvedata import TwelveDataProvider
from app.schemas import ProviderStatus


class ProviderFailoverService:
    def __init__(self) -> None:
        self.client = httpx.AsyncClient(timeout=settings.request_timeout_seconds)
        self.providers: dict[str, MarketDataProvider] = {
            "twelvedata": TwelveDataProvider(self.client),
            "alphavantage": AlphaVantageProvider(self.client),
            "polygon": PolygonProvider(self.client),
            "binance": BinanceProvider(self.client),
            "mt5": MT5Provider(self.client),
        }

    async def close(self) -> None:
        await self.client.aclose()

    async def _set_status(self, status: ProviderStatus) -> None:
        key = f"provider_status:{status.provider}"
        await redis_client.hset(
            key,
            mapping={
                "healthy": str(status.healthy).lower(),
                "latency_ms": "" if status.latency_ms is None else str(status.latency_ms),
                "error": status.error or "",
                "checked_at": status.checked_at.isoformat(),
            },
        )
        await redis_client.expire(key, 300)

    async def get_provider_status(self) -> list[ProviderStatus]:
        rows: list[ProviderStatus] = []
        for name in self.providers:
            key = f"provider_status:{name}"
            state = await redis_client.hgetall(key)
            if not state:
                rows.append(
                    ProviderStatus(
                        provider=name,
                        healthy=False,
                        latency_ms=None,
                        error="not_checked",
                        checked_at=datetime.now(UTC),
                    )
                )
                continue
            rows.append(
                ProviderStatus(
                    provider=name,
                    healthy=state.get("healthy") == "true",
                    latency_ms=float(state["latency_ms"]) if state.get("latency_ms") else None,
                    error=state.get("error") or None,
                    checked_at=datetime.fromisoformat(state["checked_at"]),
                )
            )
        return rows

    async def _execute_with_failover(self, operation_name: str, symbol: str, handler) -> object:
        errors: list[str] = []

        for provider_name in settings.parsed_provider_priority:
            provider = self.providers.get(provider_name)
            if provider is None:
                continue

            start = perf_counter()
            try:
                result = await handler(provider)
                latency_ms = round((perf_counter() - start) * 1000, 2)
                await self._set_status(
                    ProviderStatus(
                        provider=provider_name,
                        healthy=True,
                        latency_ms=latency_ms,
                        error=None,
                        checked_at=datetime.now(UTC),
                    )
                )
                return result
            except Exception as exc:
                latency_ms = round((perf_counter() - start) * 1000, 2)
                errors.append(f"{provider_name}:{exc}")
                await self._set_status(
                    ProviderStatus(
                        provider=provider_name,
                        healthy=False,
                        latency_ms=latency_ms,
                        error=str(exc),
                        checked_at=datetime.now(UTC),
                    )
                )

        raise ProviderError(f"all providers failed for {operation_name} on {symbol}: {' | '.join(errors)}")

    async def fetch_latest_price(self, symbol: str) -> ProviderPrice:
        result = await self._execute_with_failover(
            "latest_price", symbol, lambda provider: provider.fetch_latest_price(symbol)
        )
        return result  # type: ignore[return-value]

    async def fetch_historical(self, symbol: str, interval: CandleInterval, limit: int = 100) -> list[ProviderCandle]:
        result = await self._execute_with_failover(
            "historical", symbol, lambda provider: provider.fetch_historical(symbol, interval=interval, limit=limit)
        )
        return result  # type: ignore[return-value]
