from __future__ import annotations

from datetime import datetime
from decimal import Decimal
from uuid import UUID

from pydantic import BaseModel, ConfigDict, Field, field_validator

from app.db.models import CandleInterval, InstrumentType


class SymbolCreate(BaseModel):
    symbol: str = Field(min_length=2, max_length=64)
    name: str = Field(min_length=2, max_length=255)
    instrument_type: InstrumentType
    base_asset: str | None = Field(default=None, max_length=32)
    quote_asset: str | None = Field(default=None, max_length=32)
    exchange: str | None = Field(default=None, max_length=64)
    is_active: bool = True

    @field_validator("symbol")
    @classmethod
    def normalize_symbol(cls, value: str) -> str:
        return value.upper().strip()


class SymbolUpdate(BaseModel):
    name: str | None = Field(default=None, min_length=2, max_length=255)
    instrument_type: InstrumentType | None = None
    base_asset: str | None = Field(default=None, max_length=32)
    quote_asset: str | None = Field(default=None, max_length=32)
    exchange: str | None = Field(default=None, max_length=64)
    is_active: bool | None = None


class SymbolRead(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: UUID
    symbol: str
    name: str
    instrument_type: InstrumentType
    base_asset: str | None
    quote_asset: str | None
    exchange: str | None
    is_active: bool
    created_at: datetime
    updated_at: datetime


class OHLCVIngest(BaseModel):
    interval: CandleInterval
    open: Decimal = Field(gt=0)
    high: Decimal = Field(gt=0)
    low: Decimal = Field(gt=0)
    close: Decimal = Field(gt=0)
    volume: Decimal = Field(ge=0)
    timestamp: datetime
    provider: str = Field(min_length=2, max_length=64)

    @field_validator("high")
    @classmethod
    def validate_high(cls, value: Decimal, info) -> Decimal:  # type: ignore[no-untyped-def]
        low = info.data.get("low")
        if low is not None and value < low:
            raise ValueError("high must be greater than or equal to low")
        return value


class OHLCVRead(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    symbol: str
    interval: CandleInterval
    open: Decimal
    high: Decimal
    low: Decimal
    close: Decimal
    volume: Decimal
    provider: str
    timestamp: datetime


class LatestPrice(BaseModel):
    symbol: str
    price: Decimal
    timestamp: datetime
    provider: str


class ProviderStatus(BaseModel):
    provider: str
    healthy: bool
    latency_ms: float | None
    error: str | None
    checked_at: datetime
