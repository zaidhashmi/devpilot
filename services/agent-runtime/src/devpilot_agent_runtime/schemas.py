from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, ConfigDict, Field, model_validator

from devpilot_agent_runtime.policy import (
    MAX_EVIDENCE_LINES_PER_REQUEST,
    MAX_EVIDENCE_PATH_LENGTH,
    MAX_EVIDENCE_REASON_LENGTH,
    MAX_EVIDENCE_REQUESTS,
)


class StrictModel(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)


class Evidence(StrictModel):
    source_reference_id: str = Field(min_length=36, max_length=36)


class ObjectiveAssessment(StrictModel):
    answer: str = Field(min_length=1, max_length=4000)
    evidence_reference_ids: list[str] = Field(min_length=1, max_length=10)
    confidence: Literal["low", "medium", "high"]
    limitations: list[str] = Field(default_factory=list, max_length=10)

    @model_validator(mode="after")
    def validate_bounded_unique_values(self) -> ObjectiveAssessment:
        if len(self.evidence_reference_ids) != len(set(self.evidence_reference_ids)):
            raise ValueError("duplicate objective evidence reference")
        if any(len(value) != 36 for value in self.evidence_reference_ids):
            raise ValueError("invalid objective evidence reference")
        if any(not value or len(value) > 500 for value in self.limitations):
            raise ValueError("invalid objective limitation")
        return self


class EvidenceReadRequest(StrictModel):
    path: str = Field(min_length=1, max_length=MAX_EVIDENCE_PATH_LENGTH)
    start_line: int = Field(default=1, ge=1)
    line_count: int = Field(
        default=MAX_EVIDENCE_LINES_PER_REQUEST,
        ge=1,
        le=MAX_EVIDENCE_LINES_PER_REQUEST,
    )
    reason: str = Field(min_length=1, max_length=MAX_EVIDENCE_REASON_LENGTH)


class EvidenceSelection(StrictModel):
    requests: list[EvidenceReadRequest] = Field(min_length=1, max_length=MAX_EVIDENCE_REQUESTS)
    coverage_notes: list[str] = Field(default_factory=list, max_length=8)

    @model_validator(mode="after")
    def reject_duplicate_ranges(self) -> EvidenceSelection:
        keys = [(item.path, item.start_line, item.line_count) for item in self.requests]
        if len(keys) != len(set(keys)):
            raise ValueError("duplicate evidence request")
        return self


class AnalysisCoverage(StrictModel):
    repository_index_truncated: bool
    actual_file_count: int = Field(ge=0)
    files_offered: int = Field(ge=0)
    files_read: int = Field(ge=0)
    source_bytes: int = Field(ge=0)
    evidence_requests_submitted: int = Field(ge=0)
    evidence_reads_granted: int = Field(ge=0)
    evidence_reads_partially_granted: int = Field(ge=0)
    evidence_reads_skipped_budget: int = Field(ge=0)
    evidence_bundle_truncated: bool


class Finding(StrictModel):
    category: Literal[
        "architecture", "correctness", "reliability", "security", "maintainability", "testing"
    ]
    severity: Literal["info", "low", "medium", "high", "critical"]
    title: str = Field(min_length=1, max_length=200)
    description: str = Field(min_length=1, max_length=3000)
    confidence: Literal["low", "medium", "high"]
    evidence: list[Evidence] = Field(default_factory=list, max_length=10)


class RepositoryAnalysisBody(StrictModel):
    summary: str = Field(min_length=1, max_length=4000)
    architecture_observations: list[str] = Field(default_factory=list, max_length=20)
    findings: list[Finding] = Field(default_factory=list, max_length=30)
    risks: list[str] = Field(default_factory=list, max_length=20)
    testing_observations: list[str] = Field(default_factory=list, max_length=20)
    suggested_focus_areas: list[str] = Field(default_factory=list, max_length=20)
    unknowns: list[str] = Field(default_factory=list, max_length=20)
    confidence: Literal["low", "medium", "high"]


class RepositoryAnalysis(RepositoryAnalysisBody):
    objective_assessment: ObjectiveAssessment
    coverage: AnalysisCoverage | None = None


class AnalysisRequest(StrictModel):
    version: Literal[1]
    agent_run_id: str
    task_run_id: str
    commit_sha: str = Field(pattern=r"^[0-9a-f]{40}$")
    objective: str = Field(min_length=1, max_length=8000)
    plan_context: dict[str, object]
    inspection_metadata: dict[str, object]
    source_broker_url: str
    source_capability: str = Field(min_length=40, max_length=100)
    prompt_version: Literal["repository-analysis-v2"]
    provider: Literal["openai", "ollama"]
    model: str = Field(min_length=1, max_length=120)
    reasoning_effort: Literal["low", "medium", "high"] | None
    max_turns: int = Field(ge=1, le=20)
    overall_timeout_seconds: int = Field(ge=30, le=1800)


class Usage(StrictModel):
    input_tokens: int | None = Field(default=None, ge=0)
    output_tokens: int | None = Field(default=None, ge=0)
    reasoning_tokens: int | None = Field(default=None, ge=0)
    model_turns: int = Field(ge=0)
    source_tool_calls: int = Field(ge=0)
    source_bytes: int = Field(ge=0)


class AnalysisResult(StrictModel):
    version: Literal[1] = 1
    agent_run_id: str
    status: Literal["succeeded", "failed", "cancelled", "timed_out"]
    analysis: RepositoryAnalysis
    usage: Usage
    trace_id: str | None = None
    provider: Literal["openai", "ollama"]
    model: str
    prompt_version: str
    failure_code: str | None = None
    provider_response_id: str | None = None
    diagnostics: dict[str, object] = Field(default_factory=dict)
    model_calls: list[dict[str, object]] = Field(default_factory=list, max_length=3)
