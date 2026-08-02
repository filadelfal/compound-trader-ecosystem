from __future__ import annotations

from redis.asyncio import Redis

from app.config import settings

redis_client = Redis.from_url(settings.redis_url, decode_responses=True)


async def connect_cache() -> None:
    await redis_client.ping()


async def check_cache() -> None:
    await redis_client.ping()


async def close_cache() -> None:
    await redis_client.close()
