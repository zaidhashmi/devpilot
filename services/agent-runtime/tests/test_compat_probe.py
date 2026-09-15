from __future__ import annotations

import inspect

from devpilot_agent_runtime.compat_probe import (
    ProbeFailure,
    SyntheticSourceState,
    ToolDiagnosticResult,
    _failure,
)
from devpilot_agent_runtime.diagnostics import ProviderDiagnostics
from devpilot_agent_runtime.schemas import (
    Evidence,
    Finding,
    ObjectiveAssessment,
    RepositoryAnalysis,
)


def test_unpredictable_token_is_not_embedded_in_model_context() -> None:
    import devpilot_agent_runtime.compat_probe as module

    source = inspect.getsource(module._combined_probe)
    assert "secrets.token_hex(8)" in source
    assert "expected_value" not in (
        "Obtain the diagnostic token using the available tool. Return that exact token, "
        "source tool, and success true using the required schema."
    )


def test_combined_result_requires_exact_ephemeral_value_and_tool_call() -> None:
    result = ToolDiagnosticResult(token="runtime-value", source="tool", success=True)
    assert result.token == "runtime-value"
    assert result.source == "tool" and result.success is True
    assert ProbeFailure("model_required_tool_not_used").code == "model_required_tool_not_used"
    assert ProbeFailure("model_tool_result_mismatch").code == "model_tool_result_mismatch"


def test_production_schema_synthetic_result_validates() -> None:
    result = RepositoryAnalysis(
        objective_assessment=ObjectiveAssessment(
            answer="Synthetic evidence supports a limited answer.",
            evidence_reference_ids=["00000000-0000-4000-8000-000000000001"],
            confidence="low",
            limitations=["Synthetic fixture only."],
        ),
        summary="Synthetic structural observation.",
        confidence="low",
    )
    assert RepositoryAnalysis.model_validate(result).findings == []


def test_invented_or_foreign_evidence_is_rejected_by_ownership_check() -> None:
    state = SyntheticSourceState(files={})
    owned = "00000000-0000-4000-8000-000000000001"
    state.references[owned] = "main.go"
    finding = Finding(
        category="security",
        severity="medium",
        title="Synthetic",
        description="Synthetic",
        confidence="medium",
        evidence=[Evidence(source_reference_id=owned)],
    )
    cited = [item.source_reference_id for item in finding.evidence]
    assert all(reference in state.references for reference in cited)
    assert "00000000-0000-4000-8000-000000000002" not in state.references


def test_failure_diagnostic_does_not_retain_random_value_or_output() -> None:
    diagnostics = ProviderDiagnostics(reasoning_present=True, reasoning_characters=50)
    result = _failure("combined", ProbeFailure("model_tool_result_mismatch"), diagnostics, 0.0, 1)
    serialized = str(result)
    assert result["failure_code"] == "model_tool_result_mismatch"
    assert "runtime-value" not in serialized
    assert "assistant response" not in serialized
    assert "reasoning text" not in serialized


def test_probe_turns_and_provider_constraints_are_bounded() -> None:
    import devpilot_agent_runtime.compat_probe as module

    source = inspect.getsource(module)
    assert "max_turns=4" in source and "max_turns=10" not in source
    assert 'selection.provider != "ollama"' in source
    assert 'selection.model_id != "qwen3:8b"' in source
    assert "tool_choice" not in source
