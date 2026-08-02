from __future__ import annotations

import asyncio
from collections import defaultdict

from fastapi import WebSocket

from app.config import settings
from app.core.logging import get_logger

logger = get_logger()


class WebSocketManager:
    def __init__(self) -> None:
        self._connections: dict[str, set[WebSocket]] = defaultdict(set)
        # Track connection count per remote IP for connection-limit enforcement.
        self._ip_connections: dict[str, set[WebSocket]] = defaultdict(set)
        # Track per-connection subscription count.
        self._conn_subscriptions: dict[int, int] = {}
        self._lock = asyncio.Lock()

    # ------------------------------------------------------------------
    # Connection lifecycle
    # ------------------------------------------------------------------

    def register(self, websocket: WebSocket, client_ip: str) -> bool:
        """Register a new WebSocket connection.

        Returns True if the connection is accepted within the per-IP limit,
        False if it must be rejected.
        """
        ip_count = len(self._ip_connections.get(client_ip, set()))
        if ip_count >= settings.ws_max_connections_per_ip:
            return False
        self._ip_connections[client_ip].add(websocket)
        self._conn_subscriptions[id(websocket)] = 0
        return True

    def deregister(self, websocket: WebSocket, client_ip: str) -> None:
        ip_set = self._ip_connections.get(client_ip)
        if ip_set:
            ip_set.discard(websocket)
            if not ip_set:
                del self._ip_connections[client_ip]
        self._conn_subscriptions.pop(id(websocket), None)

    # ------------------------------------------------------------------
    # Subscription management
    # ------------------------------------------------------------------

    async def subscribe(self, symbol: str, websocket: WebSocket) -> bool:
        """Subscribe *websocket* to *symbol*.

        Returns False if the per-connection subscription cap is reached.
        """
        async with self._lock:
            current = self._conn_subscriptions.get(id(websocket), 0)
            if current >= settings.ws_max_subscriptions_per_connection:
                return False
            self._connections[symbol.upper()].add(websocket)
            self._conn_subscriptions[id(websocket)] = current + 1
            return True

    async def unsubscribe_symbol(self, symbol: str, websocket: WebSocket) -> None:
        async with self._lock:
            conns = self._connections.get(symbol.upper(), set())
            if websocket in conns:
                conns.discard(websocket)
                current = self._conn_subscriptions.get(id(websocket), 0)
                self._conn_subscriptions[id(websocket)] = max(0, current - 1)
            if not conns:
                self._connections.pop(symbol.upper(), None)

    async def unsubscribe(self, websocket: WebSocket) -> None:
        """Remove *websocket* from every symbol subscription."""
        async with self._lock:
            for symbol in list(self._connections.keys()):
                conns = self._connections[symbol]
                if websocket in conns:
                    conns.discard(websocket)
                if not conns:
                    del self._connections[symbol]
            self._conn_subscriptions.pop(id(websocket), None)

    # ------------------------------------------------------------------
    # Broadcasting
    # ------------------------------------------------------------------

    async def broadcast(self, symbol: str, payload: dict) -> None:  # type: ignore[type-arg]
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
