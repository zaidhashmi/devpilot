from __future__ import annotations

from typing import Literal

from pydantic import Field, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_prefix="DEVPILOT_AGENT_RUNTIME_",
        case_sensitive=False,
        extra="ignore",
    )

    environment: Literal["development", "test", "staging", "production"] = "development"
    host: str = "127.0.0.1"
    port: int = Field(default=8090, ge=1, le=65535)
    log_level: Literal["DEBUG", "INFO", "WARNING", "ERROR"] = "INFO"
    platform_api_url: str | None = None
    service_token: str | None = None

    @model_validator(mode="after")
    def require_production_values(self) -> Settings:
        if self.environment in {"staging", "production"}:
            missing = [
                name
                for name, value in {
                    "platform_api_url": self.platform_api_url,
                    "service_token": self.service_token,
                }.items()
                if not value
            ]
            if missing:
                raise ValueError(f"missing required runtime settings: {', '.join(missing)}")
        return self
