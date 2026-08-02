from __future__ import annotations

from datetime import UTC, datetime, timedelta
from unittest.mock import AsyncMock, patch

import jwt
import pytest
from fastapi import Depends, FastAPI
from fastapi.testclient import TestClient

from app.config import settings
from app.security import _decode_jwt, require_scope


def _make_token(
    *,
    sub: str = "user1",
    iss: str | None = None,
    aud: str | None = None,
    exp_delta: timedelta = timedelta(hours=1),
    extra_claims: dict | None = None,
    algorithm: str = "HS256",
    secret: str | None = None,
) -> str:
    now = datetime.now(UTC)
    payload = {
        "sub": sub,
        "iss": iss or settings.jwt_issuer,
        "aud": aud or settings.jwt_audience,
        "iat": int(now.timestamp()),
        "exp": int((now + exp_delta).timestamp()),
    }
    if extra_claims:
        payload.update(extra_claims)
    return jwt.encode(payload, secret or settings.jwt_secret, algorithm=algorithm)


def _app_with_scope(scope: str) -> FastAPI:
    async def _noop(*_args, **_kwargs) -> None:  # type: ignore[return]
        return None

    app = FastAPI()

    @app.get("/private")
    async def private(_: dict = Depends(require_scope(scope))) -> dict[str, bool]:
        return {"ok": True}

    return app


def _patched_client(app: FastAPI) -> TestClient:
    with patch("app.security.enforce_rate_limit", new=AsyncMock()):
        return TestClient(app)


# ---------------------------------------------------------------------------
# Basic authentication
# ---------------------------------------------------------------------------


def test_auth_required() -> None:
    app = _app_with_scope("market-data:read")
    with patch("app.security.enforce_rate_limit", new=AsyncMock()):
        with TestClient(app) as client:
            response = client.get("/private")
    assert response.status_code == 401


def test_auth_with_api_key() -> None:
    app = _app_with_scope("market-data:read")
    with patch("app.security.enforce_rate_limit", new=AsyncMock()):
        with TestClient(app) as client:
            response = client.get("/private", headers={"X-API-Key": "test-key"})
    assert response.status_code == 200


def test_auth_with_valid_jwt() -> None:
    token = _make_token(extra_claims={"scope": "market-data:read"})
    app = _app_with_scope("market-data:read")
    with patch("app.security.enforce_rate_limit", new=AsyncMock()):
        with TestClient(app) as client:
            response = client.get("/private", headers={"Authorization": "Bearer " + token})
    assert response.status_code == 200


# ---------------------------------------------------------------------------
# JWT claim validation
# ---------------------------------------------------------------------------


def test_expired_token_rejected() -> None:
    token = _make_token(exp_delta=timedelta(seconds=-10))
    with pytest.raises(Exception) as exc_info:
        _decode_jwt(token)
    assert "expired" in str(exc_info.value.detail).lower()  # type: ignore[attr-defined]


def test_wrong_issuer_rejected() -> None:
    token = _make_token(iss="bad-issuer")
    with pytest.raises(Exception) as exc_info:
        _decode_jwt(token)
    assert "issuer" in str(exc_info.value.detail).lower()  # type: ignore[attr-defined]


def test_wrong_audience_rejected() -> None:
    token = _make_token(aud="wrong-audience")
    with pytest.raises(Exception) as exc_info:
        _decode_jwt(token)
    assert "audience" in str(exc_info.value.detail).lower()  # type: ignore[attr-defined]


def test_missing_sub_claim_rejected() -> None:
    now = datetime.now(UTC)
    # Build token without `sub` claim.
    payload = {
        "iss": settings.jwt_issuer,
        "aud": settings.jwt_audience,
        "iat": int(now.timestamp()),
        "exp": int((now + timedelta(hours=1)).timestamp()),
    }
    token = jwt.encode(payload, settings.jwt_secret, algorithm="HS256")
    with pytest.raises(Exception) as exc_info:
        _decode_jwt(token)
    # Missing 'sub' should trigger MissingRequiredClaimError → 401
    assert exc_info.value.status_code == 401  # type: ignore[attr-defined]


def test_disallowed_algorithm_rejected() -> None:
    # HS384 is not in the approved list by default.
    token = _make_token(algorithm="HS384")
    with pytest.raises(Exception) as exc_info:
        _decode_jwt(token)
    assert exc_info.value.status_code == 401  # type: ignore[attr-defined]


# ---------------------------------------------------------------------------
# Scope-based authorization
# ---------------------------------------------------------------------------


def test_insufficient_scope_returns_403() -> None:
    # Token has only 'market-data:read'; endpoint requires 'market-data:symbols:write'.
    token = _make_token(extra_claims={"scope": "market-data:read"})
    app = _app_with_scope("market-data:symbols:write")
    with patch("app.security.enforce_rate_limit", new=AsyncMock()):
        with TestClient(app) as client:
            response = client.get("/private", headers={"Authorization": "Bearer " + token})
    assert response.status_code == 403


def test_correct_scope_passes() -> None:
    token = _make_token(extra_claims={"scope": "market-data:read market-data:symbols:write"})
    app = _app_with_scope("market-data:symbols:write")
    with patch("app.security.enforce_rate_limit", new=AsyncMock()):
        with TestClient(app) as client:
            response = client.get("/private", headers={"Authorization": "Bearer " + token})
    assert response.status_code == 200


def test_api_key_bypasses_scope() -> None:
    """API-key callers have no JWT claims; they should pass scope checks."""
    app = _app_with_scope("market-data:ingest")
    with patch("app.security.enforce_rate_limit", new=AsyncMock()):
        with TestClient(app) as client:
            response = client.get("/private", headers={"X-API-Key": "test-key"})
    assert response.status_code == 200
