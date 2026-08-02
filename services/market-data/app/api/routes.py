from __future__ import annotations

import asyncio
from datetime import datetime

from fastapi import APIRouter, Depends, Query, WebSocket, WebSocketDisconnect
from starlette.websockets import WebSocketState

from app.config import settings
from app.core.logging import get_logger
from app.db.models import CandleInterval
from app.dependencies import get_market_data_service, get_symbol_service
from app.middleware.rate_limit import enforce_ws_message_rate_limit
from app.schemas import LatestPrice, OHLCVIngest, OHLCVRead, ProviderStatus, SymbolCreate, SymbolRead, SymbolUpdate
from app.security import _decode_jwt, require_scope
from app.services.market_data_service import MarketDataService
from app.services.symbol_service import SymbolService
from app.websocket.manager import websocket_manager

logger = get_logger()
router = APIRouter(prefix="/api/v1")

# ---------------------------------------------------------------------------
# Scope constants
# ---------------------------------------------------------------------------
_SCOPE_READ = "market-data:read"
_SCOPE_SYMBOLS_WRITE = "market-data:symbols:write"
_SCOPE_INGEST = "market-data:ingest"
_SCOPE_OPERATIONS = "market-data:operations"


# ---------------------------------------------------------------------------
# Symbol endpoints
# ---------------------------------------------------------------------------


@router.post("/symbols", response_model=SymbolRead)
async def create_symbol(
    payload: SymbolCreate,
    _: dict = Depends(require_scope(_SCOPE_SYMBOLS_WRITE)),
    service: SymbolService = Depends(get_symbol_service),
) -> SymbolRead:
    row = await service.create_symbol(payload)
    return SymbolRead.model_validate(row)


@router.get("/symbols", response_model=list[SymbolRead])
async def list_symbols(
    active_only: bool = Query(default=False),
    _: dict = Depends(require_scope(_SCOPE_READ)),
    service: SymbolService = Depends(get_symbol_service),
) -> list[SymbolRead]:
    rows = await service.list_symbols(active_only=active_only)
    return [SymbolRead.model_validate(row) for row in rows]


@router.get("/symbols/{symbol}", response_model=SymbolRead)
async def get_symbol(
    symbol: str,
    _: dict = Depends(require_scope(_SCOPE_READ)),
    service: SymbolService = Depends(get_symbol_service),
) -> SymbolRead:
    row = await service.get_symbol(symbol)
    return SymbolRead.model_validate(row)


@router.put("/symbols/{symbol}", response_model=SymbolRead)
async def update_symbol(
    symbol: str,
    payload: SymbolUpdate,
    _: dict = Depends(require_scope(_SCOPE_SYMBOLS_WRITE)),
    service: SymbolService = Depends(get_symbol_service),
) -> SymbolRead:
    row = await service.update_symbol(symbol, payload)
    return SymbolRead.model_validate(row)


@router.delete("/symbols/{symbol}")
async def delete_symbol(
    symbol: str,
    _: dict = Depends(require_scope(_SCOPE_SYMBOLS_WRITE)),
    service: SymbolService = Depends(get_symbol_service),
) -> dict[str, bool]:
    await service.delete_symbol(symbol)
    return {"deleted": True}


# ---------------------------------------------------------------------------
# Price endpoints
# ---------------------------------------------------------------------------


@router.get("/prices/latest/{symbol}", response_model=LatestPrice)
async def latest_price(
    symbol: str,
    force_refresh: bool = Query(default=False),
    _: dict = Depends(require_scope(_SCOPE_READ)),
    service: MarketDataService = Depends(get_market_data_service),
) -> LatestPrice:
    return await service.get_latest_price(symbol, force_refresh=force_refresh)


# ---------------------------------------------------------------------------
# Historical endpoints
# ---------------------------------------------------------------------------


@router.post("/historical/{symbol}", response_model=OHLCVRead)
async def ingest_historical(
    symbol: str,
    payload: OHLCVIngest,
    _: dict = Depends(require_scope(_SCOPE_INGEST)),
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
    _: dict = Depends(require_scope(_SCOPE_READ)),
    service: MarketDataService = Depends(get_market_data_service),
) -> list[OHLCVRead]:
    return await service.get_historical(symbol, interval, start, end, limit)


@router.post("/historical/fetch/{symbol}/{interval}", response_model=list[OHLCVRead])
async def fetch_historical(
    symbol: str,
    interval: CandleInterval,
    limit: int = Query(default=100, ge=1, le=1000),
    _: dict = Depends(require_scope(_SCOPE_INGEST)),
    service: MarketDataService = Depends(get_market_data_service),
) -> list[OHLCVRead]:
    return await service.fetch_and_store_historical(symbol, interval, limit)


# ---------------------------------------------------------------------------
# Provider & active-symbol endpoints
# ---------------------------------------------------------------------------


@router.get("/providers/status", response_model=list[ProviderStatus])
async def provider_status(
    _: dict = Depends(require_scope(_SCOPE_OPERATIONS)),
    service: MarketDataService = Depends(get_market_data_service),
) -> list[ProviderStatus]:
    return await service.provider_service.get_provider_status()


@router.get("/active-symbols", response_model=list[str])
async def active_symbols(
    _: dict = Depends(require_scope(_SCOPE_READ)),
    service: MarketDataService = Depends(get_market_data_service),
) -> list[str]:
    return await service.list_active_symbols()


# ---------------------------------------------------------------------------
# WebSocket
# ---------------------------------------------------------------------------


async def _ws_authenticate(websocket: WebSocket) -> str | None:
    """Authenticate an incoming WebSocket handshake.

    Checks (in priority order):
      1. ``?token=<jwt>`` query parameter
      2. ``Authorization: ****** header
      3. ``X-API-Key: <key>`` header

    Returns the *identity* string on success, or sends a close frame and
    returns None on failure.
    """
    # --- JWT via query param ---
    token = websocket.query_params.get("token")
    if token:
        try:
            claims = _decode_jwt(token)
            return str(claims.get("sub") or "jwt")
        except Exception:
            await websocket.close(code=4001, reason="invalid token")
            return None

    # --- JWT via Authorization header ---
    auth_header = websocket.headers.get("Authorization", "")
    if auth_header.startswith("Bearer "):
        jwt_token = auth_header[7:]
        try:
            claims = _decode_jwt(jwt_token)
            return str(claims.get("sub") or "jwt")
        except Exception:
            await websocket.close(code=4001, reason="invalid token")
            return None

    # --- API key ---
    api_key = websocket.headers.get("X-API-Key")
    if api_key:
        if api_key in settings.parsed_api_keys:
            return f"apikey:{api_key[-6:]}"
        await websocket.close(code=4001, reason="invalid api key")
        return None

    await websocket.close(code=4001, reason="authentication required")
    return None


@router.websocket("/ws/prices")
async def prices_websocket(websocket: WebSocket) -> None:
    client_ip = (websocket.client.host if websocket.client else None) or "unknown"

    # Authentication must happen BEFORE accept().
    identity = await _ws_authenticate(websocket)
    if identity is None:
        return

    # Connection-limit enforcement.
    if not websocket_manager.register(websocket, client_ip):
        await websocket.close(code=4008, reason="connection limit exceeded")
        return

    await websocket.accept()
    logger.info("ws_connected", identity=identity, client_ip=client_ip)

    try:
        while True:
            # Idle-timeout: if no message arrives within the window, close cleanly.
            try:
                raw = await asyncio.wait_for(
                    websocket.receive_bytes(),
                    timeout=settings.ws_idle_timeout_seconds,
                )
            except TimeoutError:
                await websocket.close(code=4008, reason="idle timeout")
                break

            # Maximum message size.
            if len(raw) > settings.ws_max_message_bytes:
                await websocket.send_json({"type": "error", "message": "message too large"})
                await websocket.close(code=4009, reason="message too large")
                break

            # Per-connection message rate limit.
            try:
                await enforce_ws_message_rate_limit(identity)
            except RuntimeError:
                await websocket.send_json({"type": "error", "message": "message rate limit exceeded"})
                await websocket.close(code=4008, reason="rate limit exceeded")
                break

            import json as _json

            try:
                payload = _json.loads(raw)
            except Exception:
                await websocket.send_json({"type": "error", "message": "invalid json"})
                continue

            action = str(payload.get("action", "")).lower()
            symbol = str(payload.get("symbol", "")).upper()

            if not symbol:
                await websocket.send_json({"type": "error", "message": "symbol required"})
                continue

            if action == "subscribe":
                # Simple active-symbol check via shared cache key (best-effort).
                try:
                    from app.cache import redis_client as _rc  # noqa: PLC0415

                    members = await _rc.smembers("active_symbols")
                    if members and symbol not in {m.upper() for m in members}:
                        await websocket.send_json({"type": "error", "message": "unknown symbol: " + symbol})
                        continue
                except Exception:
                    pass  # Symbol validation best-effort; proceed if cache unavailable.

                accepted = await websocket_manager.subscribe(symbol, websocket)
                if not accepted:
                    await websocket.send_json({"type": "error", "message": "subscription limit exceeded"})
                else:
                    await websocket.send_json({"type": "subscribed", "symbol": symbol})

            elif action == "unsubscribe":
                await websocket_manager.unsubscribe_symbol(symbol, websocket)
                await websocket.send_json({"type": "unsubscribed", "symbol": symbol})
            else:
                await websocket.send_json({"type": "error", "message": "invalid action"})

    except WebSocketDisconnect:
        pass
    except Exception as exc:
        logger.warning("ws_unexpected_error", identity=identity, error_type=type(exc).__name__)
        if websocket.client_state == WebSocketState.CONNECTED:
            await websocket.close(code=1011, reason="internal error")
    finally:
        await websocket_manager.unsubscribe(websocket)
        websocket_manager.deregister(websocket, client_ip)
        logger.info("ws_disconnected", identity=identity, client_ip=client_ip)
