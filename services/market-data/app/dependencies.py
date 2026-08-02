from __future__ import annotations

from collections.abc import AsyncGenerator

from fastapi import Depends
from sqlalchemy.ext.asyncio import AsyncSession

from app.db.session import get_session
from app.services.market_data_service import MarketDataService
from app.services.provider_service import ProviderFailoverService
from app.services.symbol_service import SymbolService

_provider_service: ProviderFailoverService | None = None


def set_provider_service(provider_service: ProviderFailoverService) -> None:
    global _provider_service
    _provider_service = provider_service


def get_provider_service() -> ProviderFailoverService:
    if _provider_service is None:
        raise RuntimeError("provider service not initialized")
    return _provider_service


async def get_symbol_service(session: AsyncSession = Depends(get_session)) -> AsyncGenerator[SymbolService]:
    yield SymbolService(session)


async def get_market_data_service(
    session: AsyncSession = Depends(get_session),
) -> AsyncGenerator[MarketDataService]:
    yield MarketDataService(session, get_provider_service())
