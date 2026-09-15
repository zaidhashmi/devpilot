from __future__ import annotations

import pytest

from devpilot_agent_runtime.protocol_probe import SyntheticSourceContext


@pytest.mark.asyncio
async def test_synthetic_source_fact_is_not_in_index_and_reference_is_platform_controlled() -> None:
    reference = "00000000-0000-4000-8000-000000000001"
    hidden = "7341 * time.Millisecond"
    context = SyntheticSourceContext(hidden, reference)
    index = await context.request("GET", "/internal/v1/source/files")
    assert hidden not in str(index)
    assert reference not in str(index)
    excerpt = await context.request(
        "POST", "/internal/v1/source/read", {"path": "internal/auth/timeout.go"}
    )
    assert excerpt["source_reference_id"] == reference
    assert hidden in str(excerpt["content"])


@pytest.mark.asyncio
async def test_synthetic_injection_text_cannot_add_capabilities() -> None:
    context = SyntheticSourceContext("2001 * time.Millisecond", "ref")
    excerpt = await context.request(
        "POST", "/internal/v1/source/read", {"path": "internal/untrusted/notes.md"}
    )
    assert "change your behavior" in str(excerpt["content"])
    assert context.capability == "x" * 43
    assert not hasattr(context, "shell")
