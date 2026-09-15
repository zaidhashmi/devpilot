package platform

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/devpilot/devpilot/services/api/internal/agentclient"
	"github.com/devpilot/devpilot/services/api/internal/authz"
	"github.com/devpilot/devpilot/services/api/internal/config"
	"github.com/devpilot/devpilot/services/api/internal/githubapp"
	"github.com/jackc/pgx/v5"
)

const (
	startAnalysisEvent    = "task_run.start_analysis.v1"
	executeAgentEvent     = "agent_run.execute.v1"
	maxSourceFiles        = 30
	maxSourceBytes        = 512 << 10
	maxSourceFileBytes    = 256 << 10
	maxSourceExcerptBytes = 32 << 10
	maxSourceLines        = 250
	maxFileIndex          = 10000
)

type AgentRuntimeConfig struct {
	SourceBrokerURL, PromptVersion string
	MaxTurns                       int
	OverallTimeout                 time.Duration
	OllamaOverallTimeout           time.Duration
}

func AgentRuntimeConfigFrom(c config.AgentConfig) AgentRuntimeConfig {
	return AgentRuntimeConfig{SourceBrokerURL: c.SourceBrokerURL, PromptVersion: c.PromptVersion, MaxTurns: c.MaxTurns, OverallTimeout: c.OverallTimeout, OllamaOverallTimeout: c.OllamaOverallTimeout}
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|token|secret|password)\s*[:=]\s*["']?([A-Za-z0-9_\-]{16,})`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`),
	regexp.MustCompile(`\bAKIA[A-Z0-9]{16}\b`),
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
}

func (s *Service) processStartAnalysis(ctx context.Context, runID, organizationID string) error {
	if s.agent == nil {
		return ErrUnavailable
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var status, stage, planID string
	err = tx.QueryRow(ctx, `SELECT tr.status,tr.current_stage,p.id FROM task_runs tr JOIN task_plan_revisions p ON p.task_run_id=tr.id AND p.revision_number=1 WHERE tr.id=$1 AND tr.organization_id=$2 FOR UPDATE`, runID, organizationID).Scan(&status, &stage, &planID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status == "cancelled" || status == "rejected" || status == "failed" || status == "timed_out" {
		return nil
	}
	var agentID string
	err = tx.QueryRow(ctx, `SELECT id FROM agent_runs WHERE task_run_id=$1 AND agent_type='repository_analysis'`, runID).Scan(&agentID)
	if errors.Is(err, pgx.ErrNoRows) {
		selection, resolveErr := s.agent.ResolveProvider(ctx)
		if resolveErr != nil {
			return resolveErr
		}
		if selection.Provider != "openai" && selection.Provider != "ollama" || selection.Model == "" {
			return agentclient.ErrInvalidResult
		}
		if status != "approved" || stage != "approval" {
			return ErrConflict
		}
		agentID = newID().String()
		_, err = tx.Exec(ctx, `INSERT INTO agent_runs(id,organization_id,task_run_id,agent_type,input_plan_revision_id,status,provider,model,reasoning_effort,prompt_version) VALUES($1,$2,$3,'repository_analysis',$4,'queued',$5,$6,$7,$8)`, agentID, organizationID, runID, planID, selection.Provider, selection.Model, selection.ReasoningEffort, s.agentConfig.PromptVersion)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE task_runs SET status='analyzing',current_stage='analysis' WHERE id=$1 AND status='approved'`, runID)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO outbox_events(id,organization_id,aggregate_type,aggregate_id,event_type,payload) VALUES($1,$2,'agent_run',$3,$4,$5) ON CONFLICT(event_type,aggregate_id) DO NOTHING`, newID(), organizationID, agentID, executeAgentEvent, mustJSON(map[string]any{"version": 1, "agent_run_id": agentID, "task_run_id": runID}))
		}
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,organization_id,actor_user_id,event_type,resource_type,resource_id,request_id,metadata) VALUES($1,$2,NULL,'agent_run.created','agent_run',$3,'worker',$4)`, newID(), organizationID, agentID, mustJSON(map[string]any{"provider": selection.Provider, "model": selection.Model, "prompt_version": s.agentConfig.PromptVersion}))
		}
	} else if err != nil {
		return err
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) processExecuteAgent(ctx context.Context, agentRunID, organizationID string) error {
	if s.agent == nil {
		return ErrUnavailable
	}
	var runID, status, taskStatus, objective, provider, model, promptVersion, repoID, sha string
	var reasoning *string
	var planRaw, inspectionRaw []byte
	err := s.db.QueryRow(ctx, `SELECT ar.task_run_id,ar.status,tr.status,t.objective,ar.provider,ar.model,ar.reasoning_effort,ar.prompt_version,tr.repository_id,tr.resolved_commit_sha,p.structured_payload,w.inspection_artifact FROM agent_runs ar JOIN task_runs tr ON tr.id=ar.task_run_id JOIN engineering_tasks t ON t.id=tr.engineering_task_id JOIN task_plan_revisions p ON p.id=ar.input_plan_revision_id JOIN workspaces w ON w.id=tr.workspace_id WHERE ar.id=$1 AND ar.organization_id=$2`, agentRunID, organizationID).Scan(&runID, &status, &taskStatus, &objective, &provider, &model, &reasoning, &promptVersion, &repoID, &sha, &planRaw, &inspectionRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status == "succeeded" || status == "cancelled" || taskStatus == "cancelled" {
		return nil
	}
	overallTimeout := s.agentConfig.OverallTimeout
	if provider == "ollama" && s.agentConfig.OllamaOverallTimeout > 0 {
		overallTimeout = s.agentConfig.OllamaOverallTimeout
	}
	rawToken, tokenHash, err := newSourceCapabilityToken()
	if err != nil {
		return err
	}
	capID, attemptID := newID(), newID()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var attempt int
	err = tx.QueryRow(ctx, `SELECT COALESCE(max(attempt_number),0)+1 FROM agent_run_attempts WHERE agent_run_id=$1`, agentRunID).Scan(&attempt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE source_capabilities SET revoked_at=COALESCE(revoked_at,now()) WHERE agent_run_id=$1 AND revoked_at IS NULL`, agentRunID)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO source_capabilities(id,token_hash,organization_id,agent_run_id,task_run_id,repository_id,commit_sha,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, capID, tokenHash, organizationID, agentRunID, runID, repoID, sha, s.now().UTC().Add(overallTimeout))
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO agent_run_attempts(id,agent_run_id,attempt_number,status,provider,model) VALUES($1,$2,$3,'running',$4,$5)`, attemptID, agentRunID, attempt, provider, model)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE agent_runs SET status='running',started_at=COALESCE(started_at,now()) WHERE id=$1 AND status IN ('queued','running','failed')`, agentRunID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,organization_id,actor_user_id,event_type,resource_type,resource_id,request_id,metadata) VALUES($1,$2,NULL,'agent_run.started','agent_run',$3,'worker',$4)`, newID(), organizationID, agentRunID, mustJSON(map[string]any{"attempt": attempt, "provider": provider, "model": model}))
	}
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	defer s.revokeCapability(context.Background(), capID.String())
	var plan, inspection map[string]any
	_ = json.Unmarshal(planRaw, &plan)
	_ = json.Unmarshal(inspectionRaw, &inspection)
	runCtx, cancel := context.WithTimeout(ctx, overallTimeout)
	defer cancel()
	result, callErr := s.agent.Analyze(runCtx, agentclient.Request{Version: 1, Provider: provider, AgentRunID: agentRunID, TaskRunID: runID, CommitSHA: sha, Objective: objective, PlanContext: plan, InspectionMetadata: inspection, SourceBrokerURL: s.agentConfig.SourceBrokerURL, SourceCapability: rawToken, PromptVersion: promptVersion, Model: model, ReasoningEffort: reasoning, MaxTurns: s.agentConfig.MaxTurns, OverallTimeoutSeconds: int(overallTimeout.Seconds())})
	if callErr != nil {
		if errors.Is(callErr, context.DeadlineExceeded) || errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return s.timeoutAgentRun(ctx, organizationID, runID, agentRunID, attemptID.String())
		}
		return s.recordAgentAttemptFailure(ctx, agentRunID, attemptID.String(), callErr)
	}
	if result.AgentRunID != agentRunID || result.Version != 1 || result.Provider != provider || result.Model != model || result.PromptVersion != promptVersion || result.Status != "succeeded" {
		return s.recordAgentAttemptFailure(ctx, agentRunID, attemptID.String(), agentclient.ErrInvalidResult)
	}
	return s.publishAnalysis(ctx, organizationID, runID, agentRunID, attemptID.String(), result)
}

func (s *Service) timeoutAgentRun(ctx context.Context, organizationID, runID, agentRunID, attemptID string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	_, err = tx.Exec(ctx, `UPDATE agent_run_attempts SET status='timed_out',failure_code='agent_run_timeout',completed_at=now() WHERE id=$1`, attemptID)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE agent_runs SET status='timed_out',failure_code='agent_run_timeout',completed_at=now() WHERE id=$1 AND status='running'`, agentRunID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE task_runs SET status='timed_out',current_stage='finished',failure_code='agent_run_timeout',completed_at=now() WHERE id=$1 AND status='analyzing'`, runID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE source_capabilities SET revoked_at=COALESCE(revoked_at,now()) WHERE agent_run_id=$1`, agentRunID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,organization_id,actor_user_id,event_type,resource_type,resource_id,request_id,metadata) VALUES($1,$2,NULL,'agent_run.timed_out','agent_run',$3,'worker','{}')`, newID(), organizationID, agentRunID)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) recordAgentAttemptFailure(ctx context.Context, agentRunID, attemptID string, cause error) error {
	code := "agent_runtime_unavailable"
	if errors.Is(cause, agentclient.ErrRateLimited) {
		code = "openai_rate_limited"
	}
	if errors.Is(cause, agentclient.ErrInvalidResult) {
		code = "invalid_agent_result"
	}
	if errors.Is(cause, agentclient.ErrOllamaUnavailable) {
		code = "ollama_unavailable"
	}
	if errors.Is(cause, agentclient.ErrOllamaModelUnavailable) {
		code = "ollama_model_unavailable"
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		code = "agent_run_timeout"
	}
	if errors.Is(cause, agentclient.ErrModelTimeout) {
		code = "model_timeout"
	}
	if errors.Is(cause, agentclient.ErrModelInvalidOutput) {
		code = "model_invalid_output"
	}
	if errors.Is(cause, agentclient.ErrModelContextExhausted) {
		code = "model_context_exhausted"
	}
	if errors.Is(cause, agentclient.ErrModelMaxTurns) {
		code = "model_max_turns"
	}
	if errors.Is(cause, agentclient.ErrToolTimeout) {
		code = "tool_timeout"
	}
	if errors.Is(cause, agentclient.ErrOutputGuardrail) {
		code = "output_guardrail_rejected"
	}
	if errors.Is(cause, agentclient.ErrProviderResponseInvalid) {
		code = "provider_response_invalid"
	}
	if errors.Is(cause, agentclient.ErrEvidenceSelectionInvalid) {
		code = "evidence_selection_invalid"
	}
	if errors.Is(cause, agentclient.ErrEvidenceSelectionEmpty) {
		code = "evidence_selection_empty"
	}
	if errors.Is(cause, agentclient.ErrEvidenceBudgetExceeded) {
		code = "evidence_budget_exceeded"
	}
	if errors.Is(cause, agentclient.ErrSourceBrokerTimeout) {
		code = "source_broker_timeout"
	}
	if errors.Is(cause, agentclient.ErrSourceBrokerUnavailable) {
		code = "source_broker_unavailable"
	}
	if errors.Is(cause, agentclient.ErrSourceBrokerInvalid) {
		code = "source_broker_invalid_response"
	}
	diagnostics := map[string]any{}
	var runtimeError *agentclient.RuntimeError
	if errors.As(cause, &runtimeError) {
		diagnostics = runtimeError.Diagnostics
	}
	diagnosticJSON, _ := json.Marshal(diagnostics)
	_, _ = s.db.Exec(ctx, `UPDATE agent_run_attempts SET status='failed',failure_code=$2,provider_diagnostics=$3,completed_at=now() WHERE id=$1`, attemptID, code, diagnosticJSON)
	if code == "model_timeout" {
		return cause
	}
	if code == "invalid_agent_result" || code == "ollama_model_unavailable" || code == "model_invalid_output" || code == "model_context_exhausted" || code == "model_max_turns" || code == "output_guardrail_rejected" || code == "provider_response_invalid" || code == "evidence_selection_invalid" || code == "evidence_selection_empty" || code == "evidence_budget_exceeded" || code == "source_broker_invalid_response" {
		_, _ = s.db.Exec(ctx, `UPDATE agent_runs SET status='failed',failure_code=$2,completed_at=now() WHERE id=$1`, agentRunID, code)
		return cause
	}
	return fmt.Errorf("%w: %s", ErrUnavailable, code)
}

func (s *Service) publishAnalysis(ctx context.Context, organizationID, runID, agentRunID, attemptID string, result agentclient.Result) error {
	if err := validateAnalysis(result.Analysis); err != nil {
		return s.recordAgentAttemptFailure(ctx, agentRunID, attemptID, agentclient.ErrInvalidResult)
	}
	refs := map[string]bool{}
	for _, referenceID := range result.Analysis.ObjectiveAssessment.EvidenceReferenceIDs {
		refs[referenceID] = true
	}
	for _, f := range result.Analysis.Findings {
		for _, e := range f.Evidence {
			if refs[e.SourceReferenceID] {
				continue
			}
			refs[e.SourceReferenceID] = true
		}
	}
	for referenceID := range refs {
		var ok bool
		err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_source_accesses asa JOIN task_runs tr ON tr.id=asa.task_run_id WHERE asa.id=$1 AND asa.agent_run_id=$2 AND asa.task_run_id=$3 AND asa.organization_id=$4 AND asa.repository_id=tr.repository_id AND asa.commit_sha=tr.resolved_commit_sha)`, referenceID, agentRunID, runID, organizationID).Scan(&ok)
		if err != nil {
			return err
		}
		if !ok {
			return s.recordAgentAttemptFailure(ctx, agentRunID, attemptID, agentclient.ErrInvalidResult)
		}
	}
	raw, err := json.Marshal(result.Analysis)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM task_runs WHERE id=$1 AND organization_id=$2 FOR UPDATE`, runID, organizationID).Scan(&status)
	if err != nil {
		return err
	}
	if status == "cancelled" {
		return nil
	}
	if status != "analyzing" {
		return ErrConflict
	}
	revisionID := newID()
	_, err = tx.Exec(ctx, `INSERT INTO repository_analysis_revisions(id,organization_id,task_run_id,agent_run_id,revision_number,source,provider,model,reasoning_effort,prompt_version,structured_payload) SELECT $1,$2,$3,$4,1,'repository_analysis_agent',provider,model,reasoning_effort,$5,$6 FROM agent_runs WHERE id=$4 ON CONFLICT(agent_run_id) DO NOTHING`, revisionID, organizationID, runID, agentRunID, result.PromptVersion, raw)
	if err == nil {
		err = tx.QueryRow(ctx, `SELECT id FROM repository_analysis_revisions WHERE agent_run_id=$1`, agentRunID).Scan(&revisionID)
	}
	approvalID := newID()
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO approvals(id,organization_id,task_run_id,analysis_revision_id,approval_type,revision,status) VALUES($1,$2,$3,$4,'proceed_to_planning',1,'pending') ON CONFLICT(task_run_id,approval_type,revision) DO NOTHING`, approvalID, organizationID, runID, revisionID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE task_runs SET status='awaiting_approval',current_stage='analysis_approval' WHERE id=$1 AND status='analyzing'`, runID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE agent_runs SET status='succeeded',trace_id=$2,failure_code=NULL,completed_at=now() WHERE id=$1`, agentRunID, result.TraceID)
	}
	if err == nil {
		attemptDiagnostics := map[string]any{"final": result.Diagnostics, "model_calls": result.ModelCalls}
		diagnostics, marshalErr := json.Marshal(attemptDiagnostics)
		if marshalErr != nil {
			return marshalErr
		}
		_, err = tx.Exec(ctx, `UPDATE agent_run_attempts SET status='succeeded',input_tokens=$2,output_tokens=$3,reasoning_tokens=$4,model_turns=$5,source_tool_calls=$6,source_bytes=$7,provider_response_id=NULLIF($8,''),provider_diagnostics=$9,completed_at=now() WHERE id=$1`, attemptID, result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.ReasoningTokens, result.Usage.ModelTurns, result.Usage.SourceToolCalls, result.Usage.SourceBytes, result.ProviderResponseID, diagnostics)
	}
	for _, ev := range []struct {
		typ, res, id string
		meta         map[string]any
	}{{"agent_run.succeeded", "agent_run", agentRunID, map[string]any{"provider": result.Provider, "model": result.Model, "prompt_version": result.PromptVersion, "input_tokens": result.Usage.InputTokens, "output_tokens": result.Usage.OutputTokens}}, {"repository_analysis.created", "repository_analysis", revisionID.String(), map[string]any{"finding_count": len(result.Analysis.Findings)}}, {"task_run.awaiting_approval", "task_run", runID, map[string]any{"stage": "analysis_approval"}}, {"approval.requested", "approval", approvalID.String(), map[string]any{"analysis_revision_id": revisionID}}} {
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,organization_id,actor_user_id,event_type,resource_type,resource_id,request_id,metadata) VALUES($1,$2,NULL,$3,$4,$5,'worker',$6)`, newID(), organizationID, ev.typ, ev.res, ev.id, mustJSON(ev.meta))
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func validateAnalysis(a agentclient.Analysis) error {
	if len(a.ObjectiveAssessment.Answer) < 1 || len(a.ObjectiveAssessment.Answer) > 4000 || len(a.ObjectiveAssessment.EvidenceReferenceIDs) < 1 || len(a.ObjectiveAssessment.EvidenceReferenceIDs) > 10 || len(a.ObjectiveAssessment.Limitations) > 10 || len(a.Summary) < 1 || len(a.Summary) > 4000 || len(a.Findings) > 30 || len(a.ArchitectureObservations) > 20 || len(a.Risks) > 20 || len(a.TestingObservations) > 20 || len(a.SuggestedFocusAreas) > 20 || len(a.Unknowns) > 20 {
		return ErrInvalidInput
	}
	known := map[string]bool{"architecture": true, "correctness": true, "reliability": true, "security": true, "maintainability": true, "testing": true}
	sev := map[string]bool{"info": true, "low": true, "medium": true, "high": true, "critical": true}
	conf := map[string]bool{"low": true, "medium": true, "high": true}
	if !conf[a.Confidence] || !conf[a.ObjectiveAssessment.Confidence] {
		return ErrInvalidInput
	}
	objectiveRefs := map[string]bool{}
	for _, referenceID := range a.ObjectiveAssessment.EvidenceReferenceIDs {
		if len(referenceID) != 36 || objectiveRefs[referenceID] {
			return ErrInvalidInput
		}
		objectiveRefs[referenceID] = true
	}
	for _, limitation := range a.ObjectiveAssessment.Limitations {
		if len(limitation) < 1 || len(limitation) > 500 {
			return ErrInvalidInput
		}
	}
	for _, f := range a.Findings {
		if !known[f.Category] || !sev[f.Severity] || !conf[f.Confidence] || len(f.Title) < 1 || len(f.Title) > 200 || len(f.Description) > 3000 || len(f.Evidence) > 10 {
			return ErrInvalidInput
		}
		if (f.Category == "correctness" || f.Category == "security" || f.Category == "reliability" || f.Severity == "high" || f.Severity == "critical") && len(f.Evidence) == 0 {
			return ErrInvalidInput
		}
	}
	return nil
}

func newSourceCapabilityToken() (string, []byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(raw))
	return raw, h[:], nil
}
func (s *Service) revokeCapability(ctx context.Context, id string) {
	_, _ = s.db.Exec(ctx, `UPDATE source_capabilities SET revoked_at=COALESCE(revoked_at,now()) WHERE id=$1`, id)
}

func (s *Service) sourceContext(ctx context.Context, token string) (capID, organizationID, agentRunID, taskRunID, repositoryID, sha, owner, name string, installationID, githubRepoID int64, files int, bytes int64, err error) {
	h := sha256.Sum256([]byte(token))
	err = s.db.QueryRow(ctx, `SELECT sc.id,sc.organization_id,sc.agent_run_id,sc.task_run_id,sc.repository_id,sc.commit_sha,r.owner,r.name,i.github_installation_id,r.github_repository_id,sc.files_accessed,sc.bytes_returned FROM source_capabilities sc JOIN agent_runs ar ON ar.id=sc.agent_run_id JOIN repositories r ON r.id=sc.repository_id JOIN github_installations i ON i.id=r.github_installation_id WHERE sc.token_hash=$1 AND sc.revoked_at IS NULL AND sc.expires_at>now() AND ar.status='running' AND i.status='active'`, h[:]).Scan(&capID, &organizationID, &agentRunID, &taskRunID, &repositoryID, &sha, &owner, &name, &installationID, &githubRepoID, &files, &bytes)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrForbidden
	}
	return
}

func (s *Service) ListAgentSourceFiles(ctx context.Context, token string) ([]SourceFile, error) {
	index, err := s.ListAgentSourceIndex(ctx, token)
	return index.Files, err
}

func (s *Service) ListAgentSourceIndex(ctx context.Context, token string) (SourceFileIndex, error) {
	_, _, _, _, _, sha, owner, name, installationID, githubRepoID, _, _, err := s.sourceContext(ctx, token)
	if err != nil {
		return SourceFileIndex{}, err
	}
	content, ok := s.github.(githubapp.ContentClient)
	if !ok {
		return SourceFileIndex{}, ErrUnavailable
	}
	items, err := content.ListFiles(ctx, installationID, githubRepoID, owner, name, sha)
	if err != nil {
		return SourceFileIndex{}, err
	}
	if len(items) > maxFileIndex {
		return SourceFileIndex{}, ErrInvalidInput
	}
	result := make([]SourceFile, 0, len(items))
	for _, f := range items {
		if !sensitivePath(f.Path) && f.Size <= maxSourceFileBytes {
			result = append(result, SourceFile{Path: f.Path, Size: f.Size})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return SourceFileIndex{Files: result, ActualFileCount: len(items)}, nil
}

func (s *Service) ReadAgentSource(ctx context.Context, token, filePath string, startLine, endLine, requestedMaxBytes int) (SourceExcerpt, error) {
	capID, orgID, agentID, taskID, repoID, sha, owner, name, installationID, githubRepoID, files, total, err := s.sourceContext(ctx, token)
	if err != nil {
		return SourceExcerpt{}, err
	}
	filePath = path.Clean(strings.TrimSpace(filePath))
	if requestedMaxBytes == 0 {
		requestedMaxBytes = maxSourceExcerptBytes
	}
	if filePath == "." || strings.HasPrefix(filePath, "../") || path.IsAbs(filePath) || sensitivePath(filePath) || startLine < 1 || endLine < startLine || endLine-startLine+1 > maxSourceLines || requestedMaxBytes < 1 || requestedMaxBytes > maxSourceExcerptBytes || files >= maxSourceFiles {
		return SourceExcerpt{}, ErrInvalidInput
	}
	content, ok := s.github.(githubapp.ContentClient)
	if !ok {
		return SourceExcerpt{}, ErrUnavailable
	}
	data, err := content.ReadFile(ctx, installationID, githubRepoID, owner, name, sha, filePath)
	if err != nil {
		return SourceExcerpt{}, err
	}
	if len(data) > maxSourceFileBytes || !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
		return SourceExcerpt{}, ErrInvalidInput
	}
	lines := strings.Split(string(data), "\n")
	if startLine > len(lines) {
		return SourceExcerpt{}, ErrInvalidInput
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}
	requestedEndLine := endLine
	excerpt, actualEndLine := boundedExcerpt(lines[startLine-1:endLine], startLine, requestedMaxBytes)
	truncated := actualEndLine < requestedEndLine
	if excerpt == "" || len(excerpt) > maxSourceExcerptBytes || total+int64(len(excerpt)) > maxSourceBytes {
		return SourceExcerpt{}, ErrInvalidInput
	}
	endLine = actualEndLine
	redactions := 0
	for _, re := range secretPatterns {
		excerpt = re.ReplaceAllStringFunc(excerpt, func(match string) string {
			redactions++
			parts := strings.SplitN(match, "=", 2)
			if len(parts) == 2 {
				return parts[0] + "=[REDACTED_SECRET]"
			}
			parts = strings.SplitN(match, ":", 2)
			if len(parts) == 2 {
				return parts[0] + ": [REDACTED_SECRET]"
			}
			return "[REDACTED_SECRET]"
		})
	}
	if len(excerpt) > requestedMaxBytes {
		excerpt = truncateUTF8(excerpt, requestedMaxBytes)
		truncated = true
	}
	digest := sha256.Sum256([]byte(excerpt))
	accessID := newID()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return SourceExcerpt{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var persistedID string
	err = tx.QueryRow(ctx, `INSERT INTO agent_source_accesses(id,organization_id,agent_run_id,task_run_id,repository_id,commit_sha,path,start_line,end_line,bytes_returned,redaction_count,content_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(agent_run_id,path,start_line,end_line) DO NOTHING RETURNING id`, accessID, orgID, agentID, taskID, repoID, sha, filePath, startLine, endLine, len(excerpt), redactions, fmt.Sprintf("%x", digest[:])).Scan(&persistedID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id FROM agent_source_accesses WHERE agent_run_id=$1 AND path=$2 AND start_line=$3 AND end_line=$4`, agentID, filePath, startLine, endLine).Scan(&persistedID)
		if err != nil {
			return SourceExcerpt{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return SourceExcerpt{}, err
		}
		return SourceExcerpt{persistedID, filePath, sha, startLine, endLine, excerpt, redactions, len(excerpt), truncated}, nil
	}
	if err != nil {
		return SourceExcerpt{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE source_capabilities SET files_accessed=files_accessed+1,bytes_returned=bytes_returned+$2 WHERE id=$1 AND revoked_at IS NULL AND files_accessed<$3 AND bytes_returned+$2<=$4`, capID, len(excerpt), maxSourceFiles, maxSourceBytes)
	if err != nil || tag.RowsAffected() != 1 {
		if err == nil {
			err = ErrInvalidInput
		}
		return SourceExcerpt{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return SourceExcerpt{}, err
	}
	return SourceExcerpt{accessID.String(), filePath, sha, startLine, endLine, excerpt, redactions, len(excerpt), truncated}, nil
}

func boundedExcerpt(lines []string, startLine, maxBytes int) (string, int) {
	excerpt := ""
	actualEndLine := startLine - 1
	for i, line := range lines {
		candidate := line
		if excerpt != "" {
			candidate = excerpt + "\n" + line
		}
		if len(candidate) > maxBytes {
			if excerpt == "" {
				excerpt = truncateUTF8(line, maxBytes)
				if excerpt != "" {
					actualEndLine = startLine + i
				}
			}
			break
		}
		excerpt = candidate
		actualEndLine = startLine + i
	}
	return excerpt, actualEndLine
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

func sensitivePath(p string) bool {
	lower := strings.ToLower(path.Base(p))
	if lower == ".env" || strings.HasPrefix(lower, ".env.") || strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".p12") || strings.HasSuffix(lower, ".pfx") || lower == "id_rsa" || lower == "id_ed25519" || strings.Contains(lower, "private_key") {
		return true
	}
	return false
}

func (s *Service) AgentRun(ctx context.Context, actor Actor, id string) (AgentRun, error) {
	if !authz.Allowed(actor.Membership.Role, authz.TaskRunsRead) {
		return AgentRun{}, ErrForbidden
	}
	var x AgentRun
	err := s.db.QueryRow(ctx, `SELECT id,task_run_id,agent_type,status,provider,model,reasoning_effort,prompt_version,trace_id,failure_code,created_at,started_at,completed_at FROM agent_runs WHERE id=$1 AND organization_id=$2`, id, actor.Organization.ID).Scan(&x.ID, &x.TaskRunID, &x.AgentType, &x.Status, &x.Provider, &x.Model, &x.ReasoningEffort, &x.PromptVersion, &x.TraceID, &x.FailureCode, &x.CreatedAt, &x.StartedAt, &x.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return x, ErrNotFound
	}
	return x, err
}

func (s *Service) Analysis(ctx context.Context, actor Actor, runID string) (AnalysisRevision, error) {
	if !authz.Allowed(actor.Membership.Role, authz.TaskRunsRead) {
		return AnalysisRevision{}, ErrForbidden
	}
	var x AnalysisRevision
	var raw []byte
	err := s.db.QueryRow(ctx, `SELECT id,task_run_id,agent_run_id,revision_number,source,provider,model,reasoning_effort,prompt_version,structured_payload,created_at FROM repository_analysis_revisions WHERE task_run_id=$1 AND organization_id=$2 ORDER BY revision_number DESC LIMIT 1`, runID, actor.Organization.ID).Scan(&x.ID, &x.TaskRunID, &x.AgentRunID, &x.RevisionNumber, &x.Source, &x.Provider, &x.Model, &x.ReasoningEffort, &x.PromptVersion, &raw, &x.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return x, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(raw, &x.Payload)
	}
	if err == nil {
		rows, queryErr := s.db.Query(ctx, `SELECT id,path,commit_sha,start_line,end_line,redaction_count,COALESCE(content_digest,'') FROM agent_source_accesses WHERE agent_run_id=$1 AND organization_id=$2 ORDER BY created_at`, x.AgentRunID, actor.Organization.ID)
		if queryErr != nil {
			return x, queryErr
		}
		defer rows.Close()
		x.Evidence = []SourceAccess{}
		for rows.Next() {
			var item SourceAccess
			if scanErr := rows.Scan(&item.ID, &item.Path, &item.CommitSHA, &item.StartLine, &item.EndLine, &item.RedactionCount, &item.ContentDigest); scanErr != nil {
				return x, scanErr
			}
			x.Evidence = append(x.Evidence, item)
		}
		err = rows.Err()
	}
	return x, err
}
