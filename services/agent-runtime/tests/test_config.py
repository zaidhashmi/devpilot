import pytest
from pydantic import ValidationError

from devpilot_agent_runtime.config import Settings


def test_development_defaults() -> None:
    settings = Settings()
    assert settings.host == "127.0.0.1"
    assert settings.port == 8090


def test_production_requires_platform_credentials() -> None:
    with pytest.raises(ValidationError):
        Settings(environment="production")


def test_production_accepts_required_values() -> None:
    settings = Settings(
        environment="production",
        platform_api_url="https://api.internal.example",
        service_token="test-only-value",
    )
    assert settings.environment == "production"
