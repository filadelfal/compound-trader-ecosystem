from __future__ import annotations

import json
from datetime import datetime
from decimal import Decimal

from sqlalchemy.ext.asyncio import AsyncSession

from app.cache import redis_client
from app.db.models import OHLCV, CandleInterval
from app.repositories.ohlcv_repository import OHLCVRepository
from app.schemas import LatestPrice, OHLCVIngest, OHLCVRead
from app.services.provider_service import ProviderFailoverService
from app.services.symbol_service import SymbolService
from app.websocket.manager import websocket_manager


class MarketDataService:
    def __init__(self, session: AsyncSession, provider_service: ProviderFailoverService) -> None:
        self.session = session
        self.symbol_service = SymbolService(session)
        self.repository = OHLCVRepository(session)
        self.provider_service = provider_service

    async def get_latest_price(self, symbol: str, force_refresh: bool = False) -> LatestPrice:
        normalized_symbol = symbol.upper()
        cache_key = f"latest_price:{normalized_symbol}"
        if not force_refresh:
            cached = await redis_client.get(cache_key)
            if cached:
                data = json.loads(cached)
                return LatestPrice(
                    symbol=data["symbol"],
                    price=Decimal(data["price"]),
                    timestamp=datetime.fromisoformat(data["timestamp"]),
                    provider=data["provider"],
                )

        await self.symbol_service.get_symbol(normalized_symbol)
        tick = await self.provider_service.fetch_latest_price(normalized_symbol)
        result = LatestPrice(symbol=tick.symbol, price=tick.price, timestamp=tick.timestamp, provider=tick.provider)

        await redis_client.set(
            cache_key,
            json.dumps(
                {
                    "symbol": result.symbol,
                    "price": str(result.price),
                    "timestamp": result.timestamp.isoformat(),
                    "provider": result.provider,
                }
            ),
            ex=30,
        )

        await websocket_manager.broadcast(
            symbol=normalized_symbol,
            payload={
                "type": "price_update",
                "symbol": result.symbol,
                "price": str(result.price),
                "timestamp": result.timestamp.isoformat(),
                "provider": result.provider,
            },
        )
        return result

    async def ingest_candle(self, symbol: str, payload: OHLCVIngest) -> OHLCVRead:
        symbol_row = await self.symbol_service.get_symbol(symbol)
        row = OHLCV(
            symbol_id=symbol_row.id,
            interval=payload.interval,
            open=payload.open,
            high=payload.high,
            low=payload.low,
            close=payload.close,
            volume=payload.volume,
            provider=payload.provider,
            timestamp=payload.timestamp,
        )
        saved = await self.repository.upsert(row)
        await self.session.commit()
        return OHLCVRead(
            symbol=symbol_row.symbol,
            interval=saved.interval,
            open=saved.open,
            high=saved.high,
            low=saved.low,
            close=saved.close,
            volume=saved.volume,
            provider=saved.provider,
            timestamp=saved.timestamp,
        )

    async def get_historical(
        self,
        symbol: str,
        interval: CandleInterval,
        start: datetime | None,
        end: datetime | None,
        limit: int,
    ) -> list[OHLCVRead]:
        symbol_row = await self.symbol_service.get_symbol(symbol)
        rows = await self.repository.list(symbol_row.id, interval, start, end, limit)
        return [
            OHLCVRead(
                symbol=symbol_row.symbol,
                interval=row.interval,
                open=row.open,
                high=row.high,
                low=row.low,
                close=row.close,
                volume=row.volume,
                provider=row.provider,
                timestamp=row.timestamp,
            )
            for row in rows
        ]

    async def fetch_and_store_historical(
        self, symbol: str, interval: CandleInterval, limit: int = 100
    ) -> list[OHLCVRead]:
        symbol_row = await self.symbol_service.get_symbol(symbol)
        candles = await self.provider_service.fetch_historical(symbol_row.symbol, interval, limit=limit)

        persisted: list[OHLCVRead] = []
        for candle in candles:
            row = OHLCV(
                symbol_id=symbol_row.id,
                interval=interval,
                open=candle.open,
                high=candle.high,
                low=candle.low,
                close=candle.close,
                volume=candle.volume,
                provider=candle.provider,
                timestamp=candle.timestamp,
            )
            saved = await self.repository.upsert(row)
            persisted.append(
                OHLCVRead(
                    symbol=symbol_row.symbol,
                    interval=saved.interval,
                    open=saved.open,
                    high=saved.high,
                    low=saved.low,
                    close=saved.close,
                    volume=saved.volume,
                    provider=saved.provider,
                    timestamp=saved.timestamp,
                )
            )

        await self.session.commit()
        return persisted

    async def list_active_symbols(self) -> list[str]:
        cached = await redis_client.smembers("active_symbols")
        if cached:
            return sorted(cached)
        symbols = await self.symbol_service.list_symbols(active_only=True)
        if symbols:
            await redis_client.sadd("active_symbols", *[row.symbol for row in symbols])
            await redis_client.expire("active_symbols", 600)
        return [row.symbol for row in symbols]
