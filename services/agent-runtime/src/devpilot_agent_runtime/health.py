from __future__ import annotations

import asyncio
import json
from http import HTTPStatus


class HealthServer:
    def __init__(self, host: str, port: int) -> None:
        self._host = host
        self._port = port
        self._server: asyncio.Server | None = None

    async def start(self) -> None:
        self._server = await asyncio.start_server(self._handle, self._host, self._port)

    async def close(self) -> None:
        if self._server is not None:
            self._server.close()
            await self._server.wait_closed()

    async def _handle(
        self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter
    ) -> None:
        try:
            request_line = await asyncio.wait_for(reader.readline(), timeout=2)
            parts = request_line.decode("ascii", errors="replace").strip().split()
            path = parts[1] if len(parts) >= 2 else ""
            if path in {"/healthz", "/readyz"}:
                status = HTTPStatus.OK
                body = json.dumps(
                    {"status": "ok" if path == "/healthz" else "ready"}
                ).encode()
            else:
                status = HTTPStatus.NOT_FOUND
                body = json.dumps({"error": "not_found"}).encode()
            headers = (
                f"HTTP/1.1 {status.value} {status.phrase}\r\n"
                "Content-Type: application/json\r\n"
                f"Content-Length: {len(body)}\r\n"
                "Connection: close\r\n\r\n"
            ).encode()
            writer.write(headers + body)
            await writer.drain()
        finally:
            writer.close()
            await writer.wait_closed()
