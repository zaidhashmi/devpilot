from __future__ import annotations

from dataclasses import dataclass
from typing import Literal

from openai import AsyncOpenAI

from devpilot_agent_runtime.config import Settings
from devpilot_agent_runtime.diagnostic_model import DiagnosticChatCompletionsModel
from devpilot_agent_runtime.diagnostics import ProviderDiagnostics

ProviderName = Literal["openai", "ollama"]


@dataclass(frozen=True)
class ProviderSelection:
    provider: ProviderName
    model_id: str
    reasoning_effort: str | None


class ProviderUnavailable(RuntimeError):
    pass


class ModelUnavailable(RuntimeError):
    pass


def resolve_provider(settings: Settings) -> ProviderSelection:
    provider = settings.model_provider
    if provider == "auto":
        provider = "openai" if settings.openai_api_key else "ollama"
    if provider == "openai":
        if not settings.openai_api_key:
            raise ProviderUnavailable("openai_unavailable")
        return ProviderSelection(
            "openai", settings.openai_analysis_model, settings.openai_reasoning_effort
        )
    return ProviderSelection("ollama", settings.ollama_analysis_model, "low")


async def verify_provider(settings: Settings, selection: ProviderSelection) -> None:
    if selection.provider == "openai":
        return
    client = AsyncOpenAI(base_url=settings.ollama_base_url, api_key="ollama-local")
    try:
        models = await client.models.list()
    except Exception as exc:
        raise ProviderUnavailable("ollama_unavailable") from exc
    if selection.model_id not in {item.id for item in models.data}:
        raise ModelUnavailable("ollama_model_unavailable")


def model_for(
    settings: Settings,
    selection: ProviderSelection,
    diagnostics: ProviderDiagnostics | None = None,
) -> str | DiagnosticChatCompletionsModel:
    if selection.provider == "openai":
        return selection.model_id
    client = AsyncOpenAI(base_url=settings.ollama_base_url, api_key="ollama-local")
    return DiagnosticChatCompletionsModel(
        model=selection.model_id,
        openai_client=client,
        diagnostics=diagnostics or ProviderDiagnostics(),
        strict_feature_validation=True,
        buffer_streamed_tool_calls=True,
    )
