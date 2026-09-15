import pytest
from pydantic import ValidationError

from devpilot_agent_runtime.config import Settings


def test_development_defaults() -> None:
    settings = Settings()
    assert settings.host == "127.0.0.1"
    assert settings.port == 8091


def test_production_requires_platform_credentials() -> None:
    with pytest.raises(ValidationError):
        Settings(environment="production")


def test_production_accepts_required_values() -> None:
    settings = Settings(
        environment="production",
        platform_api_url="https://api.internal.example",
        agent_service_token="test-agent-service-token-32-characters",
    )
    assert settings.environment == "production"


def test_explicit_openai_requires_key() -> None:
    with pytest.raises(ValidationError):
        Settings(analysis_enabled=True, model_provider="openai")


def test_ollama_does_not_require_openai_key() -> None:
    settings = Settings(
        analysis_enabled=True,
        model_provider="ollama",
        platform_api_url="http://127.0.0.1:8080",
        agent_service_token="test-agent-service-token-32-characters",
    )
    assert settings.model_provider == "ollama"


def test_analysis_requires_canonical_service_token() -> None:
    with pytest.raises(ValidationError, match="DEVPILOT_AGENT_SERVICE_TOKEN"):
        Settings(
            analysis_enabled=True,
            model_provider="ollama",
            platform_api_url="http://127.0.0.1:8080",
        )


def test_canonical_service_token_environment_variable(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("DEVPILOT_ANALYSIS_ENABLED", "true")
    monkeypatch.setenv("DEVPILOT_AGENT_RUNTIME_PLATFORM_API_URL", "http://127.0.0.1:8080")
    monkeypatch.setenv("DEVPILOT_AGENT_SERVICE_TOKEN", "test-agent-service-token-32-characters")
    settings = Settings()
    assert settings.analysis_enabled is True
    assert settings.service_token == "test-agent-service-token-32-characters"


def test_legacy_service_token_variable_is_not_accepted(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setenv("DEVPILOT_ANALYSIS_ENABLED", "true")
    monkeypatch.setenv("DEVPILOT_AGENT_RUNTIME_PLATFORM_API_URL", "http://127.0.0.1:8080")
    monkeypatch.delenv("DEVPILOT_AGENT_SERVICE_TOKEN", raising=False)
    monkeypatch.setenv(
        "DEVPILOT_AGENT_RUNTIME_SERVICE_TOKEN",
        "legacy-agent-runtime-token-32-characters",
    )
    with pytest.raises(ValidationError, match="DEVPILOT_AGENT_SERVICE_TOKEN") as exc_info:
        Settings()
    assert "legacy-agent-runtime-token-32-characters" not in str(exc_info.value)
