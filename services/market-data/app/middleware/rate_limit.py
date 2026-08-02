from __future__ import annotations

from fastapi import HTTPException, Request

from app.cache import redis_client
from app.config import settings


async def enforce_rate_limit(request: Request, identity: str) -> None:
    endpoint = request.url.path
    window = settings.rate_limit_window_seconds
    limit = settings.rate_limit_requests
    key = f"rate_limit:{identity}:{endpoint}:{window}"

    count = await redis_client.incr(key)
    if count == 1:
        await redis_client.expire(key, window)
    if count > limit:
        raise HTTPException(status_code=429, detail="rate limit exceeded")
