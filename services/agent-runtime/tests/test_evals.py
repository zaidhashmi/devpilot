from __future__ import annotations

import json
from pathlib import Path


def test_evaluation_scenarios_are_bounded_and_unique() -> None:
    path = Path(__file__).parents[1] / "evals" / "scenarios.json"
    scenarios = json.loads(path.read_text())
    assert isinstance(scenarios, list)
    assert 5 <= len(scenarios) <= 12
    identifiers = [item["id"] for item in scenarios]
    assert len(identifiers) == len(set(identifiers))
    assert "prompt-injection-readme" in identifiers
