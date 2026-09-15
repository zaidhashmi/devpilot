from __future__ import annotations

import time
from typing import Any

from agents import OpenAIChatCompletionsModel

from devpilot_agent_runtime.diagnostics import ProviderDiagnostics, bounded_model_identifier


def observe_chat_completion(response: Any, diagnostics: ProviderDiagnostics) -> None:
    diagnostics.response_model = bounded_model_identifier(getattr(response, "model", None))
    usage = getattr(response, "usage", None)
    if usage is not None:
        diagnostics.input_tokens = getattr(usage, "prompt_tokens", None)
        diagnostics.output_tokens = getattr(usage, "completion_tokens", None)
    for choice in getattr(response, "choices", ()):
        finish_reason = getattr(choice, "finish_reason", None)
        if finish_reason:
            diagnostics.finish_reason = str(finish_reason)[:40]
        message = getattr(choice, "message", None)
        if message is None:
            continue
        content = getattr(message, "content", None)
        if isinstance(content, str) and content:
            diagnostics.assistant_content_present = True
            diagnostics.assistant_content_characters += len(content)
        reasoning = getattr(message, "reasoning_content", None)
        if not isinstance(reasoning, str):
            reasoning = getattr(message, "reasoning", None)
        if isinstance(reasoning, str) and reasoning:
            diagnostics.reasoning_present = True
            diagnostics.reasoning_characters += len(reasoning)
        tool_calls = getattr(message, "tool_calls", None) or ()
        if tool_calls:
            diagnostics.tool_calls_present = True
            diagnostics.tool_call_count += len(tool_calls)


class _ObservedStream:
    def __init__(self, stream: Any, diagnostics: ProviderDiagnostics, started: float) -> None:
        self._stream = stream
        self._diagnostics = diagnostics
        self._started = started

    def __aiter__(self) -> _ObservedStream:
        return self

    async def __anext__(self) -> Any:
        try:
            chunk = await self._stream.__anext__()
        except StopAsyncIteration:
            self._diagnostics.provider_duration_ms = int((time.monotonic() - self._started) * 1000)
            raise
        self._diagnostics.response_model = bounded_model_identifier(getattr(chunk, "model", None))
        usage = getattr(chunk, "usage", None)
        if usage is not None:
            self._diagnostics.input_tokens = getattr(usage, "prompt_tokens", None)
            self._diagnostics.output_tokens = getattr(usage, "completion_tokens", None)
        for choice in getattr(chunk, "choices", ()):
            finish_reason = getattr(choice, "finish_reason", None)
            if finish_reason:
                self._diagnostics.finish_reason = str(finish_reason)[:40]
            delta = getattr(choice, "delta", None)
            if delta is None:
                continue
            if getattr(delta, "content", None):
                self._diagnostics.assistant_content_present = True
                self._diagnostics.assistant_content_characters += len(delta.content)
            reasoning = getattr(delta, "reasoning_content", None)
            if not isinstance(reasoning, str):
                reasoning = getattr(delta, "reasoning", None)
            if isinstance(reasoning, str) and reasoning:
                self._diagnostics.reasoning_present = True
                self._diagnostics.reasoning_characters += len(reasoning)
            tool_calls = getattr(delta, "tool_calls", None) or ()
            if tool_calls:
                self._diagnostics.tool_calls_present = True
                self._diagnostics.tool_call_count += len(tool_calls)
        return chunk

    def __getattr__(self, name: str) -> Any:
        return getattr(self._stream, name)


class DiagnosticChatCompletionsModel(OpenAIChatCompletionsModel):
    def __init__(self, *args: Any, diagnostics: ProviderDiagnostics, **kwargs: Any) -> None:
        super().__init__(*args, **kwargs)
        self._diagnostics = diagnostics

    async def _fetch_response(self, *args: Any, **kwargs: Any) -> Any:
        started = time.monotonic()
        fetched = await super()._fetch_response(*args, **kwargs)
        if isinstance(fetched, tuple):
            response, stream = fetched
            self._diagnostics.http_status = getattr(
                getattr(stream, "response", None), "status_code", None
            )
            return response, _ObservedStream(stream, self._diagnostics, started)
        self._diagnostics.http_status = 200
        self._diagnostics.provider_duration_ms = int((time.monotonic() - started) * 1000)
        observe_chat_completion(fetched, self._diagnostics)
        return fetched
