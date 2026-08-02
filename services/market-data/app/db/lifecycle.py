from __future__ import annotations

from sqlalchemy import text

from app.db.session import engine


async def connect_database() -> None:
    async with engine.begin() as conn:
        await conn.execute(text("SELECT 1"))


async def check_database() -> None:
    async with engine.connect() as conn:
        await conn.execute(text("SELECT 1"))


async def close_database() -> None:
    await engine.dispose()
