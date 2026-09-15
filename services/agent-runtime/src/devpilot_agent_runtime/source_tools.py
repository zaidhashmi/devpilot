from __future__ import annotations

import asyncio
import json
import urllib.error
import urllib.request
from dataclasses import dataclass

from agents import RunContextWrapper, function_tool


class SourceBrokerError(RuntimeError):
    def __init__(self, code: str) -> None:
        super().__init__(code)
        self.code = code


@dataclass
class AnalysisContext:
    broker_url: str
    capability: str
    service_token: str
    tool_timeout: float
    tool_calls: int = 0
    source_bytes: int = 0

    async def request(
        self, method: str, path: str, body: dict[str, object] | None = None
    ) -> dict[str, object]:
        encoded = None if body is None else json.dumps(body).encode()

        def perform() -> dict[str, object]:
            request = urllib.request.Request(
                self.broker_url.rstrip("/") + path,
                data=encoded,
                method=method,
                headers={
                    "Authorization": f"Bearer {self.capability}",
                    "X-DevPilot-Service-Token": self.service_token,
                    "Content-Type": "application/json",
                },
            )
            try:
                with urllib.request.urlopen(request, timeout=self.tool_timeout) as response:
                    raw = response.read(1 << 20)
            except TimeoutError as exc:
                raise SourceBrokerError("source_broker_timeout") from exc
            except urllib.error.URLError as exc:
                raise SourceBrokerError("source_broker_unavailable") from exc
            value = json.loads(raw)
            if not isinstance(value, dict):
                raise SourceBrokerError("source_broker_invalid_response")
            return value

        try:
            return await asyncio.wait_for(asyncio.to_thread(perform), timeout=self.tool_timeout + 1)
        except TimeoutError as exc:
            raise SourceBrokerError("source_broker_timeout") from exc


async def bounded_file_index(context: AnalysisContext) -> dict[str, object]:
    value = await context.request("GET", "/internal/v1/source/files")
    raw_files = value.get("files")
    if not isinstance(raw_files, list):
        raise SourceBrokerError("source_broker_invalid_response")
    safe: list[dict[str, object]] = []
    serialized_bytes = 0
    for raw in sorted(
        raw_files, key=lambda item: str(item.get("path", "")) if isinstance(item, dict) else ""
    ):
        if not isinstance(raw, dict) or not isinstance(raw.get("path"), str):
            continue
        item = {"path": raw["path"][:1024], "size": raw.get("size", 0)}
        item_bytes = len(json.dumps(item, separators=(",", ":")).encode())
        if len(safe) >= 300 or serialized_bytes + item_bytes > 24 << 10:
            break
        safe.append(item)
        serialized_bytes += item_bytes
    context.tool_calls += 1
    return {
        "files": safe,
        "actual_file_count": value.get("actual_file_count", len(raw_files)),
        "entries_presented": len(safe),
        "truncated": len(safe) < len(raw_files),
        "serialized_bytes": serialized_bytes,
    }


@function_tool(timeout_behavior="raise_exception")
async def list_files(ctx: RunContextWrapper[AnalysisContext]) -> str:
    """List bounded source file metadata at the immutable repository commit."""
    ctx.context.tool_calls += 1
    value = await ctx.context.request("GET", "/internal/v1/source/files")
    return "UNTRUSTED_REPOSITORY_FILE_INDEX\n" + json.dumps(value, separators=(",", ":"))


@function_tool(timeout_behavior="raise_exception")
async def read_file(
    ctx: RunContextWrapper[AnalysisContext], path: str, start_line: int, end_line: int
) -> str:
    """Read a bounded line range from one indexed source file at the immutable commit."""
    ctx.context.tool_calls += 1
    value = await ctx.context.request(
        "POST",
        "/internal/v1/source/read",
        {"path": path, "start_line": start_line, "end_line": end_line},
    )
    content = value.get("content")
    if isinstance(content, str):
        ctx.context.source_bytes += len(content.encode())
    return (
        "UNTRUSTED_REPOSITORY_DATA_BEGIN\n"
        + json.dumps(value, separators=(",", ":"))
        + "\nUNTRUSTED_REPOSITORY_DATA_END"
    )
