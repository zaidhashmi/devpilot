from __future__ import annotations

from typing import Literal

from pydantic import Field, SecretStr, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

from devpilot_agent_runtime.policy import (
    DEFAULT_ANALYSIS_EVIDENCE_MAX_BYTES,
    MAX_ANALYSIS_EVIDENCE_MAX_BYTES,
    MIN_ANALYSIS_EVIDENCE_MAX_BYTES,
)


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_prefix="DEVPILOT_AGENT_RUNTIME_",
        case_sensitive=False,
        extra="ignore",
        populate_by_name=True,
    )

    environment: Literal["development", "test", "staging", "production"] = "development"
    host: str = "127.0.0.1"
    port: int = Field(default=8091, ge=1, le=65535)
    log_level: Literal["DEBUG", "INFO", "WARNING", "ERROR"] = "INFO"
    platform_api_url: str | None = None
    agent_service_token: str | None = Field(
        default=None,
        validation_alias="DEVPILOT_AGENT_SERVICE_TOKEN",
        min_length=32,
    )
    analysis_enabled: bool = Field(
        default=False, validation_alias="DEVPILOT_ANALYSIS_ENABLED"
    )
    model_provider: Literal["auto", "openai", "ollama"] = Field(
        default="auto", validation_alias="DEVPILOT_MODEL_PROVIDER"
    )
    openai_api_key: SecretStr | None = Field(default=None, validation_alias="OPENAI_API_KEY")
    openai_analysis_model: str = Field(
        default="gpt-5.6-sol", validation_alias="DEVPILOT_OPENAI_ANALYSIS_MODEL"
    )
    openai_reasoning_effort: Literal["low", "medium", "high"] = Field(
        default="medium", validation_alias="DEVPILOT_OPENAI_REASONING_EFFORT"
    )
    ollama_base_url: str = Field(
        default="http://localhost:11434/v1", validation_alias="DEVPILOT_OLLAMA_BASE_URL"
    )
    ollama_analysis_model: str = Field(
        default="qwen3:8b", validation_alias="DEVPILOT_OLLAMA_ANALYSIS_MODEL"
    )
    openai_model_timeout_seconds: int = Field(default=60, ge=1, le=600)
    ollama_model_timeout_seconds: int = Field(default=120, ge=1, le=600)
    analysis_evidence_max_bytes: int = Field(
        default=DEFAULT_ANALYSIS_EVIDENCE_MAX_BYTES,
        validation_alias="DEVPILOT_ANALYSIS_EVIDENCE_MAX_BYTES",
        ge=MIN_ANALYSIS_EVIDENCE_MAX_BYTES,
        le=MAX_ANALYSIS_EVIDENCE_MAX_BYTES,
    )

    @property
    def service_token(self) -> str | None:
        return self.agent_service_token

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
        if self.analysis_enabled:
            missing = [
                name
                for name, value in {
                    "DEVPILOT_AGENT_RUNTIME_PLATFORM_API_URL": self.platform_api_url,
                    "DEVPILOT_AGENT_SERVICE_TOKEN": self.service_token,
                }.items()
                if not value
            ]
            if missing:
                raise ValueError(
                    "analysis execution requires: " + ", ".join(missing)
                )
        if self.analysis_enabled and self.model_provider == "openai" and not self.openai_api_key:
            raise ValueError("OpenAI provider requires OPENAI_API_KEY")
        if not self.openai_analysis_model.strip():
            raise ValueError("DEVPILOT_OPENAI_ANALYSIS_MODEL must not be empty")
        if not self.ollama_analysis_model.strip():
            raise ValueError("DEVPILOT_OLLAMA_ANALYSIS_MODEL must not be empty")
        if not self.ollama_base_url.startswith(("http://", "https://")):
            raise ValueError("DEVPILOT_OLLAMA_BASE_URL must be an HTTP(S) URL")
        return self
