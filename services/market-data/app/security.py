from __future__ import annotations

import jwt
from fastapi import Depends, Header, HTTPException, Request
from fastapi.security import HTTPAuthorizationCredentials, HTTPBearer

from app.config import settings
from app.middleware.rate_limit import enforce_rate_limit

bearer_scheme = HTTPBearer(auto_error=False)


async def authenticate_request(
    request: Request,
    credentials: HTTPAuthorizationCredentials | None = Depends(bearer_scheme),
    x_api_key: str | None = Header(default=None, alias="X-API-Key"),
) -> dict:
    identity: str | None = None
    claims: dict = {}

    if credentials is not None:
        try:
            claims = jwt.decode(credentials.credentials, settings.jwt_secret, algorithms=[settings.jwt_algorithm])
            identity = str(claims.get("sub") or claims.get("client_id") or "jwt")
        except jwt.PyJWTError as exc:
            raise HTTPException(status_code=401, detail="invalid jwt token") from exc

    if identity is None and x_api_key:
        if x_api_key not in settings.parsed_api_keys:
            raise HTTPException(status_code=401, detail="invalid api key")
        identity = f"apikey:{x_api_key[-6:]}"

    if identity is None:
        raise HTTPException(status_code=401, detail="authentication required")

    await enforce_rate_limit(request, identity)
    return claims
