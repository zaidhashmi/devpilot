from __future__ import annotations

import asyncio
import json
import logging
import secrets
import uuid
from typing import cast

from agents import Runner

from devpilot_agent_runtime.analysis import _model_pass, repository_analysis_guardrail
from devpilot_agent_runtime.config import Settings
from devpilot_agent_runtime.prompts import OBJECTIVE_GROUNDING_INSTRUCTIONS
from devpilot_agent_runtime.protocol_probe import probe
from devpilot_agent_runtime.providers import ProviderSelection, verify_provider
from devpilot_agent_runtime.schemas import AnalysisRequest, ObjectiveAssessment


async def objective_trial(model: str) -> dict[str, object]:
    hidden_value = str(secrets.randbelow(8000) + 1000)
    reference_id = str(uuid.uuid4())
    settings = Settings(model_provider="ollama", ollama_analysis_model=model)
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
        model=model,
        reasoning_effort="low",
        max_turns=4,
        overall_timeout_seconds=600,
    )
    selection = ProviderSelection("ollama", model, "low")
    await verify_provider(settings, selection)
    result, call = await _model_pass(
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
            "coverage": {"files_read": 1, "source_bytes": 96},
            "evidence_bundle": [
                {
                    "source_reference_id": reference_id,
                    "path": "internal/auth/timeout.go",
                    "start_line": 1,
                    "end_line": 3,
                    "bytes": 96,
                    "truncated": False,
                    "redaction_count": 0,
                    "evidence": (
                        "UNTRUSTED_REPOSITORY_EVIDENCE\n"
                        "package auth\nconst AUTH_SESSION_TIMEOUT_SECONDS = "
                        + hidden_value
                    ),
                }
            ],
        },
        ObjectiveAssessment,
        [repository_analysis_guardrail],
    )
    objective = ObjectiveAssessment.model_validate(result.final_output)
    semantic = hidden_value in objective.answer
    evidence = reference_id in objective.evidence_reference_ids
    diagnostics = cast(dict[str, object], call["diagnostics"])
    return {
        "passed": semantic and evidence,
        "model": model,
        "duration_ms": call["duration_ms"],
        "input_tokens": call["input_tokens"],
        "output_tokens": call["output_tokens"],
        "finish_reason": diagnostics.get("finish_reason"),
        "schema_validation": True,
        "guardrail": True,
        "evidence_validation": evidence,
        "hidden_fact_semantic_validation": semantic,
    }


async def qualify() -> dict[str, object]:
    model = "qwen3:14b"
    trials: list[dict[str, object]] = []
    for _ in range(3):
        result = await objective_trial(model)
        trials.append(result)
        if not result["passed"]:
            return {"status": "not_qualified", "trials": trials}
    full = await probe(model)
    return {
        "status": "qualified" if full.get("status") == "passed" else "not_qualified",
        "trials": trials,
        "full_synthetic": full,
    }


def main() -> None:
    logging.disable(logging.CRITICAL)
    print(json.dumps(asyncio.run(qualify()), separators=(",", ":")))


if __name__ == "__main__":
    main()
