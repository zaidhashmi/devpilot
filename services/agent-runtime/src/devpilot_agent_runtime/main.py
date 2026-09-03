from __future__ import annotations

import asyncio
import logging
import signal

from devpilot_agent_runtime.config import Settings
from devpilot_agent_runtime.health import HealthServer
from devpilot_agent_runtime.logging import configure_logging


async def run() -> None:
    settings = Settings()
    configure_logging(settings.log_level)
    logger = logging.getLogger(__name__)
    stop = asyncio.Event()
    loop = asyncio.get_running_loop()

    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, stop.set)

    health_server = HealthServer(settings.host, settings.port)
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
