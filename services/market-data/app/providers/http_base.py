from __future__ import annotations

from decimal import Decimal

import httpx

from app.providers.base import ProviderError


def decimal_from_payload(value: str | int | float | Decimal | None, field_name: str) -> Decimal:
    if value is None:
        raise ProviderError(f"missing field {field_name}")
    try:
        return Decimal(str(value))
    except Exception as exc:
        raise ProviderError(f"invalid decimal field {field_name}") from exc


async def get_json(client: httpx.AsyncClient, url: str, params: dict[str, str] | None = None) -> dict:
    response = await client.get(url, params=params)
    response.raise_for_status()
    payload = response.json()
    if not isinstance(payload, dict):
        raise ProviderError("provider response must be an object")
    return payload
