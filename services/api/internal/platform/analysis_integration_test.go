package platform

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/devpilot/devpilot/services/api/internal/agentclient"
	"github.com/devpilot/devpilot/services/api/internal/config"
)

type fakeAnalysisClient struct {
	service    *Service
	requests   []agentclient.Request
	cancelled  []string
	selection  agentclient.ProviderSelection
	resolves   int
	analyzeErr error
}

func (f *fakeAnalysisClient) ResolveProvider(context.Context) (agentclient.ProviderSelection, error) {
	f.resolves++
	if f.selection.Provider != "" {
		return f.selection, nil
	}
	reasoning := "medium"
	return agentclient.ProviderSelection{Provider: "openai", Model: "gpt-5.6-sol", ReasoningEffort: &reasoning}, nil
}

func (f *fakeAnalysisClient) Analyze(ctx context.Context, input agentclient.Request) (agentclient.Result, error) {
	f.requests = append(f.requests, input)
	if f.analyzeErr != nil {
		return agentclient.Result{}, f.analyzeErr
	}
	if _, err := f.service.ListAgentSourceFiles(ctx, "invalid-capability"); !errors.Is(err, ErrForbidden) {
		return agentclient.Result{}, errors.New("invalid capability accepted")
	}
	files, err := f.service.ListAgentSourceFiles(ctx, input.SourceCapability)
	if err != nil {
		return agentclient.Result{}, err
	}
	for _, file := range files {
		if file.Path == ".env" {
			return agentclient.Result{}, errors.New("sensitive file exposed")
		}
	}
	if _, err = f.service.ReadAgentSource(ctx, input.SourceCapability, ".env", 1, 1, 0); !errors.Is(err, ErrInvalidInput) {
		return agentclient.Result{}, errors.New("sensitive path accepted")
	}
	if _, err = f.service.ReadAgentSource(ctx, input.SourceCapability, "internal/app.go", 1, 251, 0); !errors.Is(err, ErrInvalidInput) {
		return agentclient.Result{}, errors.New("line budget bypassed")
	}
	excerpt, err := f.service.ReadAgentSource(ctx, input.SourceCapability, "internal/app.go", 1, 4, 0)
	if err != nil {
		return agentclient.Result{}, err
	}
	if strings.Contains(excerpt.Content, "abcdefghijklmnop123456") || !strings.Contains(excerpt.Content, "[REDACTED_SECRET]") {
		return agentclient.Result{}, errors.New("secret not redacted")
	}
	repeated, err := f.service.ReadAgentSource(ctx, input.SourceCapability, "internal/app.go", 1, 4, 0)
	if err != nil || repeated.SourceReferenceID != excerpt.SourceReferenceID {
		return agentclient.Result{}, errors.New("source read was not idempotent")
	}
	if input.CommitSHA == "" || input.CommitSHA != excerpt.CommitSHA {
		return agentclient.Result{}, errors.New("immutable commit was not propagated")
	}
	inputTokens, outputTokens, reasoningTokens := int64(100), int64(30), int64(5)
	traceID := "trace_test"
	return agentclient.Result{Version: 1, Provider: input.Provider, AgentRunID: input.AgentRunID, Status: "succeeded", Model: input.Model, PromptVersion: input.PromptVersion, TraceID: &traceID, ProviderResponseID: "resp_test", Usage: agentclient.Usage{InputTokens: &inputTokens, OutputTokens: &outputTokens, ReasoningTokens: &reasoningTokens, ModelTurns: 2, SourceToolCalls: 2, SourceBytes: int64(len(excerpt.Content))}, Analysis: agentclient.Analysis{ObjectiveAssessment: agentclient.ObjectiveAssessment{Answer: "The evidence supports the requested boundary review.", EvidenceReferenceIDs: []string{excerpt.SourceReferenceID}, Confidence: "medium", Limitations: []string{"Only selected source was reviewed."}}, Summary: "Durable boundaries were inspected without execution.", Confidence: "medium", Findings: []agentclient.Finding{{Category: "security", Severity: "medium", Title: "Repository text is untrusted", Description: "The source contains an instruction-shaped comment that must remain data.", Confidence: "high", Evidence: []agentclient.Evidence{{SourceReferenceID: excerpt.SourceReferenceID}}}}}}, nil
}

func TestClassifiedAgentFailuresFinalizeAttempts(t *testing.T) {
	tests := []struct {
		cause error
		code  string
	}{
		{agentclient.ErrModelInvalidOutput, "model_invalid_output"},
		{agentclient.ErrModelContextExhausted, "model_context_exhausted"},
		{agentclient.ErrModelMaxTurns, "model_max_turns"},
		{agentclient.ErrOutputGuardrail, "output_guardrail_rejected"},
		{agentclient.ErrProviderResponseInvalid, "provider_response_invalid"},
	}
	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			s, actor, repo, _ := orchestrationFixture(t)
			failure := &agentclient.RuntimeError{
				Cause: test.cause,
				Code:  test.code,
				Diagnostics: map[string]any{
					"finish_reason": "length",
				},
			}
			fake := &fakeAnalysisClient{service: s, analyzeErr: failure}
			s.SetAgentClient(fake, AgentRuntimeConfig{SourceBrokerURL: "http://platform.internal", PromptVersion: "repository-analysis-v2", MaxTurns: 10, OverallTimeout: time.Minute})
			ctx := context.Background()
			task, _ := s.CreateTask(ctx, actor, repo.ID, "Failure", "Classify failure", "req")
			run, _ := s.CreateTaskRun(ctx, actor, task.ID, "main", "req")
			cfg := config.WorkerConfig{MaxAttempts: 1, BaseRetry: time.Millisecond, MaxRetry: time.Second}
			first, _, _ := s.ClaimOutbox(ctx, "worker", time.Minute)
			if err := s.ProcessOutbox(ctx, first, cfg); err != nil {
				t.Fatal(err)
			}
			current, _ := s.TaskRun(ctx, actor, run.ID)
			_, _ = s.DecideApproval(ctx, actor, current.Approval.ID, "approved", "", "approve")
			second, _, _ := s.ClaimOutbox(ctx, "worker", time.Minute)
			if err := s.ProcessOutbox(ctx, second, cfg); err != nil {
				t.Fatal(err)
			}
			third, _, _ := s.ClaimOutbox(ctx, "worker", time.Minute)
			_ = s.ProcessOutbox(ctx, third, cfg)
			var attemptStatus, failureCode string
			var diagnosticJSON []byte
			if err := s.db.QueryRow(ctx, `SELECT status,failure_code,provider_diagnostics FROM agent_run_attempts WHERE agent_run_id=(SELECT id FROM agent_runs WHERE task_run_id=$1)`, run.ID).Scan(&attemptStatus, &failureCode, &diagnosticJSON); err != nil {
				t.Fatal(err)
			}
			diagnostics := map[string]any{}
			if err := json.Unmarshal(diagnosticJSON, &diagnostics); err != nil {
				t.Fatal(err)
			}
			if attemptStatus == "running" || failureCode != test.code || diagnostics["finish_reason"] != "length" {
				t.Fatalf("status=%q code=%q diagnostics=%v", attemptStatus, failureCode, diagnostics)
			}
		})
	}
}
func (f *fakeAnalysisClient) Cancel(_ context.Context, id string) error {
	f.cancelled = append(f.cancelled, id)
	return nil
}

func TestRepositoryAnalysisWorkflowAndEvidenceBoundary(t *testing.T) {
	s, actor, repo, _ := orchestrationFixture(t)
	fake := &fakeAnalysisClient{service: s}
	s.SetAgentClient(fake, AgentRuntimeConfig{SourceBrokerURL: "http://platform.internal", PromptVersion: "repository-analysis-v2", MaxTurns: 10, OverallTimeout: time.Minute})
	ctx := context.Background()
	task, err := s.CreateTask(ctx, actor, repo.ID, "Analyze safety", "Find the highest-risk architecture gaps", "req")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateTaskRun(ctx, actor, task.ID, "main", "req")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.WorkerConfig{MaxAttempts: 5, BaseRetry: time.Millisecond, MaxRetry: time.Second}
	event, ok, err := s.ClaimOutbox(ctx, "worker", time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err = s.ProcessOutbox(ctx, event, cfg); err != nil {
		t.Fatal(err)
	}
	current, err := s.TaskRun(ctx, actor, run.ID)
	if err != nil || current.Approval == nil {
		t.Fatalf("run=%+v err=%v", current, err)
	}
	if _, err = s.DecideApproval(ctx, actor, current.Approval.ID, "approved", "", "approve"); err != nil {
		t.Fatal(err)
	}
	event, ok, err = s.ClaimOutbox(ctx, "worker", time.Minute)
	if err != nil || !ok || event.EventType != startAnalysisEvent {
		t.Fatalf("event=%+v ok=%v err=%v", event, ok, err)
	}
	if err = s.ProcessOutbox(ctx, event, cfg); err != nil {
		t.Fatal(err)
	}
	// Changing runtime auto-selection after AgentRun creation must not change this run.
	fake.selection = agentclient.ProviderSelection{Provider: "ollama", Model: "qwen3:8b"}
	event, ok, err = s.ClaimOutbox(ctx, "worker", time.Minute)
	if err != nil || !ok || event.EventType != executeAgentEvent {
		t.Fatalf("event=%+v ok=%v err=%v", event, ok, err)
	}
	if err = s.ProcessOutbox(ctx, event, cfg); err != nil {
		t.Fatal(err)
	}
	current, err = s.TaskRun(ctx, actor, run.ID)
	if err != nil || current.Status != "awaiting_approval" || current.CurrentStage != "analysis_approval" || current.AgentRun == nil || current.Analysis == nil || current.Approval == nil || current.Approval.ApprovalType != "proceed_to_planning" {
		t.Fatalf("run=%+v err=%v", current, err)
	}
	if current.Approval.AnalysisRevisionID == nil || *current.Approval.AnalysisRevisionID != current.Analysis.ID {
		t.Fatal("approval is not bound to exact analysis")
	}
	var refs, redactions, activeCaps int
	if err = s.db.QueryRow(ctx, `SELECT count(*),COALESCE(sum(redaction_count),0) FROM agent_source_accesses WHERE agent_run_id=$1`, current.AgentRun.ID).Scan(&refs, &redactions); err != nil || refs != 1 || redactions < 1 {
		t.Fatalf("refs=%d redactions=%d err=%v", refs, redactions, err)
	}
	if err = s.db.QueryRow(ctx, `SELECT count(*) FROM source_capabilities WHERE agent_run_id=$1 AND revoked_at IS NULL`, current.AgentRun.ID).Scan(&activeCaps); err != nil || activeCaps != 0 {
		t.Fatalf("active caps=%d err=%v", activeCaps, err)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("model invocations=%d", len(fake.requests))
	}
	if fake.resolves != 1 || fake.requests[0].Provider != "openai" || fake.requests[0].Model != "gpt-5.6-sol" {
		t.Fatalf("provider was not pinned: resolves=%d request=%+v", fake.resolves, fake.requests[0])
	}
	var runProvider, attemptProvider string
	if err = s.db.QueryRow(ctx, `SELECT ar.provider,ara.provider FROM agent_runs ar JOIN agent_run_attempts ara ON ara.agent_run_id=ar.id WHERE ar.id=$1`, current.AgentRun.ID).Scan(&runProvider, &attemptProvider); err != nil || runProvider != "openai" || attemptProvider != "openai" {
		t.Fatalf("persisted providers run=%q attempt=%q err=%v", runProvider, attemptProvider, err)
	}
	if _, err = s.ListAgentSourceFiles(ctx, fake.requests[0].SourceCapability); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked capability accepted: %v", err)
	}
	var leaked bool
	if err = s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM audit_events WHERE metadata::text LIKE '%'||$1||'%' UNION ALL SELECT 1 FROM outbox_events WHERE payload::text LIKE '%'||$1||'%')`, fake.requests[0].SourceCapability).Scan(&leaked); err != nil || leaked {
		t.Fatalf("source capability leaked=%v err=%v", leaked, err)
	}
	if _, err = s.DecideApproval(ctx, actor, current.Approval.ID, "approved", "Reviewed evidence", "approve-planning"); err != nil {
		t.Fatal(err)
	}
	current, _ = s.TaskRun(ctx, actor, run.ID)
	if current.Status != "approved" || current.CurrentStage != "analysis_approval" || current.CompletedAt != nil {
		t.Fatalf("second checkpoint=%+v", current)
	}
	var pending int
	_ = s.db.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE completed_at IS NULL`).Scan(&pending)
	if pending != 0 {
		t.Fatalf("planning work dispatched: %d", pending)
	}
}

func TestAnalysisTenantIsolation(t *testing.T) {
	s, actor, repo, _ := orchestrationFixture(t)
	other, err := s.Register(context.Background(), registration("analysis-other@example.com", "Other Analysis Org"))
	if err != nil {
		t.Fatal(err)
	}
	task, _ := s.CreateTask(context.Background(), actor, repo.ID, "Tenant", "Objective", "req")
	run, _ := s.CreateTaskRun(context.Background(), actor, task.ID, "main", "req")
	if _, err = s.Analysis(context.Background(), other.Actor, run.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant analysis=%v", err)
	}
}

func TestAnalyzingCancellationBlocksExecution(t *testing.T) {
	s, actor, repo, _ := orchestrationFixture(t)
	fake := &fakeAnalysisClient{service: s}
	s.SetAgentClient(fake, AgentRuntimeConfig{SourceBrokerURL: "http://platform.internal", PromptVersion: "repository-analysis-v2", MaxTurns: 10, OverallTimeout: time.Minute})
	ctx := context.Background()
	task, _ := s.CreateTask(ctx, actor, repo.ID, "Cancel analysis", "Stop before the model call", "req")
	run, _ := s.CreateTaskRun(ctx, actor, task.ID, "main", "req")
	cfg := config.WorkerConfig{MaxAttempts: 5, BaseRetry: time.Millisecond, MaxRetry: time.Second}
	event, _, _ := s.ClaimOutbox(ctx, "worker", time.Minute)
	if err := s.ProcessOutbox(ctx, event, cfg); err != nil {
		t.Fatal(err)
	}
	current, _ := s.TaskRun(ctx, actor, run.ID)
	if _, err := s.DecideApproval(ctx, actor, current.Approval.ID, "approved", "", "approve"); err != nil {
		t.Fatal(err)
	}
	event, _, _ = s.ClaimOutbox(ctx, "worker", time.Minute)
	if err := s.ProcessOutbox(ctx, event, cfg); err != nil {
		t.Fatal(err)
	}
	current, _ = s.TaskRun(ctx, actor, run.ID)
	if current.Status != "analyzing" || current.AgentRun == nil {
		t.Fatalf("run=%+v", current)
	}
	if err := s.CancelTaskRun(ctx, actor, run.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	event, ok, err := s.ClaimOutbox(ctx, "worker", time.Minute)
	if err != nil || !ok || event.EventType != executeAgentEvent {
		t.Fatalf("event=%+v ok=%v err=%v", event, ok, err)
	}
	if err = s.ProcessOutbox(ctx, event, cfg); err != nil {
		t.Fatal(err)
	}
	current, _ = s.TaskRun(ctx, actor, run.ID)
	if current.Status != "cancelled" || current.AgentRun.Status != "cancelled" || len(fake.requests) != 0 {
		t.Fatalf("late execution occurred: run=%+v calls=%d", current, len(fake.requests))
	}
}
