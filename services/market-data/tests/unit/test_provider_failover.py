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
