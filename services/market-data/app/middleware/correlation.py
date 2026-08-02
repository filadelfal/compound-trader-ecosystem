from __future__ import annotations

import time
import uuid

from fastapi import Request
from starlette.middleware.base import BaseHTTPMiddleware

from app.core.logging import correlation_id_ctx, get_logger


class CorrelationIdMiddleware(BaseHTTPMiddleware):
    async def dispatch(self, request: Request, call_next):  # type: ignore[override]
        correlation_id = request.headers.get("X-Correlation-ID") or str(uuid.uuid4())
        token = correlation_id_ctx.set(correlation_id)
        request.state.correlation_id = correlation_id

        logger = get_logger()
        started = time.perf_counter()
        try:
            response = await call_next(request)
            return response
        finally:
            elapsed_ms = round((time.perf_counter() - started) * 1000, 2)
            logger.info(
                "request_processed",
                method=request.method,
                path=request.url.path,
                status_code=getattr(locals().get("response", None), "status_code", 500),
                duration_ms=elapsed_ms,
            )
            correlation_id_ctx.reset(token)
