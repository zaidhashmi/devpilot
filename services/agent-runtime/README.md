# DevPilot agent runtime

This package is the Phase 0 foundation for the internal Python orchestration runtime. It currently provides validated configuration, JSON logging, lifecycle signals, and liveness/readiness endpoints. It does not run agents or call OpenAI.

The OpenAI Agents SDK is declared for the planned implementation, but use must remain behind DevPilot's typed orchestration and tool boundaries.
