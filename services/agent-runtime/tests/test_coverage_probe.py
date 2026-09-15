from devpilot_agent_runtime.coverage_probe import (
    FACT_LINE,
    FACT_PATH,
    coverage_provenance,
    should_run_objective_grounding,
)
from devpilot_agent_runtime.schemas import EvidenceReadRequest


def evidence(start: int, end: int, content: str = "hidden-needle") -> list[dict[str, object]]:
    return [{"path": FACT_PATH, "start_line": start, "end_line": end, "evidence": content}]


def test_fact_inside_requested_and_granted_range() -> None:
    result = coverage_provenance(
        [EvidenceReadRequest(path=FACT_PATH, start_line=1, line_count=10, reason="fact")],
        evidence(1, 10),
        "hidden-needle",
    )
    assert result["outcome"] == "ready_for_objective_grounding"


def test_partial_grant_can_remove_fact() -> None:
    result = coverage_provenance(
        [EvidenceReadRequest(path=FACT_PATH, start_line=1, line_count=10, reason="fact")],
        evidence(1, FACT_LINE - 1, "unrelated"),
        "hidden-needle",
    )
    assert result["outcome"] == "normalization_removed_fact"
    assert result["requested_start_line"] == 1
    assert result["requested_end_line"] == 10
    assert result["granted_start_line"] == 1
    assert result["granted_end_line"] == FACT_LINE - 1
    assert result["granted_line_count"] == 1


def test_range_miss_and_wrong_file_are_distinct() -> None:
    missed = coverage_provenance(
        [EvidenceReadRequest(path=FACT_PATH, start_line=3, line_count=4, reason="fact")],
        evidence(3, 6, "unrelated"),
        "hidden-needle",
    )
    wrong = coverage_provenance(
        [EvidenceReadRequest(path="README.md", start_line=1, line_count=4, reason="fact")],
        [],
        "hidden-needle",
    )
    assert missed["outcome"] == "selection_range_missed_fact"
    assert wrong["outcome"] == "selection_wrong_file"


def test_bundle_missing_fact_blocks_objective_stage() -> None:
    result = coverage_provenance(
        [EvidenceReadRequest(path=FACT_PATH, start_line=1, line_count=10, reason="fact")],
        evidence(1, 10, "unrelated"),
        "hidden-needle",
    )
    assert result["outcome"] == "evidence_bundle_missing_fact"
    assert not should_run_objective_grounding(result)


def test_skipped_fact_request_is_classified_as_normalization_loss() -> None:
    result = coverage_provenance(
        [EvidenceReadRequest(path=FACT_PATH, start_line=1, line_count=10, reason="fact")],
        [],
        "hidden-needle",
    )
    assert result["outcome"] == "normalization_removed_fact"
    assert result["granted_line_count"] == 0
    assert not should_run_objective_grounding(result)


def test_objective_stage_runs_only_when_all_coverage_checks_pass() -> None:
    ready = coverage_provenance(
        [EvidenceReadRequest(path=FACT_PATH, start_line=1, line_count=10, reason="fact")],
        evidence(1, 10),
        "hidden-needle",
    )
    assert should_run_objective_grounding(ready)
