from __future__ import annotations

from types import SimpleNamespace

from devpilot_agent_runtime.diagnostic_model import observe_chat_completion
from devpilot_agent_runtime.diagnostics import ProviderDiagnostics, safe_exception_origin


def completion(
    *, content: str | None, reasoning: str | None, finish_reason: str
) -> SimpleNamespace:
    return SimpleNamespace(
        model="qwen3:8b",
        usage=SimpleNamespace(prompt_tokens=12, completion_tokens=34),
        choices=[
            SimpleNamespace(
                finish_reason=finish_reason,
                message=SimpleNamespace(
                    content=content,
                    reasoning_content=reasoning,
                    tool_calls=None,
                ),
            )
        ],
    )


def test_reasoning_only_metadata_contains_counts_not_text() -> None:
    diagnostics = ProviderDiagnostics()
    observe_chat_completion(
        completion(content=None, reasoning="private-reasoning", finish_reason="length"),
        diagnostics,
    )
    safe = diagnostics.safe_dict()
    assert safe["reasoning_present"] is True
    assert safe["reasoning_characters"] == 17
    assert safe["assistant_content_present"] is False
    assert safe["finish_reason"] == "length"
    assert "private-reasoning" not in str(safe)


def test_empty_stop_and_content_present_are_distinguishable() -> None:
    empty = ProviderDiagnostics()
    observe_chat_completion(completion(content="", reasoning=None, finish_reason="stop"), empty)
    assert empty.safe_dict()["assistant_content_present"] is False
    assert empty.safe_dict()["finish_reason"] == "stop"

    present = ProviderDiagnostics()
    observe_chat_completion(
        completion(content="READY", reasoning=None, finish_reason="stop"), present
    )
    assert present.safe_dict()["assistant_content_characters"] == 5
    assert "READY" not in str(present.safe_dict())


def test_value_error_origin_is_package_relative_and_message_free() -> None:
    try:
        raise ValueError("sensitive provider output")
    except ValueError as exc:
        origin = safe_exception_origin(exc)
    assert origin["exception_class"] == "ValueError"
    assert not str(origin["source_file"]).startswith("/")
    assert "sensitive provider output" not in str(origin)
