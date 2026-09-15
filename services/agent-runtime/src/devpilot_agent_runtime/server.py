from __future__ import annotations

import asyncio
import hmac
import json
import logging
from http import HTTPStatus

from pydantic import ValidationError

from devpilot_agent_runtime.analysis import EvidenceProtocolError, run_analysis
from devpilot_agent_runtime.config import Settings
from devpilot_agent_runtime.diagnostics import ClassifiedAgentError
from devpilot_agent_runtime.providers import (
    ModelUnavailable,
    ProviderSelection,
    ProviderUnavailable,
    resolve_provider,
    verify_provider,
)
from devpilot_agent_runtime.schemas import AnalysisRequest
from devpilot_agent_runtime.source_tools import SourceBrokerError

MAX_BODY = 256 << 10


class RuntimeServer:
    def __init__(self, settings: Settings) -> None:
        self.settings = settings
        self.host, self.port, self.service_token = (
            settings.host,
            settings.port,
            settings.service_token,
        )
        self.server: asyncio.Server | None = None
        self.active: dict[str, asyncio.Task[object]] = {}

    async def start(self) -> None:
        self.server = await asyncio.start_server(self._handle, self.host, self.port)

    async def close(self) -> None:
        for task in self.active.values():
            task.cancel()
        if self.server is not None:
            self.server.close()
            await self.server.wait_closed()

    async def _handle(self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
        try:
            head = await asyncio.wait_for(reader.readuntil(b"\r\n\r\n"), timeout=3)
            lines = head.decode("ascii", "replace").split("\r\n")
            method, path, _ = lines[0].split(" ", 2)
            headers = {}
            for line in lines[1:]:
                if ":" in line:
                    key, value = line.split(":", 1)
                    headers[key.lower()] = value.strip()
            length = int(headers.get("content-length", "0"))
            if length > MAX_BODY:
                await self._reply(
                    writer, HTTPStatus.REQUEST_ENTITY_TOO_LARGE, {"error": "request_too_large"}
                )
                return
            body = await reader.readexactly(length) if length else b""
            if path == "/healthz":
                await self._reply(writer, HTTPStatus.OK, {"status": "ok"})
                return
            if path == "/readyz":
                provider = await self._provider_status()
                ready = (
                    self.settings.analysis_enabled
                    and self.service_token is not None
                    and provider is not None
                )
                status = HTTPStatus.OK if ready else HTTPStatus.SERVICE_UNAVAILABLE
                await self._reply(
                    writer,
                    status,
                    {
                        "status": f"{provider.provider}_ready"
                        if ready and provider
                        else "provider_unavailable"
                    },
                )
                return
            if not self._authorized(headers.get("authorization", "")):
                await self._reply(writer, HTTPStatus.UNAUTHORIZED, {"error": "unauthorized"})
                return
            if method == "GET" and path == "/internal/v1/provider":
                provider = await self._provider_status()
                if provider is None:
                    await self._reply(
                        writer, HTTPStatus.SERVICE_UNAVAILABLE, {"error": "provider_unavailable"}
                    )
                else:
                    await self._reply(
                        writer,
                        HTTPStatus.OK,
                        {
                            "provider": provider.provider,
                            "model": provider.model_id,
                            "reasoning_effort": provider.reasoning_effort,
                        },
                    )
                return
            if method == "POST" and path == "/internal/v1/agent-runs":
                await self._analyze(writer, body)
                return
            prefix = "/internal/v1/agent-runs/"
            if method == "POST" and path.startswith(prefix) and path.endswith("/cancel"):
                run_id = path[len(prefix) : -len("/cancel")]
                task = self.active.get(run_id)
                if task is not None:
                    task.cancel()
                await self._reply(writer, HTTPStatus.NO_CONTENT, None)
                return
            await self._reply(writer, HTTPStatus.NOT_FOUND, {"error": "not_found"})
        except (ValueError, asyncio.IncompleteReadError, asyncio.LimitOverrunError):
            await self._reply(writer, HTTPStatus.BAD_REQUEST, {"error": "invalid_request"})
        finally:
            writer.close()
            await writer.wait_closed()

    def _authorized(self, header: str) -> bool:
        expected = "Bearer " + (self.service_token or "")
        return bool(self.service_token) and hmac.compare_digest(header, expected)

    async def _analyze(self, writer: asyncio.StreamWriter, body: bytes) -> None:
        try:
            request = AnalysisRequest.model_validate_json(body)
        except ValidationError:
            await self._reply(
                writer, HTTPStatus.UNPROCESSABLE_ENTITY, {"error": "invalid_contract"}
            )
            return
        task = asyncio.create_task(run_analysis(request, self.service_token or "", self.settings))
        self.active[request.agent_run_id] = task
        try:
            result = await task
            await self._reply(writer, HTTPStatus.OK, result.model_dump(mode="json"))
        except asyncio.CancelledError:
            await self._reply(writer, HTTPStatus.CONFLICT, {"error": "cancelled"})
        except ClassifiedAgentError as exc:
            status = (
                HTTPStatus.GATEWAY_TIMEOUT
                if exc.code in {"model_timeout", "tool_timeout"}
                else HTTPStatus.UNPROCESSABLE_ENTITY
            )
            await self._reply(
                writer,
                status,
                {"error": exc.code, "diagnostics": exc.diagnostics.safe_dict()},
            )
        except EvidenceProtocolError as exc:
            await self._reply(writer, HTTPStatus.UNPROCESSABLE_ENTITY, {"error": exc.code})
        except SourceBrokerError as exc:
            status = (
                HTTPStatus.GATEWAY_TIMEOUT
                if exc.code == "source_broker_timeout"
                else HTTPStatus.BAD_GATEWAY
            )
            await self._reply(writer, status, {"error": exc.code})
        except TimeoutError:
            await self._reply(writer, HTTPStatus.GATEWAY_TIMEOUT, {"error": "agent_timeout"})
        except ModelUnavailable:
            await self._reply(
                writer, HTTPStatus.UNPROCESSABLE_ENTITY, {"error": "ollama_model_unavailable"}
            )
        except ProviderUnavailable:
            await self._reply(
                writer, HTTPStatus.SERVICE_UNAVAILABLE, {"error": "ollama_unavailable"}
            )
        except Exception as exc:
            logging.getLogger(__name__).error(
                "agent run failed",
                extra={
                    "agent_run_id": request.agent_run_id,
                    "exception_class": type(exc).__name__,
                },
            )
            await self._reply(writer, HTTPStatus.BAD_GATEWAY, {"error": "agent_failed"})
        finally:
            self.active.pop(request.agent_run_id, None)

    async def _provider_status(self) -> ProviderSelection | None:
        try:
            selection = resolve_provider(self.settings)
            await verify_provider(self.settings, selection)
            return selection
        except Exception:
            return None

    @staticmethod
    async def _reply(
        writer: asyncio.StreamWriter, status: HTTPStatus, value: object | None
    ) -> None:
        body = b"" if value is None else json.dumps(value, separators=(",", ":")).encode()
        headers = (
            f"HTTP/1.1 {status.value} {status.phrase}\r\nContent-Type: application/json\r\n"
            f"Content-Length: {len(body)}\r\nConnection: close\r\n\r\n"
        ).encode()
        writer.write(headers + body)
        await writer.drain()
