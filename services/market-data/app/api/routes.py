from __future__ import annotations

from datetime import datetime

from fastapi import APIRouter, Depends, Query, WebSocket, WebSocketDisconnect

from app.db.models import CandleInterval
from app.dependencies import get_market_data_service, get_symbol_service
from app.schemas import LatestPrice, OHLCVIngest, OHLCVRead, ProviderStatus, SymbolCreate, SymbolRead, SymbolUpdate
from app.security import authenticate_request
from app.services.market_data_service import MarketDataService
from app.services.symbol_service import SymbolService
from app.websocket.manager import websocket_manager

router = APIRouter(prefix="/api/v1")


@router.post("/symbols", response_model=SymbolRead)
async def create_symbol(
    payload: SymbolCreate,
    _: dict = Depends(authenticate_request),
    service: SymbolService = Depends(get_symbol_service),
) -> SymbolRead:
    row = await service.create_symbol(payload)
    return SymbolRead.model_validate(row)


@router.get("/symbols", response_model=list[SymbolRead])
async def list_symbols(
    active_only: bool = Query(default=False),
    _: dict = Depends(authenticate_request),
    service: SymbolService = Depends(get_symbol_service),
) -> list[SymbolRead]:
    rows = await service.list_symbols(active_only=active_only)
    return [SymbolRead.model_validate(row) for row in rows]


@router.get("/symbols/{symbol}", response_model=SymbolRead)
async def get_symbol(
    symbol: str,
    _: dict = Depends(authenticate_request),
    service: SymbolService = Depends(get_symbol_service),
) -> SymbolRead:
    row = await service.get_symbol(symbol)
    return SymbolRead.model_validate(row)


@router.put("/symbols/{symbol}", response_model=SymbolRead)
async def update_symbol(
    symbol: str,
    payload: SymbolUpdate,
    _: dict = Depends(authenticate_request),
    service: SymbolService = Depends(get_symbol_service),
) -> SymbolRead:
    row = await service.update_symbol(symbol, payload)
    return SymbolRead.model_validate(row)


@router.delete("/symbols/{symbol}")
async def delete_symbol(
    symbol: str,
    _: dict = Depends(authenticate_request),
    service: SymbolService = Depends(get_symbol_service),
) -> dict[str, bool]:
    await service.delete_symbol(symbol)
    return {"deleted": True}


@router.get("/prices/latest/{symbol}", response_model=LatestPrice)
async def latest_price(
    symbol: str,
    force_refresh: bool = Query(default=False),
    _: dict = Depends(authenticate_request),
    service: MarketDataService = Depends(get_market_data_service),
) -> LatestPrice:
    return await service.get_latest_price(symbol, force_refresh=force_refresh)


@router.post("/historical/{symbol}", response_model=OHLCVRead)
async def ingest_historical(
    symbol: str,
    payload: OHLCVIngest,
    _: dict = Depends(authenticate_request),
    service: MarketDataService = Depends(get_market_data_service),
) -> OHLCVRead:
    return await service.ingest_candle(symbol, payload)


@router.get("/historical/{symbol}/{interval}", response_model=list[OHLCVRead])
async def list_historical(
    symbol: str,
    interval: CandleInterval,
    start: datetime | None = Query(default=None),
    end: datetime | None = Query(default=None),
    limit: int = Query(default=500, ge=1, le=5000),
    _: dict = Depends(authenticate_request),
    service: MarketDataService = Depends(get_market_data_service),
) -> list[OHLCVRead]:
    return await service.get_historical(symbol, interval, start, end, limit)


@router.post("/historical/fetch/{symbol}/{interval}", response_model=list[OHLCVRead])
async def fetch_historical(
    symbol: str,
    interval: CandleInterval,
    limit: int = Query(default=100, ge=1, le=1000),
    _: dict = Depends(authenticate_request),
    service: MarketDataService = Depends(get_market_data_service),
) -> list[OHLCVRead]:
    return await service.fetch_and_store_historical(symbol, interval, limit)


@router.get("/providers/status", response_model=list[ProviderStatus])
async def provider_status(
    _: dict = Depends(authenticate_request),
    service: MarketDataService = Depends(get_market_data_service),
) -> list[ProviderStatus]:
    return await service.provider_service.get_provider_status()


@router.get("/active-symbols", response_model=list[str])
async def active_symbols(
    _: dict = Depends(authenticate_request),
    service: MarketDataService = Depends(get_market_data_service),
) -> list[str]:
    return await service.list_active_symbols()


@router.websocket("/ws/prices")
async def prices_websocket(websocket: WebSocket) -> None:
    await websocket.accept()
    try:
        while True:
            payload = await websocket.receive_json()
            action = str(payload.get("action", "")).lower()
            symbol = str(payload.get("symbol", "")).upper()
            if action == "subscribe" and symbol:
                await websocket_manager.subscribe(symbol, websocket)
                await websocket.send_json({"type": "subscribed", "symbol": symbol})
            elif action == "unsubscribe" and symbol:
                await websocket_manager.unsubscribe(websocket)
                await websocket.send_json({"type": "unsubscribed", "symbol": symbol})
            else:
                await websocket.send_json({"type": "error", "message": "invalid action"})
    except WebSocketDisconnect:
        await websocket_manager.unsubscribe(websocket)
