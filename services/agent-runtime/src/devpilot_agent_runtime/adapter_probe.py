from __future__ import annotations

import asyncio
import json
import time
import urllib.request
from typing import Any, cast

from agents import Agent, ModelSettings, RunConfig, Runner
from agents.models.interface import ModelTracing
from openai import AsyncOpenAI

from devpilot_agent_runtime.config import Settings
from devpilot_agent_runtime.diagnostics import ProviderDiagnostics, safe_exception_origin
from devpilot_agent_runtime.providers import model_for, resolve_provider, verify_provider

PROMPT = "Respond with the word READY."


def _text_length(value: object) -> int:
    return len(value) if isinstance(value, str) else 0


def _native_request(settings: Settings) -> dict[str, object]:
    native_url = settings.ollama_base_url.removesuffix("/v1") + "/api/chat"
    body = json.dumps(
        {
            "model": settings.ollama_analysis_model,
            "messages": [{"role": "user", "content": PROMPT}],
            "stream": False,
            "think": False,
            "keep_alive": "10m",
        }
    ).encode()
    started = time.monotonic()
    request = urllib.request.Request(
        native_url, data=body, headers={"Content-Type": "application/json"}
    )
    with urllib.request.urlopen(request, timeout=120) as response:  # noqa: S310 local URL
        value = json.loads(response.read(1 << 20))
        status = response.status
    message = value.get("message", {})
    content_length = _text_length(message.get("content"))
    thinking_length = _text_length(message.get("thinking"))
    return {
        "layer": "A",
        "status": "passed" if status == 200 and content_length else "failed",
        "http_status": status,
        "content_present": content_length > 0,
        "content_characters": content_length,
        "reasoning_present": thinking_length > 0,
        "reasoning_characters": thinking_length,
        "duration_ms": int((time.monotonic() - started) * 1000),
        "prompt_tokens": value.get("prompt_eval_count"),
        "output_tokens": value.get("eval_count"),
        "load_duration_ns": value.get("load_duration"),
        "prompt_eval_duration_ns": value.get("prompt_eval_duration"),
        "eval_duration_ns": value.get("eval_duration"),
    }


async def _raw_chat(settings: Settings, reasoning_effort: str, name: str) -> dict[str, object]:
    client = AsyncOpenAI(base_url=settings.ollama_base_url, api_key="ollama-local")
    started = time.monotonic()
    try:
        response = await client.chat.completions.create(
            model=settings.ollama_analysis_model,
            messages=[{"role": "user", "content": PROMPT}],
            stream=False,
            reasoning_effort=cast(Any, reasoning_effort),
        )
        diagnostics = ProviderDiagnostics(http_status=200)
        from devpilot_agent_runtime.diagnostic_model import observe_chat_completion

        observe_chat_completion(response, diagnostics)
        return {
            "layer": name,
            "status": "passed" if diagnostics.assistant_content_present else "failed",
            "reasoning_effort": reasoning_effort,
            "duration_ms": int((time.monotonic() - started) * 1000),
            **diagnostics.safe_dict(),
        }
    except Exception as exc:
        return {
            "layer": name,
            "status": "failed",
            "reasoning_effort": reasoning_effort,
            "duration_ms": int((time.monotonic() - started) * 1000),
            "origin": safe_exception_origin(exc),
        }


async def _adapter(settings: Settings) -> dict[str, object]:
    selection = resolve_provider(settings)
    diagnostics = ProviderDiagnostics()
    model = model_for(settings, selection, diagnostics)
    if isinstance(model, str):
        raise RuntimeError("ollama adapter was not selected")
    started = time.monotonic()
    try:
        response = await model.get_response(
            system_instructions="Return a minimal harmless response.",
            input=PROMPT,
            model_settings=ModelSettings(
                reasoning={"effort": "low"}, include_usage=True, timeout=120
            ),
            tools=[],
            output_schema=None,
            handoffs=[],
            tracing=ModelTracing.DISABLED,
        )
        message_items = [item for item in response.output if item.type == "message"]
        reasoning_items = [item for item in response.output if item.type == "reasoning"]
        text_length = sum(
            _text_length(getattr(part, "text", None))
            for item in message_items
            for part in item.content
        )
        return {
            "layer": "C",
            "status": "passed" if text_length else "failed",
            "duration_ms": int((time.monotonic() - started) * 1000),
            "response_created": True,
            "output_items": len(response.output),
            "assistant_message_present": bool(message_items),
            "reasoning_item_present": bool(reasoning_items),
            "assistant_content_present": text_length > 0,
            "assistant_content_characters": text_length,
            "usage": {
                "input_tokens": getattr(response.usage, "input_tokens", None),
                "output_tokens": getattr(response.usage, "output_tokens", None),
            },
            "diagnostics": diagnostics.safe_dict(),
        }
    except Exception as exc:
        return {
            "layer": "C",
            "status": "failed",
            "duration_ms": int((time.monotonic() - started) * 1000),
            "response_created": False,
            "origin": safe_exception_origin(exc),
            "diagnostics": diagnostics.safe_dict(),
        }


async def _runner(settings: Settings) -> dict[str, object]:
    selection = resolve_provider(settings)
    diagnostics = ProviderDiagnostics()
    model = model_for(settings, selection, diagnostics)
    started = time.monotonic()
    try:
        result = await Runner.run(
            Agent(
                name="Ollama adapter isolation",
                instructions="Return a minimal harmless response.",
                model=model,
                model_settings=ModelSettings(
                    reasoning={"effort": "low"}, include_usage=True, timeout=120
                ),
            ),
            PROMPT,
            max_turns=2,
            run_config=RunConfig(
                model=model,
                tracing_disabled=True,
                trace_include_sensitive_data=False,
            ),
        )
        output_length = _text_length(result.final_output)
        return {
            "layer": "D",
            "status": "passed" if output_length else "failed",
            "duration_ms": int((time.monotonic() - started) * 1000),
            "final_output_present": output_length > 0,
            "final_output_characters": output_length,
            "turns": len(result.raw_responses),
            "usage": {
                "input_tokens": result.context_wrapper.usage.input_tokens,
                "output_tokens": result.context_wrapper.usage.output_tokens,
            },
            "diagnostics": diagnostics.safe_dict(),
        }
    except Exception as exc:
        return {
            "layer": "D",
            "status": "failed",
            "duration_ms": int((time.monotonic() - started) * 1000),
            "origin": safe_exception_origin(exc),
            "diagnostics": diagnostics.safe_dict(),
        }


async def run_layers(settings: Settings) -> list[dict[str, object]]:
    selection = resolve_provider(settings)
    if selection.provider != "ollama":
        raise RuntimeError("adapter isolation requires ollama")
    await verify_provider(settings, selection)
    results = [await asyncio.to_thread(_native_request, settings)]
    if results[-1]["status"] != "passed":
        return results
    b1 = await _raw_chat(settings, "low", "B1")
    b2 = await _raw_chat(settings, "none", "B2")
    results.extend((b1, b2))
    if not any(item.get("assistant_content_present") is True for item in (b1, b2)):
        return results
    results.append(await _adapter(settings))
    if results[-1]["status"] != "passed":
        return results
    results.append(await _runner(settings))
    return results


def main() -> None:
    results = asyncio.run(run_layers(Settings()))
    print(json.dumps({"layers": results}, separators=(",", ":")))
    if results[-1]["status"] != "passed":
        raise SystemExit(1)


if __name__ == "__main__":
    main()
