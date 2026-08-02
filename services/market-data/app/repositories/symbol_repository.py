from __future__ import annotations

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db.models import Symbol


class SymbolRepository:
    def __init__(self, session: AsyncSession) -> None:
        self.session = session

    async def create(self, symbol: Symbol) -> Symbol:
        self.session.add(symbol)
        await self.session.flush()
        await self.session.refresh(symbol)
        return symbol

    async def list(self, active_only: bool = False) -> list[Symbol]:
        stmt = select(Symbol).order_by(Symbol.symbol)
        if active_only:
            stmt = stmt.where(Symbol.is_active.is_(True))
        result = await self.session.execute(stmt)
        return list(result.scalars().all())

    async def get_by_symbol(self, symbol_code: str) -> Symbol | None:
        stmt = select(Symbol).where(Symbol.symbol == symbol_code.upper())
        result = await self.session.execute(stmt)
        return result.scalar_one_or_none()

    async def delete(self, symbol: Symbol) -> None:
        await self.session.delete(symbol)
