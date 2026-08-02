from __future__ import annotations

from typing import Any

import jwt
from fastapi import Depends, Header, HTTPException, Request, status
from fastapi.security import HTTPAuthorizationCredentials, HTTPBearer

from app.config import settings
from app.middleware.rate_limit import enforce_rate_limit

bearer_scheme = HTTPBearer(auto_error=False)

_REQUIRED_CLAIMS = ("exp", "iat", "sub", "iss", "aud")


def _decode_jwt(token: str) -> dict[str, Any]:
    """Decode and fully validate a JWT token."""
    try:
        claims: dict[str, Any] = jwt.decode(
            token,
            settings.jwt_secret,
            algorithms=settings.parsed_jwt_approved_algorithms,
            issuer=settings.jwt_issuer,
            audience=settings.jwt_audience,
            options={
                "require": list(_REQUIRED_CLAIMS),
                "verify_exp": True,
                "verify_iat": True,
            },
        )
    except jwt.ExpiredSignatureError as exc:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="token expired") from exc
    except jwt.InvalidIssuerError as exc:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="invalid token issuer") from exc
    except jwt.InvalidAudienceError as exc:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="invalid token audience") from exc
    except jwt.MissingRequiredClaimError as exc:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail=f"missing claim: {exc}") from exc
    except jwt.InvalidAlgorithmError as exc:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="disallowed algorithm") from exc
    except jwt.PyJWTError as exc:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="invalid jwt token") from exc
    return claims


async def authenticate_request(
    request: Request,
    credentials: HTTPAuthorizationCredentials | None = Depends(bearer_scheme),
    x_api_key: str | None = Header(default=None, alias="X-API-Key"),
) -> dict[str, Any]:
    identity: str | None = None
    claims: dict[str, Any] = {}

    if credentials is not None:
        claims = _decode_jwt(credentials.credentials)
        identity = str(claims.get("sub") or "jwt")

    if identity is None and x_api_key:
        if x_api_key not in settings.parsed_api_keys:
            # Never log the full key – only a suffix for tracing.
            raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="invalid api key")
        identity = f"apikey:{x_api_key[-6:]}"

    if identity is None:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="authentication required")

    await enforce_rate_limit(request, identity)
    return claims


def _get_token_scopes(claims: dict[str, Any]) -> set[str]:
    """Return the set of scopes carried by the token."""
    raw = claims.get("scope", claims.get("scopes", ""))
    if isinstance(raw, list):
        return {s.strip() for s in raw if s.strip()}
    return {s.strip() for s in str(raw).split() if s.strip()}


def require_scope(scope: str):  # noqa: ANN201
    """Return a FastAPI dependency that enforces a specific scope on JWT tokens.

    API-key holders are granted all scopes so that existing integrations keep
    working without migration.
    """

    async def _dependency(
        claims: dict[str, Any] = Depends(authenticate_request),
    ) -> dict[str, Any]:
        # API-key callers have no JWT claims – pass through.
        if not claims:
            return claims
        token_scopes = _get_token_scopes(claims)
        if scope not in token_scopes:
            raise HTTPException(
                status_code=status.HTTP_403_FORBIDDEN,
                detail=f"insufficient scope: '{scope}' required",
            )
        return claims

    return _dependency
