package platform

import (
	"errors"
	"testing"

	"github.com/devpilot/devpilot/services/api/internal/agentclient"
)

func TestBoundedExcerptTruncatesOnLineBoundary(t *testing.T) {
	excerpt, end := boundedExcerpt([]string{"one", "two", "three"}, 10, 7)
	if excerpt != "one\ntwo" || end != 11 {
		t.Fatalf("unexpected bounded excerpt %q ending at %d", excerpt, end)
	}
}

func TestBoundedExcerptSafelyTruncatesLongUTF8Line(t *testing.T) {
	excerpt, end := boundedExcerpt([]string{"éééé"}, 4, 5)
	if excerpt != "éé" || end != 4 {
		t.Fatalf("unexpected UTF-8 excerpt %q ending at %d", excerpt, end)
	}
}

func TestModelTimeoutClassification(t *testing.T) {
	if got := stableWorkerError(agentclient.ErrModelTimeout); got != "model_timeout" {
		t.Fatalf("classification=%q", got)
	}
	if !errors.Is(agentclient.ErrModelTimeout, agentclient.ErrModelTimeout) {
		t.Fatal("model timeout identity lost")
	}
}

func TestAgentBehaviorFailureClassifications(t *testing.T) {
	tests := []struct {
		err  error
		code string
	}{
		{agentclient.ErrModelInvalidOutput, "model_invalid_output"},
		{agentclient.ErrModelContextExhausted, "model_context_exhausted"},
		{agentclient.ErrModelMaxTurns, "model_max_turns"},
		{agentclient.ErrToolTimeout, "tool_timeout"},
		{agentclient.ErrOutputGuardrail, "output_guardrail_rejected"},
		{agentclient.ErrProviderResponseInvalid, "provider_response_invalid"},
	}
	for _, test := range tests {
		if got := stableWorkerError(test.err); got != test.code {
			t.Fatalf("stableWorkerError(%v) = %q, want %q", test.err, got, test.code)
		}
	}
}

func TestSensitiveSourcePaths(t *testing.T) {
	for _, value := range []string{".env", ".env.production", "id_rsa", "service.pem", "client.p12", "my_private_key.txt"} {
		if !sensitivePath(value) {
			t.Fatalf("expected %q to be denied", value)
		}
	}
	for _, value := range []string{"config/example.env", "README.md", "services/api/main.go"} {
		if sensitivePath(value) {
			t.Fatalf("expected %q to be allowed", value)
		}
	}
}

func TestAnalysisValidation(t *testing.T) {
	referenceID := "00000000-0000-4000-8000-000000000001"
	valid := agentclient.Analysis{ObjectiveAssessment: agentclient.ObjectiveAssessment{Answer: "The evidence supports the objective.", EvidenceReferenceIDs: []string{referenceID}, Confidence: "medium", Limitations: []string{"Selected evidence only."}}, Summary: "Evidence-backed result", Confidence: "medium", Findings: []agentclient.Finding{{Category: "security", Severity: "high", Title: "Tenant filter", Description: "Review the tenant predicate.", Confidence: "medium", Evidence: []agentclient.Evidence{{SourceReferenceID: referenceID}}}}}
	if err := validateAnalysis(valid); err != nil {
		t.Fatal(err)
	}
	valid.Findings[0].Category = "invented"
	if err := validateAnalysis(valid); err == nil {
		t.Fatal("unknown category accepted")
	}
	valid.Findings[0].Category = "security"
	valid.ObjectiveAssessment.Answer = ""
	if err := validateAnalysis(valid); err == nil {
		t.Fatal("empty objective answer accepted")
	}
	valid.ObjectiveAssessment.Answer = "A bounded answer."
	valid.ObjectiveAssessment.EvidenceReferenceIDs = nil
	if err := validateAnalysis(valid); err == nil {
		t.Fatal("objective without evidence accepted")
	}
}

func TestTaskRunAnalysisTransitions(t *testing.T) {
	if !ValidTaskRunTransition("approved", "analyzing") {
		t.Fatal("approved must transition to analyzing")
	}
	if !ValidTaskRunTransition("analyzing", "awaiting_approval") {
		t.Fatal("analysis must reach approval")
	}
	if IsTerminalTaskRunStatus("analyzing") {
		t.Fatal("analyzing is not terminal")
	}
}
