from pydantic_settings import BaseSettings, SettingsConfigDict

class Settings(BaseSettings):
    service_name: str = "ai-mentor"
    port: int = 3006
    log_level: str = "INFO"
    database_url: str = "postgresql://compound:compound_dev_password@postgres:5432/compound"
    redis_url: str = "redis://redis:6379/0"

    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

settings = Settings()
