import os
import sys
from pathlib import Path

os.environ.setdefault("TESTING", "true")
os.environ.setdefault("API_KEYS", "test-key")
os.environ.setdefault("JWT_SECRET", "test-secret")
os.environ.setdefault("DATABASE_URL", "sqlite+aiosqlite:///:memory:")
os.environ.setdefault("REDIS_URL", "redis://localhost:6379/15")
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
