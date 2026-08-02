"""Rate-limit middleware tests – atomicity, time-bucket expiration, Redis failure."""

from __future__ import annotations

import math
import time
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from fastapi import Request

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_request(path: str = "/prices/latest/EURUSD", method: str = "GET") -> MagicMock:
    req = MagicMock(spec=Request)
    req.url = MagicMock()
    req.url.path = path
    req.method = method
    req.query_params = {}
    return req


# ---------------------------------------------------------------------------
# Policy selection
# ---------------------------------------------------------------------------


def test_resolve_policy_default():
    from app.middleware.rate_limit import _resolve_policy

    req = _make_request("/prices/latest/BTC")
    policy, limit, window = _resolve_policy(req, "user1")
    assert policy == "default"


def test_resolve_policy_force_refresh():
    from app.middleware.rate_limit import _resolve_policy

    req = _make_request("/prices/latest/BTC")
    req.query_params = {"force_refresh": "true"}
    policy, limit, window = _resolve_policy(req, "user1")
    assert policy == "force_refresh"
    assert limit < 120  # stricter than default


def test_resolve_policy_symbol_write():
    from app.middleware.rate_limit import _resolve_policy

    req = _make_request("/api/v1/symbols/BTC", method="POST")
    policy, limit, window = _resolve_policy(req, "user1")
    assert policy == "symbol_write"


def test_resolve_policy_ws_connect():
    from app.middleware.rate_limit import _resolve_policy

    req = _make_request("/api/v1/ws/prices")
    policy, limit, window = _resolve_policy(req, "user1")
    assert policy == "ws_connect"


def test_resolve_policy_historical_fetch():
    from app.middleware.rate_limit import _resolve_policy

    req = _make_request("/api/v1/historical/fetch/BTC/1h", method="POST")
    policy, limit, window = _resolve_policy(req, "user1")
    assert policy == "historical_fetch"


# ---------------------------------------------------------------------------
# Time-bucketed key format
# ---------------------------------------------------------------------------


def test_bucket_key_is_time_bucketed():
    from app.middleware.rate_limit import _bucket_key

    window = 60
    bucket_now = math.floor(time.time() / window)
    key = _bucket_key("user1", "default", window)
    assert f":{bucket_now}" in key, "Key must embed current time bucket"


# ---------------------------------------------------------------------------
# Redis-failure: fail open
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_enforce_rate_limit_fails_open_on_redis_error():
    """When Redis is unavailable the request must pass through (fail open)."""
    from app.middleware.rate_limit import enforce_rate_limit

    req = _make_request()
    req.state = MagicMock()

    with patch("app.middleware.rate_limit.redis_client") as mock_redis:
        mock_redis.eval = AsyncMock(side_effect=ConnectionError("redis down"))
        # Should not raise.
        await enforce_rate_limit(req, "user1")


# ---------------------------------------------------------------------------
# 429 when limit exceeded
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_enforce_rate_limit_blocks_when_over_limit():
    from fastapi import HTTPException

    from app.middleware.rate_limit import enforce_rate_limit

    req = _make_request()
    req.state = MagicMock()

    with patch("app.middleware.rate_limit.redis_client") as mock_redis:
        # Simulate count=999 (far over default limit of 120), ttl=30.
        mock_redis.eval = AsyncMock(return_value=[999, 30])
        with pytest.raises(HTTPException) as exc_info:
            await enforce_rate_limit(req, "user1")
    assert exc_info.value.status_code == 429
    assert "Retry-After" in exc_info.value.headers


# ---------------------------------------------------------------------------
# Concurrency: separate buckets for different policies/identities
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_separate_policy_keys_are_independent():
    from app.middleware.rate_limit import _bucket_key

    key1 = _bucket_key("alice", "default", 60)
    key2 = _bucket_key("alice", "force_refresh", 60)
    key3 = _bucket_key("bob", "default", 60)

    assert key1 != key2  # different policies
    assert key1 != key3  # different identities
    assert key2 != key3


# ---------------------------------------------------------------------------
# WS message rate limit
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_ws_message_rate_limit_blocks():
    from app.middleware.rate_limit import enforce_ws_message_rate_limit

    with patch("app.middleware.rate_limit.redis_client") as mock_redis:
        mock_redis.eval = AsyncMock(return_value=[500, 30])  # way over limit
        with pytest.raises(RuntimeError, match="rate_limit"):
            await enforce_ws_message_rate_limit("ws-user")


@pytest.mark.asyncio
async def test_ws_message_rate_limit_fails_open_on_redis_error():
    from app.middleware.rate_limit import enforce_ws_message_rate_limit

    with patch("app.middleware.rate_limit.redis_client") as mock_redis:
        mock_redis.eval = AsyncMock(side_effect=ConnectionError("down"))
        # Should not raise.
        await enforce_ws_message_rate_limit("ws-user")
