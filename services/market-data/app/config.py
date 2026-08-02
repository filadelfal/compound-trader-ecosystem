from __future__ import annotations

import sys
from functools import lru_cache
from typing import Literal

from pydantic import field_validator, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

# Known development / placeholder values that must never reach production.
_WEAK_JWT_SECRETS: frozenset[str] = frozenset(
    {
        "",
        "change-me",
        "changeme",
        "secret",
        "dev-secret",
        "development",
        "test-secret",
        "your-secret-here",
    }
)


class Settings(BaseSettings):
    service_name: str = "market-data"
    environment: str = "development"  # development | staging | production
    port: int = 3004
    log_level: str = "INFO"
    database_url: str = "******postgres:5432/compound"
    redis_url: str = "redis://redis:6379/0"

    # JWT secret has NO default – callers must supply it (or set TESTING=true).
    jwt_secret: str = ""
    jwt_algorithm: str = "HS256"
    jwt_issuer: str = "compound-trader"
    jwt_audience: str = "market-data"
    # Approved algorithms: reject anything outside this list.
    jwt_approved_algorithms: str = "HS256,HS512"

    api_keys: str = ""
    rate_limit_requests: int = 120
    rate_limit_window_seconds: int = 60

    # Default to restrictive; operator must explicitly set "*" for dev only.
    cors_allow_origins: str = ""
    cors_allow_methods: str = "GET,POST,PUT,DELETE,OPTIONS"
    cors_allow_headers: str = "Authorization,Content-Type,X-API-Key,X-Correlation-ID"

    provider_priority: str = "twelvedata,alphavantage,polygon,binance,mt5"
    twelvedata_api_key: str | None = None
    alphavantage_api_key: str | None = None
    polygon_api_key: str | None = None
    mt5_base_url: str | None = None

    request_timeout_seconds: float = 5.0

    # WebSocket connection/subscription limits
    ws_max_connections_per_ip: int = 10
    ws_max_subscriptions_per_connection: int = 50
    ws_idle_timeout_seconds: float = 60.0
    ws_max_message_bytes: int = 4096

    # Request body size limit (bytes)
    max_request_body_bytes: int = 1_048_576  # 1 MiB

    testing: bool = False

    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    @field_validator("log_level")
    @classmethod
    def validate_log_level(cls, value: str) -> str:
        return value.upper()

    @model_validator(mode="after")
    def validate_security_config(self) -> Settings:
        env = self.environment.lower()
        is_strict = env in ("staging", "production")

        if not self.testing:
            # JWT secret must be present and non-weak in staging/production.
            if is_strict:
                if not self.jwt_secret or self.jwt_secret.lower() in _WEAK_JWT_SECRETS:
                    print(  # noqa: T201
                        f"FATAL: JWT_SECRET is missing or weak in {env} environment. " "Set a strong random secret.",
                        file=sys.stderr,
                    )
                    sys.exit(1)

            # CORS wildcard forbidden in production.
            if env == "production" and "*" in self.cors_allow_origins:
                print(  # noqa: T201
                    "FATAL: CORS_ALLOW_ORIGINS cannot be '*' in production. " "Specify explicit allowed origins.",
                    file=sys.stderr,
                )
                sys.exit(1)

        return self

    @property
    def parsed_api_keys(self) -> set[str]:
        return {key.strip() for key in self.api_keys.split(",") if key.strip()}

    @property
    def parsed_provider_priority(self) -> list[str]:
        return [provider.strip().lower() for provider in self.provider_priority.split(",") if provider.strip()]

    @property
    def parsed_cors_origins(self) -> list[str]:
        return [origin.strip() for origin in self.cors_allow_origins.split(",") if origin.strip()]

    @property
    def parsed_cors_methods(self) -> list[str]:
        return [method.strip().upper() for method in self.cors_allow_methods.split(",") if method.strip()]

    @property
    def parsed_cors_headers(self) -> list[str]:
        return [header.strip() for header in self.cors_allow_headers.split(",") if header.strip()]

    @property
    def parsed_jwt_approved_algorithms(self) -> list[str]:
        return [alg.strip() for alg in self.jwt_approved_algorithms.split(",") if alg.strip()]


@lru_cache
def get_settings() -> Settings:
    return Settings()


settings = get_settings()

InstrumentType = Literal["forex", "crypto", "commodities", "indices"]
IntervalType = Literal["1m", "5m", "15m", "30m", "1h", "4h", "1d", "1w", "1mo"]
