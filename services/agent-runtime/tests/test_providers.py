from pydantic import SecretStr

from devpilot_agent_runtime.config import Settings
from devpilot_agent_runtime.providers import resolve_provider


def test_auto_with_openai_key_selects_openai() -> None:
    selected = resolve_provider(Settings(openai_api_key=SecretStr("test-key")))
    assert (selected.provider, selected.model_id) == ("openai", "gpt-5.6-sol")


def test_auto_without_openai_key_selects_ollama() -> None:
    selected = resolve_provider(Settings(openai_api_key=None))
    assert (selected.provider, selected.model_id, selected.reasoning_effort) == (
        "ollama",
        "qwen3:8b",
        "low",
    )


def test_explicit_openai_with_key_selects_openai() -> None:
    assert (
        resolve_provider(
            Settings(model_provider="openai", openai_api_key=SecretStr("test-key"))
        ).provider
        == "openai"
    )


def test_explicit_ollama_ignores_openai_key() -> None:
    selected = resolve_provider(
        Settings(model_provider="ollama", openai_api_key=SecretStr("unused-key"))
    )
    assert selected.provider == "ollama"


def test_explicit_ollama_without_key_selects_ollama() -> None:
    assert (
        resolve_provider(Settings(model_provider="ollama", openai_api_key=None)).provider
        == "ollama"
    )


def test_ollama_model_enables_strict_compatibility_options() -> None:
    from devpilot_agent_runtime.diagnostic_model import DiagnosticChatCompletionsModel
    from devpilot_agent_runtime.diagnostics import ProviderDiagnostics
    from devpilot_agent_runtime.providers import model_for

    settings = Settings(model_provider="ollama")
    selected = resolve_provider(settings)
    model = model_for(settings, selected, ProviderDiagnostics())
    assert isinstance(model, DiagnosticChatCompletionsModel)
    assert model._strict_feature_validation is True
    assert model._buffer_streamed_tool_calls is True
