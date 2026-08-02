from __future__ import annotations

import logging
import sys
from contextvars import ContextVar

import structlog

from app.config import settings

correlation_id_ctx: ContextVar[str] = ContextVar("correlation_id", default="")


def add_correlation_id(
    _logger: structlog.types.WrappedLogger, _method_name: str, event_dict: structlog.types.EventDict
) -> structlog.types.EventDict:
    correlation_id = correlation_id_ctx.get()
    if correlation_id:
        event_dict["correlation_id"] = correlation_id
    return event_dict


def configure_logging() -> None:
    timestamper = structlog.processors.TimeStamper(fmt="iso", utc=True)

    logging.basicConfig(format="%(message)s", stream=sys.stdout, level=settings.log_level)

    structlog.configure(
        processors=[
            structlog.contextvars.merge_contextvars,
            add_correlation_id,
            structlog.stdlib.add_log_level,
            timestamper,
            structlog.processors.StackInfoRenderer(),
            structlog.processors.format_exc_info,
            structlog.processors.JSONRenderer(),
        ],
        logger_factory=structlog.stdlib.LoggerFactory(),
        wrapper_class=structlog.stdlib.BoundLogger,
        cache_logger_on_first_use=True,
    )


def get_logger() -> structlog.stdlib.BoundLogger:
    return structlog.get_logger(service=settings.service_name)
