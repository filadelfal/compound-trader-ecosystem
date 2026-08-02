from fastapi import Depends, FastAPI
from fastapi.testclient import TestClient

from app.security import authenticate_request


def test_auth_required(monkeypatch) -> None:
    async def _noop(*_args, **_kwargs) -> None:
        return None

    monkeypatch.setattr("app.security.enforce_rate_limit", _noop)
    app = FastAPI()

    @app.get("/private")
    async def private(_: dict = Depends(authenticate_request)) -> dict[str, bool]:
        return {"ok": True}

    with TestClient(app) as client:
        response = client.get("/private")
        assert response.status_code == 401


def test_auth_with_api_key(monkeypatch) -> None:
    async def _noop(*_args, **_kwargs) -> None:
        return None

    monkeypatch.setattr("app.security.enforce_rate_limit", _noop)
    app = FastAPI()

    @app.get("/private")
    async def private(_: dict = Depends(authenticate_request)) -> dict[str, bool]:
        return {"ok": True}

    with TestClient(app) as client:
        response = client.get("/private", headers={"X-API-Key": "test-key"})
        assert response.status_code == 200
