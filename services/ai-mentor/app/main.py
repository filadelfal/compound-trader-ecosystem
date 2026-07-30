from contextlib import asynccontextmanager
from fastapi import FastAPI, HTTPException
from prometheus_fastapi_instrumentator import Instrumentator
import structlog

from app.config import settings
from app.db import connect_database, check_database, close_database
from app.cache import connect_cache, check_cache, close_cache

logger = structlog.get_logger(service=settings.service_name)

@asynccontextmanager
async def lifespan(app: FastAPI):
    await connect_database()
    await connect_cache()
    logger.info("service_started", port=settings.port)
    yield
    await close_cache()
    await close_database()
    logger.info("service_stopped")

app = FastAPI(title=settings.service_name, lifespan=lifespan)
Instrumentator().instrument(app).expose(app)

@app.get("/health")
async def health():
    return {"status": "ok", "service": settings.service_name}

@app.get("/ready")
async def ready():
    try:
        await check_database()
        await check_cache()
    except Exception as exc:
        raise HTTPException(status_code=503, detail=str(exc)) from exc
    return {"ready": True, "service": settings.service_name}

@app.get("/api/v1/ping")
async def ping():
    return {"message": "pong", "service": settings.service_name}
