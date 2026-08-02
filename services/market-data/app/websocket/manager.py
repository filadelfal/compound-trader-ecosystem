from __future__ import annotations

import asyncio
from collections import defaultdict

from fastapi import WebSocket


class WebSocketManager:
    def __init__(self) -> None:
        self._connections: dict[str, set[WebSocket]] = defaultdict(set)
        self._lock = asyncio.Lock()

    async def subscribe(self, symbol: str, websocket: WebSocket) -> None:
        async with self._lock:
            self._connections[symbol.upper()].add(websocket)

    async def unsubscribe(self, websocket: WebSocket) -> None:
        async with self._lock:
            for symbol in list(self._connections.keys()):
                conns = self._connections[symbol]
                if websocket in conns:
                    conns.remove(websocket)
                if not conns:
                    del self._connections[symbol]

    async def broadcast(self, symbol: str, payload: dict) -> None:
        symbol_key = symbol.upper()
        conns = list(self._connections.get(symbol_key, set()))
        dead: list[WebSocket] = []
        for websocket in conns:
            try:
                await asyncio.wait_for(websocket.send_json(payload), timeout=1)
            except Exception:
                dead.append(websocket)
        for websocket in dead:
            await self.unsubscribe(websocket)


websocket_manager = WebSocketManager()
