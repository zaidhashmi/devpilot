# DevPilot agent runtime

This service runs DevPilot's bounded Python agent orchestration. Phase 5 contains exactly one
read-only Repository Analysis Agent built with the OpenAI Agents SDK. It can use the official
OpenAI Responses integration or an Ollama OpenAI-compatible Chat Completions client. It receives immutable
workflow context from the Go platform, selects bounded evidence in a first structured pass, and analyzes
only the excerpts acquired by the platform in a second structured pass. It returns
strict Pydantic output. It has no database or GitHub credentials and cannot execute or modify
repository code.

`/healthz` reports process health. `/readyz` reports whether explicit analysis configuration is
present without exposing values. Normal tests use a deterministic runner double and never call
the selected model provider. Provider selection is centralized and persisted by the Go platform before
execution; retrying an AgentRun never changes its provider or model.
