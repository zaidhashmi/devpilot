# Contributing to DevPilot

Thank you for helping build DevPilot. The project favors small, reviewable changes and explicit security boundaries.

## Before opening a change

1. Discuss substantial architecture or product changes in an issue first.
2. Read the architecture decisions under `docs/adr` and the threat model.
3. Keep business state in the Go platform boundary; do not let the agent runtime write platform databases.
4. Never execute repository code on a service host or mount a Docker socket into a future sandbox.

## Development

Use the toolchain versions documented in each component. Run `make bootstrap` once and `make check` before submitting. Add focused tests for changed behavior and update contracts or ADRs when boundaries change.

Commits should be understandable in isolation. Pull requests should explain the problem, approach, validation, security impact, and follow-up work. Generated code and patches require the same review standard as human-written changes.

By participating, you agree to follow the Code of Conduct. Contributions are accepted under Apache-2.0.
