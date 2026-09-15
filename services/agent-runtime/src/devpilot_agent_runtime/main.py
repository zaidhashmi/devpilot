from __future__ import annotations

import asyncio
import logging
import signal

from devpilot_agent_runtime.config import Settings
from devpilot_agent_runtime.logging import configure_logging
from devpilot_agent_runtime.server import RuntimeServer


async def run() -> None:
    settings = Settings()
    configure_logging(settings.log_level)
    logger = logging.getLogger(__name__)
    stop = asyncio.Event()
    loop = asyncio.get_running_loop()

    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, stop.set)

    health_server = RuntimeServer(settings)
    await health_server.start()
    logger.info("agent runtime health server started")

    try:
        await stop.wait()
    finally:
        logger.info("agent runtime shutting down")
        await health_server.close()


def main() -> None:
    asyncio.run(run())


if __name__ == "__main__":
    main()
