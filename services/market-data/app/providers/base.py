from __future__ import annotations

from abc import ABC, abstractmethod
from datetime import UTC, datetime
from decimal import Decimal

from pydantic import BaseModel

from app.db.models import CandleInterval


class ProviderPrice(BaseModel):
    symbol: str
    price: Decimal
    timestamp: datetime
    provider: str


class ProviderCandle(BaseModel):
    symbol: str
    interval: CandleInterval
    open: Decimal
    high: Decimal
    low: Decimal
    close: Decimal
    volume: Decimal
    timestamp: datetime
    provider: str


class MarketDataProvider(ABC):
    name: str

    @abstractmethod
    async def fetch_latest_price(self, symbol: str) -> ProviderPrice:
        raise NotImplementedError

    @abstractmethod
    async def fetch_historical(
        self,
        symbol: str,
        interval: CandleInterval,
        limit: int = 100,
    ) -> list[ProviderCandle]:
        raise NotImplementedError


class ProviderError(Exception):
    pass


def utcnow() -> datetime:
    return datetime.now(tz=UTC)
