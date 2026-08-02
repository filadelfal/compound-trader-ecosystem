from fastapi.testclient import TestClient

from app.main import app


def test_ping_endpoint() -> None:
    with TestClient(app) as client:
        response = client.get("/api/v1/ping")
        assert response.status_code == 200
