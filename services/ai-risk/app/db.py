import asyncpg
from app.config import settings

_pool: asyncpg.Pool | None = None

async def connect_database() -> None:
    global _pool
    if _pool is None:
        _pool = await asyncpg.create_pool(settings.database_url, min_size=1, max_size=5)

async def check_database() -> None:
    if _pool is None:
        raise RuntimeError("database pool is not initialized")
    async with _pool.acquire() as connection:
        await connection.fetchval("SELECT 1")

async def close_database() -> None:
    global _pool
    if _pool is not None:
        await _pool.close()
        _pool = None
