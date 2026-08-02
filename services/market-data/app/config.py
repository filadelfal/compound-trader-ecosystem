from __future__ import annotations

from functools import lru_cache
from typing import Literal

from pydantic import field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    service_name: str = "market-data"
    port: int = 3004
    log_level: str = "INFO"
    database_url: str = "******postgres:5432/compound"
    redis_url: str = "redis://redis:6379/0"

    jwt_secret: str = "change-me"
    jwt_algorithm: str = "HS256"
    api_keys: str = ""
    rate_limit_requests: int = 120
    rate_limit_window_seconds: int = 60

    cors_allow_origins: str = "*"
    cors_allow_methods: str = "GET,POST,PUT,DELETE,OPTIONS"
    cors_allow_headers: str = "Authorization,Content-Type,X-API-Key,X-Correlation-ID"

    provider_priority: str = "twelvedata,alphavantage,polygon,binance,mt5"
    twelvedata_api_key: str | None = None
    alphavantage_api_key: str | None = None
    polygon_api_key: str | None = None
    mt5_base_url: str | None = None

    request_timeout_seconds: float = 5.0
    testing: bool = False

    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    @field_validator("log_level")
    @classmethod
    def validate_log_level(cls, value: str) -> str:
        return value.upper()

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


@lru_cache
def get_settings() -> Settings:
    return Settings()


settings = get_settings()

InstrumentType = Literal["forex", "crypto", "commodities", "indices"]
IntervalType = Literal["1m", "5m", "15m", "30m", "1h", "4h", "1d", "1w", "1mo"]
