from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from agents.exceptions import (
    MaxTurnsExceeded,
    ModelBehaviorError,
    ModelTimeoutError,
    OutputGuardrailTripwireTriggered,
    ToolTimeoutError,
)
from pydantic import ValidationError


@dataclass
class ProviderDiagnostics:
    http_status: int | None = None
    finish_reason: str | None = None
    assistant_content_present: bool = False
    assistant_content_characters: int = 0
    reasoning_present: bool = False
    reasoning_characters: int = 0
    tool_calls_present: bool = False
    tool_call_count: int = 0
    input_tokens: int | None = None
    output_tokens: int | None = None
    provider_duration_ms: int | None = None
    response_model: str | None = None
    structured_output_parsing_began: bool = False
    schema_validation_succeeded: bool = False
    output_guardrail_ran: bool = False
    exception_class: str | None = None

    def safe_dict(self) -> dict[str, object]:
        values: dict[str, object] = {
            "assistant_content_present": self.assistant_content_present,
            "assistant_content_characters": min(self.assistant_content_characters, 1_000_000),
            "reasoning_present": self.reasoning_present,
            "reasoning_characters": min(self.reasoning_characters, 1_000_000),
            "tool_calls_present": self.tool_calls_present,
            "tool_call_count": min(self.tool_call_count, 100),
            "structured_output_parsing_began": self.structured_output_parsing_began,
            "schema_validation_succeeded": self.schema_validation_succeeded,
            "output_guardrail_ran": self.output_guardrail_ran,
        }
        for key in (
            "http_status",
            "finish_reason",
            "input_tokens",
            "output_tokens",
            "provider_duration_ms",
            "response_model",
            "exception_class",
        ):
            value = getattr(self, key)
            if value is not None:
                values[key] = value if not isinstance(value, str) else value[:120]
        return values


class ClassifiedAgentError(RuntimeError):
    def __init__(self, code: str, diagnostics: ProviderDiagnostics) -> None:
        super().__init__(code)
        self.code = code
        self.diagnostics = diagnostics


def classify_agent_exception(
    exc: BaseException, diagnostics: ProviderDiagnostics
) -> ClassifiedAgentError:
    diagnostics.exception_class = type(exc).__name__
    if isinstance(exc, ModelTimeoutError):
        code = "model_timeout"
    elif isinstance(exc, MaxTurnsExceeded):
        code = "model_max_turns"
    elif isinstance(exc, ToolTimeoutError):
        code = "tool_timeout"
    elif isinstance(exc, OutputGuardrailTripwireTriggered):
        code = "output_guardrail_rejected"
    elif isinstance(exc, ValidationError):
        code = "model_invalid_output"
    elif isinstance(exc, ModelBehaviorError):
        code = (
            "model_context_exhausted"
            if diagnostics.finish_reason == "length"
            else "model_invalid_output"
        )
    else:
        code = "provider_response_invalid"
    return ClassifiedAgentError(code, diagnostics)


def bounded_model_identifier(value: Any) -> str | None:
    return value[:120] if isinstance(value, str) and value else None


def safe_exception_origin(exc: BaseException) -> dict[str, object]:
    traceback = exc.__traceback__
    if traceback is None:
        return {"exception_class": type(exc).__name__}
    while traceback.tb_next is not None:
        traceback = traceback.tb_next
    code = traceback.tb_frame.f_code
    filename = code.co_filename.replace("\\", "/")
    for marker in ("/site-packages/", "/devpilot_agent_runtime/"):
        if marker in filename:
            filename = marker.strip("/") + "/" + filename.split(marker, 1)[1]
            break
    else:
        filename = filename.rsplit("/", 1)[-1]
    return {
        "exception_class": type(exc).__name__,
        "source_file": filename[:240],
        "function": code.co_name[:120],
        "line": traceback.tb_lineno,
    }
