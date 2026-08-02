from __future__ import annotations

import asyncio
from datetime import UTC, datetime
from time import perf_counter

import httpx

from app.cache import redis_client
from app.config import settings
from app.core.logging import get_logger
from app.db.models import CandleInterval
from app.providers.alphavantage import AlphaVantageProvider
from app.providers.base import MarketDataProvider, ProviderCandle, ProviderError, ProviderPrice
from app.providers.binance import BinanceProvider
from app.providers.mt5 import MT5Provider
from app.providers.polygon import PolygonProvider
from app.providers.twelvedata import TwelveDataProvider
from app.schemas import ProviderStatus

logger = get_logger()


class ProviderFailoverService:
    def __init__(self) -> None:
        self.client = httpx.AsyncClient(timeout=settings.request_timeout_seconds)
        self.providers: dict[str, MarketDataProvider] = {
            "twelvedata": TwelveDataProvider(self.client),
            "alphavantage": AlphaVantageProvider(self.client),
            "polygon": PolygonProvider(self.client),
            "binance": BinanceProvider(self.client),
            "mt5": MT5Provider(self.client),
        }

    async def close(self) -> None:
        await self.client.aclose()

    async def _set_status(self, status_obj: ProviderStatus) -> None:
        """Best-effort telemetry write.  A Redis failure must never discard
        a valid provider result – the caller always gets the data back first."""
        key = f"provider_status:{status_obj.provider}"
        try:
            await redis_client.hset(
                key,
                mapping={
                    "healthy": str(status_obj.healthy).lower(),
                    "latency_ms": "" if status_obj.latency_ms is None else str(status_obj.latency_ms),
                    "error": status_obj.error or "",
                    "checked_at": status_obj.checked_at.isoformat(),
                },
            )
            await redis_client.expire(key, 300)
        except asyncio.CancelledError:
            # Re-raise cancellation – never swallow it.
            raise
        except Exception as exc:
            # Status writes are best-effort; log the failure without exposing
            # provider data or secrets, then carry on.
            logger.warning(
                "provider_status_write_failed",
                provider=status_obj.provider,
                error_type=type(exc).__name__,
            )

    async def get_provider_status(self) -> list[ProviderStatus]:
        rows: list[ProviderStatus] = []
        for name in self.providers:
            key = f"provider_status:{name}"
            try:
                state = await redis_client.hgetall(key)
            except Exception as exc:
                logger.warning("provider_status_read_failed", provider=name, error_type=type(exc).__name__)
                state = {}
            if not state:
                rows.append(
                    ProviderStatus(
                        provider=name,
                        healthy=False,
                        latency_ms=None,
                        error="not_checked",
                        checked_at=datetime.now(UTC),
                    )
                )
                continue
            rows.append(
                ProviderStatus(
                    provider=name,
                    healthy=state.get("healthy") == "true",
                    latency_ms=float(state["latency_ms"]) if state.get("latency_ms") else None,
                    error=state.get("error") or None,
                    checked_at=datetime.fromisoformat(state["checked_at"]),
                )
            )
        return rows

    async def _execute_with_failover(self, operation_name: str, symbol: str, handler) -> object:  # type: ignore[type-arg]
        errors: list[str] = []

        for provider_name in settings.parsed_provider_priority:
            provider = self.providers.get(provider_name)
            if provider is None:
                continue

            start = perf_counter()
            try:
                result = await handler(provider)
            except asyncio.CancelledError:
                # Propagate task-cancellation; do not count it as a provider error.
                raise
            except ProviderError as exc:
                latency_ms = round((perf_counter() - start) * 1000, 2)
                errors.append(f"{provider_name}:{exc}")
                # Record failure telemetry in the background so that it cannot
                # block or discard a result from a later provider.
                asyncio.ensure_future(
                    self._set_status(
                        ProviderStatus(
                            provider=provider_name,
                            healthy=False,
                            latency_ms=latency_ms,
                            error=str(exc),
                            checked_at=datetime.now(UTC),
                        )
                    )
                )
                continue
            except Exception:
                # Unexpected (programming) errors are NOT swallowed.
                raise

            latency_ms = round((perf_counter() - start) * 1000, 2)
            # Fire-and-forget: telemetry failure must not discard the result.
            asyncio.ensure_future(
                self._set_status(
                    ProviderStatus(
                        provider=provider_name,
                        healthy=True,
                        latency_ms=latency_ms,
                        error=None,
                        checked_at=datetime.now(UTC),
                    )
                )
            )
            return result

        raise ProviderError(f"all providers failed for {operation_name} on {symbol}: {' | '.join(errors)}")

    async def fetch_latest_price(self, symbol: str) -> ProviderPrice:
        result = await self._execute_with_failover(
            "latest_price", symbol, lambda provider: provider.fetch_latest_price(symbol)
        )
        return result  # type: ignore[return-value]

    async def fetch_historical(self, symbol: str, interval: CandleInterval, limit: int = 100) -> list[ProviderCandle]:
        result = await self._execute_with_failover(
            "historical", symbol, lambda provider: provider.fetch_historical(symbol, interval=interval, limit=limit)
        )
        return result  # type: ignore[return-value]
