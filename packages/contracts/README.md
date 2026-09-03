# Service contracts

This directory contains preliminary language-neutral JSON Schemas for communication between the Go platform API and Python agent runtime. They are intentionally small and evolvable; they do not define a general event bus.

Rules:

- The platform issues jobs; the runtime cannot invent authorization or durable state.
- Every message has a schema version, opaque IDs, tenant scope, and correlation metadata.
- Delivery may be at least once, so message IDs and job idempotency keys are required.
- Artifact references carry integrity hashes; contracts do not embed large artifact bodies.
- Unknown schema versions fail explicitly rather than being guessed.
- No contract carries database credentials or general-purpose platform credentials.

Schemas are drafts until the first runtime integration and may change incompatibly before a tagged release.
