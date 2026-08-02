"""initial market data tables

Revision ID: 20260802_01
Revises:
Create Date: 2026-08-02 00:00:00.000000
"""

from __future__ import annotations

from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects import postgresql


revision = "20260802_01"
down_revision = None
branch_labels = None
depends_on = None


instrument_type = sa.Enum("forex", "crypto", "commodities", "indices", name="instrument_type")
candle_interval = sa.Enum("1m", "5m", "15m", "30m", "1h", "4h", "1d", "1w", "1mo", name="candle_interval")


def upgrade() -> None:
    instrument_type.create(op.get_bind(), checkfirst=True)
    candle_interval.create(op.get_bind(), checkfirst=True)

    op.create_table(
        "symbols",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True, nullable=False),
        sa.Column("symbol", sa.String(length=64), nullable=False, unique=True),
        sa.Column("name", sa.String(length=255), nullable=False),
        sa.Column("instrument_type", instrument_type, nullable=False),
        sa.Column("base_asset", sa.String(length=32), nullable=True),
        sa.Column("quote_asset", sa.String(length=32), nullable=True),
        sa.Column("exchange", sa.String(length=64), nullable=True),
        sa.Column("is_active", sa.Boolean(), nullable=False, server_default=sa.true()),
        sa.Column("created_at", sa.DateTime(timezone=True), server_default=sa.func.now(), nullable=False),
        sa.Column("updated_at", sa.DateTime(timezone=True), server_default=sa.func.now(), nullable=False),
    )
    op.create_index("ix_symbols_symbol", "symbols", ["symbol"], unique=True)

    op.create_table(
        "ohlcv",
        sa.Column("id", sa.Integer(), primary_key=True, autoincrement=True),
        sa.Column("symbol_id", postgresql.UUID(as_uuid=True), sa.ForeignKey("symbols.id", ondelete="CASCADE"), nullable=False),
        sa.Column("interval", candle_interval, nullable=False),
        sa.Column("open", sa.Numeric(20, 10), nullable=False),
        sa.Column("high", sa.Numeric(20, 10), nullable=False),
        sa.Column("low", sa.Numeric(20, 10), nullable=False),
        sa.Column("close", sa.Numeric(20, 10), nullable=False),
        sa.Column("volume", sa.Numeric(28, 10), nullable=False),
        sa.Column("provider", sa.String(length=64), nullable=False),
        sa.Column("timestamp", sa.DateTime(timezone=True), nullable=False),
        sa.Column("created_at", sa.DateTime(timezone=True), server_default=sa.func.now(), nullable=False),
        sa.UniqueConstraint("symbol_id", "interval", "timestamp", name="uq_ohlcv_symbol_interval_ts"),
    )
    op.create_index("ix_ohlcv_symbol_id", "ohlcv", ["symbol_id"], unique=False)
    op.create_index("ix_ohlcv_interval", "ohlcv", ["interval"], unique=False)
    op.create_index("ix_ohlcv_timestamp", "ohlcv", ["timestamp"], unique=False)


def downgrade() -> None:
    op.drop_index("ix_ohlcv_timestamp", table_name="ohlcv")
    op.drop_index("ix_ohlcv_interval", table_name="ohlcv")
    op.drop_index("ix_ohlcv_symbol_id", table_name="ohlcv")
    op.drop_table("ohlcv")

    op.drop_index("ix_symbols_symbol", table_name="symbols")
    op.drop_table("symbols")

    candle_interval.drop(op.get_bind(), checkfirst=True)
    instrument_type.drop(op.get_bind(), checkfirst=True)
