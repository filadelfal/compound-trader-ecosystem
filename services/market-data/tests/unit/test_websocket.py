"""WebSocket authentication, subscription and abuse-limit tests."""
# NOTE: do NOT add `from __future__ import annotations` here.
# FastAPI resolves WebSocket type hints at route-definition time; with
# lazy annotations that lookup fails for locally-scoped handler functions.

from datetime import UTC, datetime, timedelta
from unittest.mock import AsyncMock, MagicMock, patch

import jwt
import pytest
from fastapi.testclient import TestClient

from app.config import settings


def _make_token(
    *,
    sub: str = "ws-user",
    exp_delta: timedelta = timedelta(hours=1),
    iss: str | None = None,
    aud: str | None = None,
) -> str:
    now = datetime.now(UTC)
    payload = {
        "sub": sub,
        "iss": iss or settings.jwt_issuer,
        "aud": aud or settings.jwt_audience,
        "iat": int(now.timestamp()),
        "exp": int((now + exp_delta).timestamp()),
        "scope": "market-data:read",
    }
    return jwt.encode(payload, settings.jwt_secret, algorithm="HS256")


def _app():
    """Return a fresh FastAPI app with the WS router (no real DB/Redis)."""
    import importlib

    # Patch Redis before importing routes.
    from app.websocket.manager import WebSocketManager

    manager = WebSocketManager()

    with (
        patch("app.cache.redis_client") as mock_redis,
        patch("app.websocket.manager.websocket_manager", manager),
        patch("app.api.routes.websocket_manager", manager),
        patch("app.middleware.rate_limit.redis_client", mock_redis),
    ):
        mock_redis.smembers = AsyncMock(return_value=set())
        mock_redis.eval = AsyncMock(return_value=[1, 60])

        import app.api.routes as routes_module

        importlib.reload(routes_module)

        from fastapi import FastAPI

        a = FastAPI()
        a.include_router(routes_module.router)
        return a, manager


# ---------------------------------------------------------------------------
# Authentication – reject before accepting
# ---------------------------------------------------------------------------


def test_ws_rejects_without_credentials():
    """No token → close with code 4001 before accepting."""
    from fastapi import FastAPI, WebSocket

    app = FastAPI()

    @app.websocket("/ws/prices")
    async def _ws(websocket: WebSocket) -> None:
        from app.api.routes import _ws_authenticate as auth

        identity = await auth(websocket)
        if identity is None:
            return
        await websocket.accept()
        await websocket.send_text("ok")

    with TestClient(app) as client:
        with pytest.raises(Exception):
            with client.websocket_connect("/ws/prices"):
                pass


def test_ws_accepts_valid_jwt_query_param():
    token = _make_token()
    from fastapi import FastAPI, WebSocket

    app = FastAPI()

    @app.websocket("/ws/check")
    async def _ws(websocket: WebSocket) -> None:
        from app.api.routes import _ws_authenticate

        identity = await _ws_authenticate(websocket)
        if identity is None:
            return
        await websocket.accept()
        await websocket.send_text(identity)
        await websocket.close()

    with TestClient(app) as client:
        with client.websocket_connect("/ws/check?token=" + token) as ws:
            msg = ws.receive_text()
    assert msg == "ws-user"


def test_ws_rejects_expired_jwt():
    token = _make_token(exp_delta=timedelta(seconds=-5))
    from fastapi import FastAPI, WebSocket

    app = FastAPI()

    @app.websocket("/ws/check")
    async def _ws(websocket: WebSocket) -> None:
        from app.api.routes import _ws_authenticate

        identity = await _ws_authenticate(websocket)
        # If None, websocket was closed with an error code.
        if identity is None:
            return
        await websocket.accept()
        await websocket.close()

    with TestClient(app) as client:
        with pytest.raises(Exception):
            with client.websocket_connect("/ws/check?token=" + token) as ws:
                ws.receive_text()


def test_ws_accepts_valid_api_key():
    from fastapi import FastAPI, WebSocket

    app = FastAPI()

    @app.websocket("/ws/check")
    async def _ws(websocket: WebSocket) -> None:
        from app.api.routes import _ws_authenticate

        identity = await _ws_authenticate(websocket)
        if identity is None:
            return
        await websocket.accept()
        await websocket.send_text(identity)
        await websocket.close()

    with TestClient(app) as client:
        with client.websocket_connect("/ws/check", headers={"X-API-Key": "test-key"}) as ws:
            msg = ws.receive_text()
    assert msg.startswith("apikey:")


def test_ws_rejects_invalid_api_key():
    from fastapi import FastAPI, WebSocket

    app = FastAPI()

    @app.websocket("/ws/check")
    async def _ws(websocket: WebSocket) -> None:
        from app.api.routes import _ws_authenticate

        identity = await _ws_authenticate(websocket)
        if identity is None:
            return
        await websocket.accept()
        await websocket.close()

    with TestClient(app) as client:
        with pytest.raises(Exception):
            with client.websocket_connect("/ws/check", headers={"X-API-Key": "bad-key"}) as ws:
                ws.receive_text()


# ---------------------------------------------------------------------------
# Subscription limit
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_subscription_limit_enforced():
    from app.config import settings
    from app.websocket.manager import WebSocketManager

    manager = WebSocketManager()
    mock_ws = MagicMock()
    mock_ws.__hash__ = lambda self: 1
    client_ip = "1.2.3.4"
    manager.register(mock_ws, client_ip)

    for _ in range(settings.ws_max_subscriptions_per_connection):
        accepted = await manager.subscribe(f"SYM{_}", mock_ws)
        assert accepted

    # Next subscription should be rejected.
    rejected = await manager.subscribe("OVERFLOW", mock_ws)
    assert rejected is False


# ---------------------------------------------------------------------------
# Connection limit
# ---------------------------------------------------------------------------


def test_connection_limit_enforced():
    from app.websocket.manager import WebSocketManager

    manager = WebSocketManager()
    client_ip = "10.0.0.1"

    mocks = []
    for i in range(settings.ws_max_connections_per_ip):
        m = MagicMock()
        m.__hash__ = lambda self, _i=i: _i
        accepted = manager.register(m, client_ip)
        assert accepted
        mocks.append(m)

    extra = MagicMock()
    extra.__hash__ = lambda self: 999
    rejected = manager.register(extra, client_ip)
    assert rejected is False


# ---------------------------------------------------------------------------
# Disconnect cleanup
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_deregister_removes_ip_tracking():
    from app.websocket.manager import WebSocketManager

    manager = WebSocketManager()
    client_ip = "192.168.1.1"
    mock_ws = MagicMock()
    mock_ws.__hash__ = lambda self: 42

    manager.register(mock_ws, client_ip)
    assert client_ip in manager._ip_connections

    await manager.unsubscribe(mock_ws)
    manager.deregister(mock_ws, client_ip)
    assert client_ip not in manager._ip_connections
