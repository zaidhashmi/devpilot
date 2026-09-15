from __future__ import annotations

import asyncio
import json
import logging
import secrets
import uuid
from typing import Any, cast

from agents import Runner

from devpilot_agent_runtime.analysis import (
    _acquire_evidence,
    _model_pass,
    _validate_selection,
    repository_analysis_guardrail,
)
from devpilot_agent_runtime.config import Settings
from devpilot_agent_runtime.prompts import (
    EVIDENCE_SELECTION_INSTRUCTIONS,
    OBJECTIVE_GROUNDING_INSTRUCTIONS,
)
from devpilot_agent_runtime.protocol_probe import SyntheticSourceContext
from devpilot_agent_runtime.providers import ProviderSelection, verify_provider
from devpilot_agent_runtime.schemas import (
    AnalysisRequest,
    EvidenceReadRequest,
    EvidenceSelection,
    ObjectiveAssessment,
)
from devpilot_agent_runtime.source_tools import bounded_file_index

FACT_PATH = "internal/auth/timeout.go"
FACT_LINE = 2


def should_run_objective_grounding(provenance: dict[str, object]) -> bool:
    return provenance.get("outcome") == "ready_for_objective_grounding"


def coverage_provenance(
    requests: list[EvidenceReadRequest],
    evidence: list[dict[str, object]],
    hidden_value: str,
) -> dict[str, object]:
    fact_requests = [item for item in requests if item.path == FACT_PATH]
    selected = bool(fact_requests)
    covering = next(
        (
            item
            for item in fact_requests
            if item.start_line <= FACT_LINE <= item.start_line + item.line_count - 1
        ),
        None,
    )
    matching = next((item for item in evidence if item.get("path") == FACT_PATH), None)
    granted_contains = bool(
        matching
        and isinstance(matching.get("start_line"), int)
        and isinstance(matching.get("end_line"), int)
        and cast(int, matching["start_line"]) <= FACT_LINE <= cast(int, matching["end_line"])
    )
    bundle_contains = hidden_value in json.dumps(evidence, separators=(",", ":"))
    requested = covering or (fact_requests[0] if fact_requests else None)
    if not selected:
        outcome = "selection_wrong_file"
    elif covering is None:
        outcome = "selection_range_missed_fact"
    elif not granted_contains:
        outcome = "normalization_removed_fact"
    elif not bundle_contains:
        outcome = "evidence_bundle_missing_fact"
    else:
        outcome = "ready_for_objective_grounding"
    return {
        "outcome": outcome,
        "fact_file_selected": selected,
        "requested_range_contains_fact": covering is not None,
        "granted_range_contains_fact": granted_contains,
        "pass2_bundle_contains_fact": bundle_contains,
        "fact_line_number": FACT_LINE,
        "requested_start_line": requested.start_line if requested else None,
        "requested_end_line": (
            requested.start_line + requested.line_count - 1 if requested else None
        ),
        "requested_line_count": requested.line_count if requested else None,
        "granted_start_line": matching.get("start_line") if matching else None,
        "granted_end_line": matching.get("end_line") if matching else None,
        "granted_line_count": (
            cast(int, matching["end_line"]) - cast(int, matching["start_line"]) + 1
            if matching
            and isinstance(matching.get("start_line"), int)
            and isinstance(matching.get("end_line"), int)
            else 0
        ),
    }


async def run_probe() -> dict[str, object]:
    hidden_value = str(secrets.randbelow(8000) + 1000)
    reference_id = str(uuid.uuid4())
    context = SyntheticSourceContext(f"{hidden_value} * time.Millisecond", reference_id)
    settings = Settings(model_provider="ollama", analysis_evidence_max_bytes=1024)
    request = AnalysisRequest(
        version=1,
        provider="ollama",
        agent_run_id=str(uuid.uuid4()),
        task_run_id=str(uuid.uuid4()),
        commit_sha="a" * 40,
        objective=(
            "Determine the authentication session timeout configured by the service and state "
            "the value supported by repository evidence."
        ),
        plan_context={},
        inspection_metadata={},
        source_broker_url="http://synthetic.invalid",
        source_capability="x" * 43,
        prompt_version="repository-analysis-v2",
        model="qwen3:14b",
        reasoning_effort="low",
        max_turns=4,
        overall_timeout_seconds=600,
    )
    selection = ProviderSelection("ollama", request.model, "low")
    await verify_provider(settings, selection)
    index = await bounded_file_index(context)
    selected, stage_one = await _model_pass(
        request,
        settings,
        selection,
        Runner.run,
        None,
        "evidence_selection",
        EVIDENCE_SELECTION_INSTRUCTIONS,
        {"objective": request.objective, "repository_index": index},
        EvidenceSelection,
        [],
    )
    requests = _validate_selection(EvidenceSelection.model_validate(selected.final_output), index)
    evidence, budget = await _acquire_evidence(
        requests, context, settings.analysis_evidence_max_bytes
    )
    provenance = coverage_provenance(requests, evidence, hidden_value)
    base: dict[str, object] = {
        "stage_one": stage_one,
        "files_offered": index["entries_presented"],
        "synthetic_file_count": index["actual_file_count"],
        "synthetic_fact_file_line_count": 122,
        "provenance": provenance,
        "budget": budget,
        "evidence_bundle_bytes": len(json.dumps(evidence, separators=(",", ":")).encode()),
    }
    if not should_run_objective_grounding(provenance):
        base.update(
            objective_grounding="not_run_due_to_missing_evidence",
            root_cause=provenance["outcome"],
        )
        return base
    objective_result, stage_two = await _model_pass(
        request,
        settings,
        selection,
        Runner.run,
        None,
        "objective_grounding",
        OBJECTIVE_GROUNDING_INSTRUCTIONS,
        {
            "objective": request.objective,
            "immutable_repository_identity": {"commit_sha": request.commit_sha},
            "coverage": budget,
            "evidence_bundle": evidence,
        },
        ObjectiveAssessment,
        [repository_analysis_guardrail],
    )
    objective = ObjectiveAssessment.model_validate(objective_result.final_output)
    semantic = hidden_value in objective.answer
    reference_valid = reference_id in objective.evidence_reference_ids
    base.update(
        objective_grounding={
            "duration_ms": stage_two["duration_ms"],
            "input_tokens": stage_two["input_tokens"],
            "output_tokens": stage_two["output_tokens"],
            "finish_reason": cast(dict[str, Any], stage_two["diagnostics"]).get(
                "finish_reason"
            ),
            "schema_validation": True,
            "guardrail": True,
            "evidence_validation": reference_valid,
            "semantic_validation": semantic,
        },
        root_cause=(
            "success"
            if semantic and reference_valid
            else "objective_grounding_failed_with_fact_present"
        ),
    )
    return base


def main() -> None:
    logging.disable(logging.CRITICAL)
    print(json.dumps(asyncio.run(run_probe()), separators=(",", ":")))


if __name__ == "__main__":
    main()
