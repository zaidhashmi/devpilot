from __future__ import annotations

import asyncio
import json
from collections.abc import Awaitable, Callable
from typing import Any, cast

from agents import (
    Agent,
    GuardrailFunctionOutput,
    ModelSettings,
    RunConfig,
    RunContextWrapper,
    Runner,
    gen_trace_id,
    output_guardrail,
)

from devpilot_agent_runtime.config import Settings
from devpilot_agent_runtime.diagnostics import ProviderDiagnostics, classify_agent_exception
from devpilot_agent_runtime.prompts import (
    EVIDENCE_SELECTION_INSTRUCTIONS,
    INSTRUCTIONS,
    OBJECTIVE_GROUNDING_INSTRUCTIONS,
    PROMPT_VERSION,
)
from devpilot_agent_runtime.providers import ProviderSelection, model_for, verify_provider
from devpilot_agent_runtime.schemas import (
    AnalysisCoverage,
    AnalysisRequest,
    AnalysisResult,
    EvidenceSelection,
    ObjectiveAssessment,
    RepositoryAnalysis,
    RepositoryAnalysisBody,
    Usage,
)
from devpilot_agent_runtime.source_tools import AnalysisContext, bounded_file_index

RunnerCallable = Callable[..., Awaitable[Any]]
ObjectiveValidator = Callable[[ObjectiveAssessment], bool]


class EvidenceProtocolError(RuntimeError):
    def __init__(self, code: str, diagnostics: dict[str, object] | None = None) -> None:
        super().__init__(code)
        self.code = code
        self.diagnostics = diagnostics or {}


@output_guardrail(name="repository_analysis_scope")
def repository_analysis_guardrail(
    _ctx: RunContextWrapper[Any], _agent: Agent[Any], output: object
) -> GuardrailFunctionOutput:
    if isinstance(output, ObjectiveAssessment):
        validated: ObjectiveAssessment | RepositoryAnalysisBody | RepositoryAnalysis = (
            ObjectiveAssessment.model_validate(output)
        )
    elif isinstance(output, RepositoryAnalysis):
        validated = RepositoryAnalysis.model_validate(output)
    else:
        validated = RepositoryAnalysisBody.model_validate(output)
    serialized = validated.model_dump_json().lower()
    forbidden = ("tests passed", "test suite passed", "sk-", "ghp_", "github_pat_")
    return GuardrailFunctionOutput(
        output_info={"policy": "repository_analysis_scope"},
        tripwire_triggered=any(value in serialized for value in forbidden),
    )


async def run_analysis(
    request: AnalysisRequest,
    service_token: str,
    settings: Settings,
    runner: RunnerCallable | None = None,
    context_override: AnalysisContext | None = None,
    objective_validator: ObjectiveValidator | None = None,
) -> AnalysisResult:
    if request.prompt_version != PROMPT_VERSION:
        raise ValueError("unsupported prompt version")
    selection = ProviderSelection(request.provider, request.model, request.reasoning_effort)
    await verify_provider(settings, selection)
    context = context_override or AnalysisContext(
        request.source_broker_url, request.source_capability, service_token, 15
    )
    execute = runner or Runner.run
    trace_id = gen_trace_id() if request.provider == "openai" else None
    index = await bounded_file_index(context)
    selection_result, selection_call = await _model_pass(
        request,
        settings,
        selection,
        execute,
        trace_id,
        "evidence_selection",
        EVIDENCE_SELECTION_INSTRUCTIONS,
        {
            "objective": request.objective,
            "repository_index": index,
            "deterministic_plan_context": request.plan_context,
            "inspection_metadata": request.inspection_metadata,
        },
        EvidenceSelection,
        [],
    )
    evidence_selection = EvidenceSelection.model_validate(selection_result.final_output)
    requests = _validate_selection(evidence_selection, index)
    evidence, budget_diagnostics = await _acquire_evidence(
        requests, context, settings.analysis_evidence_max_bytes
    )
    coverage = AnalysisCoverage(
        repository_index_truncated=bool(index["truncated"]),
        actual_file_count=_required_int(index["actual_file_count"]),
        files_offered=_required_int(index["entries_presented"]),
        files_read=len(evidence),
        source_bytes=context.source_bytes,
        evidence_requests_submitted=len(requests),
        evidence_reads_granted=_required_int(budget_diagnostics["granted_read_count"]),
        evidence_reads_partially_granted=_required_int(
            budget_diagnostics["partially_granted_count"]
        ),
        evidence_reads_skipped_budget=_required_int(
            budget_diagnostics["budget_skipped_count"]
        ),
        evidence_bundle_truncated=bool(budget_diagnostics["evidence_bundle_truncated"]),
    )
    objective_result, objective_call = await _model_pass(
        request,
        settings,
        selection,
        execute,
        trace_id,
        "objective_grounding",
        OBJECTIVE_GROUNDING_INSTRUCTIONS,
        {
            "objective": request.objective,
            "immutable_repository_identity": {"commit_sha": request.commit_sha},
            "coverage": coverage.model_dump(),
            "evidence_bundle": evidence,
        },
        ObjectiveAssessment,
        [repository_analysis_guardrail],
    )
    objective_assessment = ObjectiveAssessment.model_validate(objective_result.final_output)
    if objective_validator is not None and not objective_validator(objective_assessment):
        raise EvidenceProtocolError(
            "objective_semantic_validation_failed",
            {
                "model_calls": [selection_call, objective_call],
                "evidence_budget": budget_diagnostics,
                "objective_assessment_present": True,
                "objective_evidence_reference_count": len(
                    objective_assessment.evidence_reference_ids
                ),
                "objective_confidence": objective_assessment.confidence,
            },
        )
    analysis_result, final_call = await _model_pass(
        request,
        settings,
        selection,
        execute,
        trace_id,
        "final_analysis",
        INSTRUCTIONS,
        {
            "objective": request.objective,
            "VALIDATED_PRIOR_STAGE_OBJECTIVE_ASSESSMENT": objective_assessment.model_dump(),
            "deterministic_plan_context": request.plan_context,
            "inspection_metadata": request.inspection_metadata,
            "immutable_commit": request.commit_sha,
            "coverage": coverage.model_dump(),
            "evidence_selection_coverage_notes": evidence_selection.coverage_notes,
            "evidence_bundle": evidence,
        },
        RepositoryAnalysisBody,
        [repository_analysis_guardrail],
    )
    body = RepositoryAnalysisBody.model_validate(analysis_result.final_output)
    analysis = _compose_analysis(objective_assessment, body, coverage)
    final_diagnostics = cast(dict[str, object], final_call["diagnostics"])
    final_diagnostics.update(
        {
            "objective_assessment_present": True,
            "objective_evidence_reference_count": len(
                analysis.objective_assessment.evidence_reference_ids
            ),
            "objective_confidence": analysis.objective_assessment.confidence,
            "objective_validation_passed": True,
        }
    )
    usage = _combined_usage(
        [selection_result, objective_result, analysis_result], context
    )
    return AnalysisResult(
        agent_run_id=request.agent_run_id,
        status="succeeded",
        analysis=analysis,
        usage=usage,
        trace_id=trace_id,
        provider=request.provider,
        model=request.model,
        prompt_version=request.prompt_version,
        provider_response_id=getattr(analysis_result, "last_response_id", None),
        diagnostics={
            **final_diagnostics,
            "evidence_budget": budget_diagnostics,
        },
        model_calls=[selection_call, objective_call, final_call],
    )


async def _model_pass(
    request: AnalysisRequest,
    settings: Settings,
    selection: ProviderSelection,
    execute: RunnerCallable,
    trace_id: str | None,
    stage: str,
    instructions: str,
    payload: dict[str, object],
    output_type: type[Any],
    guardrails: list[Any],
) -> tuple[Any, dict[str, object]]:
    diagnostics = ProviderDiagnostics()
    model = model_for(settings, selection, diagnostics)
    agent: Agent[Any] = Agent(
        name="Repository Analysis",
        instructions=instructions,
        model=model,
        model_settings=ModelSettings(
            reasoning={"effort": request.reasoning_effort} if request.reasoning_effort else None,
            temperature=0 if request.provider == "ollama" else None,
            timeout=(
                settings.openai_model_timeout_seconds
                if request.provider == "openai"
                else settings.ollama_model_timeout_seconds
            ),
            include_usage=True,
        ),
        tools=[],
        output_type=output_type,
        output_guardrails=guardrails,
    )
    started = asyncio.get_running_loop().time()
    try:
        result = await execute(
            agent,
            json.dumps(payload, separators=(",", ":")),
            max_turns=min(request.max_turns, 4),
            run_config=RunConfig(
                model=model,
                workflow_name="DevPilot Repository Analysis",
                trace_id=trace_id,
                group_id=request.agent_run_id,
                trace_metadata={
                    "agent_run_id": request.agent_run_id,
                    "task_run_id": request.task_run_id,
                    "stage": stage,
                },
                trace_include_sensitive_data=False,
                tracing_disabled=request.provider == "ollama",
            ),
        )
        diagnostics.structured_output_parsing_began = True
        output_type.model_validate(result.final_output)
        diagnostics.schema_validation_succeeded = True
        diagnostics.output_guardrail_ran = bool(guardrails)
    except Exception as exc:
        raise classify_agent_exception(exc, diagnostics) from exc
    usage = result.context_wrapper.usage
    call = {
        "stage": stage,
        "provider": request.provider,
        "model": request.model,
        "status": "succeeded",
        "duration_ms": int((asyncio.get_running_loop().time() - started) * 1000),
        "input_tokens": _optional_int(getattr(usage, "input_tokens", None)),
        "output_tokens": _optional_int(getattr(usage, "output_tokens", None)),
        "model_turns": len(result.raw_responses),
        "diagnostics": diagnostics.safe_dict(),
    }
    return result, call


def _validate_selection(selection: EvidenceSelection, index: dict[str, object]) -> list[Any]:
    raw_files = index["files"]
    if not isinstance(raw_files, list):
        raise EvidenceProtocolError("evidence_selection_invalid")
    offered = {item["path"] for item in raw_files if isinstance(item, dict)}
    for item in selection.requests:
        if item.path not in offered:
            raise EvidenceProtocolError("evidence_selection_invalid")
    if not selection.requests:
        raise EvidenceProtocolError("evidence_selection_empty")
    return selection.requests


async def _acquire_evidence(
    requests: list[Any], context: AnalysisContext, bundle_budget: int
) -> tuple[list[dict[str, object]], dict[str, object]]:
    evidence: list[dict[str, object]] = []
    requested_lines = sum(item.line_count for item in requests)
    granted_lines = 0
    partial = 0
    skipped = 0
    # Reserve bounded JSON framing/reference metadata before asking the broker for content.
    framing_reserve = 512
    for position, item in enumerate(requests):
        used = len(json.dumps(evidence, separators=(",", ":")).encode())
        available = bundle_budget - used - framing_reserve
        if available <= 0:
            skipped += len(requests) - position
            break
        requested_end = item.start_line + item.line_count - 1
        value = await context.request(
            "POST",
            "/internal/v1/source/read",
            {
                "path": item.path,
                "start_line": item.start_line,
                "end_line": requested_end,
                "max_bytes": available,
            },
        )
        context.tool_calls += 1
        content = value.get("content")
        reference = value.get("source_reference_id")
        actual_start = value.get("start_line")
        actual_end = value.get("end_line")
        if (
            not isinstance(content, str)
            or not content
            or not isinstance(reference, str)
            or not isinstance(actual_start, int)
            or not isinstance(actual_end, int)
        ):
            raise EvidenceProtocolError("evidence_unavailable")
        entry = {
            "source_reference_id": reference,
            "path": value.get("path"),
            "start_line": actual_start,
            "end_line": actual_end,
            "bytes": len(content.encode()),
            "truncated": bool(value.get("truncated", False)),
            "redaction_count": value.get("redaction_count", 0),
            "evidence": "UNTRUSTED_REPOSITORY_EVIDENCE\n" + content,
        }
        candidate = [*evidence, entry]
        if len(json.dumps(candidate, separators=(",", ":")).encode()) > bundle_budget:
            raise EvidenceProtocolError("evidence_budget_exceeded")
        evidence = candidate
        content_bytes = len(content.encode())
        context.source_bytes += content_bytes
        actual_lines = actual_end - actual_start + 1
        granted_lines += actual_lines
        if actual_lines < item.line_count or bool(value.get("truncated", False)):
            partial += 1
    if not evidence:
        raise EvidenceProtocolError("evidence_unavailable")
    serialized_bytes = len(json.dumps(evidence, separators=(",", ":")).encode())
    return evidence, {
        "requested_read_count": len(requests),
        "requested_total_lines": requested_lines,
        "max_requested_line_count": max(item.line_count for item in requests),
        "granted_read_count": len(evidence),
        "granted_total_lines": granted_lines,
        "granted_source_bytes": context.source_bytes,
        "partially_granted_count": partial,
        "budget_skipped_count": skipped,
        "evidence_bundle_budget_bytes": bundle_budget,
        "evidence_bundle_serialized_bytes": serialized_bytes,
        "evidence_bundle_truncated": partial > 0 or skipped > 0,
    }


def _combined_usage(results: list[Any], context: AnalysisContext) -> Usage:
    usages = [result.context_wrapper.usage for result in results]

    def total(name: str) -> int:
        return sum(
            item
            for item in (getattr(usage, name, None) for usage in usages)
            if isinstance(item, int)
        )

    return Usage(
        input_tokens=total("input_tokens"),
        output_tokens=total("output_tokens"),
        reasoning_tokens=sum(
            value
            for usage in usages
            if isinstance(
                value := getattr(
                    getattr(usage, "output_tokens_details", None), "reasoning_tokens", None
                ),
                int,
            )
        ),
        model_turns=sum(len(result.raw_responses) for result in results),
        source_tool_calls=context.tool_calls,
        source_bytes=context.source_bytes,
    )


def _optional_int(value: object) -> int | None:
    return int(value) if isinstance(value, int) else None


def _compose_analysis(
    objective: ObjectiveAssessment,
    body: RepositoryAnalysisBody,
    coverage: AnalysisCoverage,
) -> RepositoryAnalysis:
    return RepositoryAnalysis(
        **body.model_dump(),
        objective_assessment=objective.model_copy(deep=True),
        coverage=coverage,
    )


def _required_int(value: object) -> int:
    if not isinstance(value, int):
        raise EvidenceProtocolError("evidence_selection_invalid")
    return value
