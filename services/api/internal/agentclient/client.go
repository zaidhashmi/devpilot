package agentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var (
	ErrUnavailable              = errors.New("agent runtime unavailable")
	ErrRateLimited              = errors.New("model provider rate limited")
	ErrInvalidResult            = errors.New("invalid agent result")
	ErrOllamaUnavailable        = errors.New("ollama unavailable")
	ErrOllamaModelUnavailable   = errors.New("ollama model unavailable")
	ErrModelTimeout             = errors.New("model timeout")
	ErrModelInvalidOutput       = errors.New("model invalid output")
	ErrModelContextExhausted    = errors.New("model context exhausted")
	ErrModelMaxTurns            = errors.New("model max turns")
	ErrToolTimeout              = errors.New("tool timeout")
	ErrOutputGuardrail          = errors.New("output guardrail rejected")
	ErrProviderResponseInvalid  = errors.New("provider response invalid")
	ErrEvidenceSelectionInvalid = errors.New("evidence selection invalid")
	ErrEvidenceSelectionEmpty   = errors.New("evidence selection empty")
	ErrEvidenceBudgetExceeded   = errors.New("evidence budget exceeded")
	ErrSourceBrokerTimeout      = errors.New("source broker timeout")
	ErrSourceBrokerUnavailable  = errors.New("source broker unavailable")
	ErrSourceBrokerInvalid      = errors.New("source broker invalid response")
)

type RuntimeError struct {
	Cause       error
	Code        string
	Diagnostics map[string]any
}

func (e *RuntimeError) Error() string { return e.Code }
func (e *RuntimeError) Unwrap() error { return e.Cause }

type Evidence struct {
	SourceReferenceID string `json:"source_reference_id"`
}
type Finding struct {
	Category    string     `json:"category"`
	Severity    string     `json:"severity"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Confidence  string     `json:"confidence"`
	Evidence    []Evidence `json:"evidence"`
}
type ObjectiveAssessment struct {
	Answer               string   `json:"answer"`
	EvidenceReferenceIDs []string `json:"evidence_reference_ids"`
	Confidence           string   `json:"confidence"`
	Limitations          []string `json:"limitations"`
}
type Analysis struct {
	ObjectiveAssessment      ObjectiveAssessment `json:"objective_assessment"`
	Summary                  string              `json:"summary"`
	ArchitectureObservations []string            `json:"architecture_observations"`
	Findings                 []Finding           `json:"findings"`
	Risks                    []string            `json:"risks"`
	TestingObservations      []string            `json:"testing_observations"`
	SuggestedFocusAreas      []string            `json:"suggested_focus_areas"`
	Unknowns                 []string            `json:"unknowns"`
	Confidence               string              `json:"confidence"`
	Coverage                 *AnalysisCoverage   `json:"coverage,omitempty"`
}
type AnalysisCoverage struct {
	RepositoryIndexTruncated bool  `json:"repository_index_truncated"`
	ActualFileCount          int   `json:"actual_file_count"`
	FilesOffered             int   `json:"files_offered"`
	FilesRead                int   `json:"files_read"`
	SourceBytes              int64 `json:"source_bytes"`
}
type Usage struct {
	InputTokens     *int64 `json:"input_tokens"`
	OutputTokens    *int64 `json:"output_tokens"`
	ReasoningTokens *int64 `json:"reasoning_tokens"`
	ModelTurns      int    `json:"model_turns"`
	SourceToolCalls int    `json:"source_tool_calls"`
	SourceBytes     int64  `json:"source_bytes"`
}
type Request struct {
	Version               int            `json:"version"`
	Provider              string         `json:"provider"`
	AgentRunID            string         `json:"agent_run_id"`
	TaskRunID             string         `json:"task_run_id"`
	CommitSHA             string         `json:"commit_sha"`
	Objective             string         `json:"objective"`
	PlanContext           map[string]any `json:"plan_context"`
	InspectionMetadata    map[string]any `json:"inspection_metadata"`
	SourceBrokerURL       string         `json:"source_broker_url"`
	SourceCapability      string         `json:"source_capability"`
	PromptVersion         string         `json:"prompt_version"`
	Model                 string         `json:"model"`
	ReasoningEffort       *string        `json:"reasoning_effort"`
	MaxTurns              int            `json:"max_turns"`
	OverallTimeoutSeconds int            `json:"overall_timeout_seconds"`
}
type Result struct {
	Version            int              `json:"version"`
	Provider           string           `json:"provider"`
	AgentRunID         string           `json:"agent_run_id"`
	Status             string           `json:"status"`
	Analysis           Analysis         `json:"analysis"`
	Usage              Usage            `json:"usage"`
	TraceID            *string          `json:"trace_id"`
	Model              string           `json:"model"`
	PromptVersion      string           `json:"prompt_version"`
	FailureCode        string           `json:"failure_code,omitempty"`
	ProviderResponseID string           `json:"provider_response_id,omitempty"`
	Diagnostics        map[string]any   `json:"diagnostics,omitempty"`
	ModelCalls         []map[string]any `json:"model_calls,omitempty"`
}

type Client interface {
	ResolveProvider(context.Context) (ProviderSelection, error)
	Analyze(context.Context, Request) (Result, error)
	Cancel(context.Context, string) error
}

type ProviderSelection struct {
	Provider        string  `json:"provider"`
	Model           string  `json:"model"`
	ReasoningEffort *string `json:"reasoning_effort"`
}

type HTTPClient struct {
	baseURL, token string
	http           *http.Client
}

func New(baseURL, token string, timeout time.Duration) *HTTPClient {
	return &HTTPClient{baseURL: strings.TrimRight(baseURL, "/"), token: token, http: &http.Client{Timeout: timeout}}
}
func (c *HTTPClient) ResolveProvider(ctx context.Context) (ProviderSelection, error) {
	var out ProviderSelection
	err := c.do(ctx, http.MethodGet, "/internal/v1/provider", nil, &out)
	return out, err
}
func (c *HTTPClient) Analyze(ctx context.Context, input Request) (Result, error) {
	var out Result
	err := c.do(ctx, http.MethodPost, "/internal/v1/agent-runs", input, &out)
	return out, err
}
func (c *HTTPClient) Cancel(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/internal/v1/agent-runs/"+id+"/cancel", map[string]any{}, nil)
}
func (c *HTTPClient) do(ctx context.Context, method, path string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: transport", ErrUnavailable)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return ErrRateLimited
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiError struct {
			Error       string         `json:"error"`
			Diagnostics map[string]any `json:"diagnostics"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&apiError)
		if apiError.Error == "ollama_unavailable" {
			return ErrOllamaUnavailable
		}
		if apiError.Error == "ollama_model_unavailable" {
			return ErrOllamaModelUnavailable
		}
		classified := map[string]error{
			"model_timeout":                  ErrModelTimeout,
			"model_invalid_output":           ErrModelInvalidOutput,
			"model_context_exhausted":        ErrModelContextExhausted,
			"model_max_turns":                ErrModelMaxTurns,
			"tool_timeout":                   ErrToolTimeout,
			"output_guardrail_rejected":      ErrOutputGuardrail,
			"provider_response_invalid":      ErrProviderResponseInvalid,
			"evidence_selection_invalid":     ErrEvidenceSelectionInvalid,
			"evidence_selection_empty":       ErrEvidenceSelectionEmpty,
			"evidence_budget_exceeded":       ErrEvidenceBudgetExceeded,
			"source_broker_timeout":          ErrSourceBrokerTimeout,
			"source_broker_unavailable":      ErrSourceBrokerUnavailable,
			"source_broker_invalid_response": ErrSourceBrokerInvalid,
		}
		if cause := classified[apiError.Error]; cause != nil {
			return &RuntimeError{Cause: cause, Code: apiError.Error, Diagnostics: apiError.Diagnostics}
		}
		if resp.StatusCode >= 500 {
			return ErrUnavailable
		}
		return ErrInvalidResult
	}
	if output != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(output); err != nil {
			return ErrInvalidResult
		}
	}
	return nil
}
