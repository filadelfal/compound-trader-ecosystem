from __future__ import annotations

from contextlib import asynccontextmanager

from fastapi import FastAPI, HTTPException, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse
from prometheus_fastapi_instrumentator import Instrumentator
from starlette.middleware.base import BaseHTTPMiddleware

from app.api.routes import router as api_router
from app.cache import check_cache, close_cache, connect_cache
from app.config import settings
from app.core.logging import configure_logging, get_logger
from app.db.lifecycle import check_database, close_database, connect_database
from app.dependencies import set_provider_service
from app.services.provider_service import ProviderFailoverService

configure_logging()
logger = get_logger()


# ---------------------------------------------------------------------------
# Request-body size guard
# ---------------------------------------------------------------------------


class _MaxBodySizeMiddleware(BaseHTTPMiddleware):
    """Reject requests whose body exceeds the configured maximum."""

    async def dispatch(self, request: Request, call_next):  # type: ignore[override]
        content_length = request.headers.get("content-length")
        if content_length and int(content_length) > settings.max_request_body_bytes:
            return JSONResponse(status_code=413, content={"detail": "request body too large"})
        return await call_next(request)


# ---------------------------------------------------------------------------
# Application lifespan
# ---------------------------------------------------------------------------


@asynccontextmanager
async def lifespan(_app: FastAPI):
    provider_service = ProviderFailoverService()
    set_provider_service(provider_service)

    if not settings.testing:
        await connect_database()
        await connect_cache()
    logger.info("service_started", port=settings.port)
    yield
    await provider_service.close()
    if not settings.testing:
        await close_cache()
        await close_database()
    logger.info("service_stopped")


app = FastAPI(title=settings.service_name, version="1.0.0", lifespan=lifespan)

app.add_middleware(_MaxBodySizeMiddleware)
app.add_middleware(
    CORSMiddleware,
    allow_origins=settings.parsed_cors_origins,
    allow_methods=settings.parsed_cors_methods,
    allow_headers=settings.parsed_cors_headers,
)

from app.middleware.correlation import CorrelationIdMiddleware  # noqa: E402

app.add_middleware(CorrelationIdMiddleware)
Instrumentator().instrument(app).expose(app, include_in_schema=False)


@app.get("/health")
async def health() -> dict[str, str]:
    return {"status": "ok", "service": settings.service_name}


@app.get("/live")
async def live() -> dict[str, str]:
    return {"status": "alive", "service": settings.service_name}


@app.get("/ready")
async def ready() -> dict[str, bool | str]:
    if settings.testing:
        return {"ready": True, "service": settings.service_name}
    try:
        await check_database()
        await check_cache()
    except Exception as exc:
        # Log the full error internally (with correlation ID) but never expose
        # raw exception text to callers.
        logger.error("readiness_check_failed", error_type=type(exc).__name__, error=str(exc))
        raise HTTPException(status_code=503, detail="service unavailable") from exc
    return {"ready": True, "service": settings.service_name}


app.include_router(api_router)


@app.get("/api/v1/ping")
async def ping() -> dict[str, str]:
    return {"message": "pong", "service": settings.service_name}
