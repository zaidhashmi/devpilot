import asyncio

import pytest

from devpilot_agent_runtime.health import HealthServer


@pytest.mark.asyncio
async def test_health_endpoint() -> None:
    server = HealthServer("127.0.0.1", 0)
    await server.start()
    assert server._server is not None
    socket = server._server.sockets[0]
    port = int(socket.getsockname()[1])

    reader, writer = await asyncio.open_connection("127.0.0.1", port)
    writer.write(b"GET /healthz HTTP/1.1\r\nHost: localhost\r\n\r\n")
    await writer.drain()
    response = await reader.read()
    writer.close()
    await writer.wait_closed()
    await server.close()

    assert b"200 OK" in response
    assert b'{"status": "ok"}' in response
