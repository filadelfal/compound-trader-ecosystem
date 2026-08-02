from __future__ import annotations

from fastapi import HTTPException
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession

from app.cache import redis_client
from app.db.models import Symbol
from app.repositories.symbol_repository import SymbolRepository
from app.schemas import SymbolCreate, SymbolUpdate


class SymbolService:
    def __init__(self, session: AsyncSession) -> None:
        self.session = session
        self.repository = SymbolRepository(session)

    async def _refresh_active_symbol_cache(self) -> None:
        active = await self.repository.list(active_only=True)
        key = "active_symbols"
        if active:
            await redis_client.delete(key)
            await redis_client.sadd(key, *[symbol.symbol for symbol in active])
            await redis_client.expire(key, 600)
        else:
            await redis_client.delete(key)

    async def create_symbol(self, payload: SymbolCreate) -> Symbol:
        symbol = Symbol(
            symbol=payload.symbol,
            name=payload.name,
            instrument_type=payload.instrument_type,
            base_asset=payload.base_asset,
            quote_asset=payload.quote_asset,
            exchange=payload.exchange,
            is_active=payload.is_active,
        )

        try:
            saved = await self.repository.create(symbol)
            await self.session.commit()
        except IntegrityError as exc:
            await self.session.rollback()
            raise HTTPException(status_code=409, detail="symbol already exists") from exc

        await self._refresh_active_symbol_cache()
        return saved

    async def list_symbols(self, active_only: bool = False) -> list[Symbol]:
        return await self.repository.list(active_only=active_only)

    async def get_symbol(self, symbol_code: str) -> Symbol:
        symbol = await self.repository.get_by_symbol(symbol_code)
        if symbol is None:
            raise HTTPException(status_code=404, detail="symbol not found")
        return symbol

    async def update_symbol(self, symbol_code: str, payload: SymbolUpdate) -> Symbol:
        symbol = await self.get_symbol(symbol_code)

        updates = payload.model_dump(exclude_unset=True)
        for key, value in updates.items():
            setattr(symbol, key, value)

        await self.session.commit()
        await self.session.refresh(symbol)
        await self._refresh_active_symbol_cache()
        return symbol

    async def delete_symbol(self, symbol_code: str) -> None:
        symbol = await self.get_symbol(symbol_code)
        await self.repository.delete(symbol)
        await self.session.commit()
        await self._refresh_active_symbol_cache()
