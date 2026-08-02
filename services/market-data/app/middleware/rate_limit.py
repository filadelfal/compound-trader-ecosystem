from __future__ import annotations

import math
import time

from fastapi import HTTPException, Request, Response, status

from app.cache import redis_client
from app.config import settings
from app.core.logging import get_logger

logger = get_logger()

# ---------------------------------------------------------------------------
# Per-endpoint rate-limit policies
# (requests, window_seconds)
# ---------------------------------------------------------------------------
_POLICIES: dict[str, tuple[int, int]] = {
    # Forced refresh – more expensive, stricter limit.
    "force_refresh": (20, 60),
    # Historical fetch from external provider.
    "historical_fetch": (30, 60),
    # Symbol mutations.
    "symbol_write": (60, 60),
    # WebSocket new connections per IP.
    "ws_connect": (20, 60),
    # WebSocket inbound messages per connection.
    "ws_message": (200, 60),
    # Default for all other reads.
    "default": (settings.rate_limit_requests, settings.rate_limit_window_seconds),
}


def _resolve_policy(request: Request, identity: str) -> tuple[str, int, int]:
    """Return (policy_name, limit, window) for the current request."""
    path = request.url.path
    method = request.method.upper()

    force = request.query_params.get("force_refresh", "").lower() in ("1", "true")

    if "/ws/" in path:
        return "ws_connect", *_POLICIES["ws_connect"]
    if force and "/prices/latest/" in path:
        return "force_refresh", *_POLICIES["force_refresh"]
    if method == "POST" and "/historical/fetch/" in path:
        return "historical_fetch", *_POLICIES["historical_fetch"]
    if method in ("POST", "PUT", "DELETE") and "/symbols" in path:
        return "symbol_write", *_POLICIES["symbol_write"]

    policy = "default"
    limit, window = _POLICIES["default"]
    return policy, limit, window


# ---------------------------------------------------------------------------
# Atomic Lua script: increment a time-bucketed counter and set TTL in one
# round-trip.  Returns (current_count, ttl_seconds_remaining).
# ---------------------------------------------------------------------------
_RATE_LIMIT_LUA = """
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window = tonumber(ARGV[2])

local count = redis.call('INCR', key)
if count == 1 then
    redis.call('EXPIRE', key, window)
end
local ttl = redis.call('TTL', key)
return {count, ttl}
"""


def _bucket_key(identity: str, policy: str, window: int) -> str:
    """Generate a time-bucketed Redis key so windows reset cleanly."""
    bucket = math.floor(time.time() / window)
    return f"rl:{policy}:{identity}:{bucket}"


async def enforce_rate_limit(request: Request, identity: str) -> None:
    """Apply rate-limiting; raises HTTP 429 on excess and sets rate-limit headers."""
    _, limit, window = _resolve_policy(request, identity)
    policy_name = _resolve_policy(request, identity)[0]
    key = _bucket_key(identity, policy_name, window)

    try:
        result = await redis_client.eval(_RATE_LIMIT_LUA, 1, key, limit, window)  # type: ignore[arg-type]
        count, ttl = int(result[0]), int(result[1])
    except Exception:
        # Redis unavailable: fail open (log + continue) to avoid blocking all traffic.
        logger.warning("rate_limit_redis_unavailable", identity=identity, policy=policy_name)
        return

    remaining = max(0, limit - count)
    reset_ts = int(time.time()) + max(ttl, 0)

    # Attach standard rate-limit headers to the response when available.
    response: Response | None = None
    try:
        response = request.state.response  # set by some middlewares
    except AttributeError:
        pass

    if response is not None:
        response.headers["X-RateLimit-Limit"] = str(limit)
        response.headers["X-RateLimit-Remaining"] = str(remaining)
        response.headers["X-RateLimit-Reset"] = str(reset_ts)

    if count > limit:
        raise HTTPException(
            status_code=status.HTTP_429_TOO_MANY_REQUESTS,
            detail="rate limit exceeded",
            headers={
                "X-RateLimit-Limit": str(limit),
                "X-RateLimit-Remaining": "0",
                "X-RateLimit-Reset": str(reset_ts),
                "Retry-After": str(max(ttl, 0)),
            },
        )


async def enforce_ws_message_rate_limit(identity: str) -> None:
    """Rate-limit WebSocket messages. Raises RuntimeError on excess."""
    limit, window = _POLICIES["ws_message"]
    key = _bucket_key(identity, "ws_message", window)

    try:
        result = await redis_client.eval(_RATE_LIMIT_LUA, 1, key, limit, window)  # type: ignore[arg-type]
        count = int(result[0])
    except Exception:
        logger.warning("ws_rate_limit_redis_unavailable", identity=identity)
        return

    if count > limit:
        raise RuntimeError("ws_message_rate_limit_exceeded")
