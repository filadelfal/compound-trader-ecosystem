from __future__ import annotations

import asyncio
from datetime import UTC, datetime
from decimal import Decimal

import pytest

from app.providers.base import MarketDataProvider, ProviderError, ProviderPrice
from app.services.provider_service import ProviderFailoverService


class FailProvider(MarketDataProvider):
    name = "twelvedata"

    async def fetch_latest_price(self, symbol: str) -> ProviderPrice:
        raise ProviderError("failed")

    async def fetch_historical(self, symbol, interval, limit=100):  # type: ignore[override]
        raise ProviderError("failed")


class OkProvider(MarketDataProvider):
    name = "binance"

    async def fetch_latest_price(self, symbol: str) -> ProviderPrice:
        return ProviderPrice(symbol=symbol, price=Decimal("1.23"), timestamp=datetime.now(UTC), provider=self.name)

    async def fetch_historical(self, symbol, interval, limit=100):  # type: ignore[override]
        return []


class RedisFailingProvider(MarketDataProvider):
    """Provider that succeeds – used when Redis telemetry is broken."""

    name = "polygon"

    async def fetch_latest_price(self, symbol: str) -> ProviderPrice:
        return ProviderPrice(symbol=symbol, price=Decimal("9.99"), timestamp=datetime.now(UTC), provider=self.name)

    async def fetch_historical(self, symbol, interval, limit=100):  # type: ignore[override]
        return []


@pytest.mark.asyncio
async def test_failover_uses_second_provider(monkeypatch):
    service = ProviderFailoverService()
    service.providers = {"twelvedata": FailProvider(), "binance": OkProvider()}
    monkeypatch.setattr("app.services.provider_service.settings.provider_priority", "twelvedata,binance")

    async def _noop(_status) -> None:
        return None

    monkeypatch.setattr(service, "_set_status", _noop)
    price = await service.fetch_latest_price("BTCUSDT")
    assert price.provider == "binance"
    await service.close()


@pytest.mark.asyncio
async def test_failover_continues_when_telemetry_redis_unavailable(monkeypatch):
    """A Redis failure in _set_status must not discard a valid provider result."""
    service = ProviderFailoverService()
    service.providers = {"polygon": RedisFailingProvider()}
    monkeypatch.setattr("app.services.provider_service.settings.provider_priority", "polygon")

    async def _failing_set_status(_status) -> None:
        raise ConnectionError("redis is down")

    monkeypatch.setattr(service, "_set_status", _failing_set_status)

    # The result must arrive despite _set_status raising.
    # Because _set_status is now fire-and-forget we need to drain pending tasks.
    price = await service.fetch_latest_price("EURUSD")
    # Let any pending ensure_future tasks complete/fail.
    await asyncio.sleep(0)
    assert price.provider == "polygon"
    await service.close()


@pytest.mark.asyncio
async def test_cancellation_not_swallowed(monkeypatch):
    """asyncio.CancelledError must not be caught and discarded."""

    class CancellingProvider(MarketDataProvider):
        name = "twelvedata"

        async def fetch_latest_price(self, symbol: str) -> ProviderPrice:
            raise asyncio.CancelledError()

        async def fetch_historical(self, symbol, interval, limit=100):  # type: ignore[override]
            raise asyncio.CancelledError()

    service = ProviderFailoverService()
    service.providers = {"twelvedata": CancellingProvider()}
    monkeypatch.setattr("app.services.provider_service.settings.provider_priority", "twelvedata")

    with pytest.raises(asyncio.CancelledError):
        await service.fetch_latest_price("BTCUSDT")
    await service.close()


@pytest.mark.asyncio
async def test_all_providers_fail_raises_provider_error(monkeypatch):
    service = ProviderFailoverService()
    service.providers = {"twelvedata": FailProvider()}
    monkeypatch.setattr("app.services.provider_service.settings.provider_priority", "twelvedata")

    async def _noop(_status) -> None:
        return None

    monkeypatch.setattr(service, "_set_status", _noop)

    with pytest.raises(ProviderError):
        await service.fetch_latest_price("UNKNOWN")
    await service.close()
