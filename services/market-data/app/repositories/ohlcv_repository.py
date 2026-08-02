from __future__ import annotations

from datetime import datetime
from uuid import UUID

from sqlalchemy import Select, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db.models import OHLCV, CandleInterval


class OHLCVRepository:
    def __init__(self, session: AsyncSession) -> None:
        self.session = session

    async def upsert(self, row: OHLCV) -> OHLCV:
        stmt = select(OHLCV).where(
            OHLCV.symbol_id == row.symbol_id,
            OHLCV.interval == row.interval,
            OHLCV.timestamp == row.timestamp,
        )
        existing = (await self.session.execute(stmt)).scalar_one_or_none()
        if existing:
            existing.open = row.open
            existing.high = row.high
            existing.low = row.low
            existing.close = row.close
            existing.volume = row.volume
            existing.provider = row.provider
            await self.session.flush()
            await self.session.refresh(existing)
            return existing

        self.session.add(row)
        await self.session.flush()
        await self.session.refresh(row)
        return row

    async def list(
        self,
        symbol_id: UUID,
        interval: CandleInterval,
        start: datetime | None,
        end: datetime | None,
        limit: int,
    ) -> list[OHLCV]:
        stmt: Select[tuple[OHLCV]] = select(OHLCV).where(OHLCV.symbol_id == symbol_id, OHLCV.interval == interval)
        if start is not None:
            stmt = stmt.where(OHLCV.timestamp >= start)
        if end is not None:
            stmt = stmt.where(OHLCV.timestamp <= end)
        stmt = stmt.order_by(OHLCV.timestamp.desc()).limit(limit)
        result = await self.session.execute(stmt)
        return list(result.scalars().all())
