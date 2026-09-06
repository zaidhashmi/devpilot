package runner

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var ErrUnavailable = errors.New("workspace runner unavailable")

type Limits struct {
	MaxArchiveBytes           int64 `json:"max_archive_bytes"`
	MaxExpandedBytes          int64 `json:"max_expanded_bytes"`
	MaxFileBytes              int64 `json:"max_file_bytes"`
	MaxFiles                  int   `json:"max_files"`
	MaxDepth                  int   `json:"max_depth"`
	AcquisitionTimeoutSeconds int   `json:"acquisition_timeout_seconds"`
	InspectionTimeoutSeconds  int   `json:"inspection_timeout_seconds"`
	OverallTimeoutSeconds     int   `json:"overall_timeout_seconds"`
}

type Request struct {
	WorkspaceID string `json:"workspace_id"`
	Repository  string `json:"repository"`
	CommitSHA   string `json:"commit_sha"`
	ArchiveURL  string `json:"archive_url"`
	Limits      Limits `json:"limits"`
}

type Artifact struct {
	Repository             string         `json:"repository"`
	CommitSHA              string         `json:"commit_sha"`
	RegularFiles           int            `json:"regular_file_count"`
	TotalBytes             int64          `json:"total_bytes"`
	Directories            int            `json:"directory_count"`
	MaximumDepth           int            `json:"maximum_depth"`
	Extensions             map[string]int `json:"extension_distribution"`
	Manifests              []string       `json:"manifests"`
	Ecosystems             []string       `json:"ecosystems"`
	CIConfiguration        bool           `json:"ci_configuration_present"`
	ContainerConfiguration bool           `json:"container_configuration_present"`
	Gitmodules             bool           `json:"gitmodules_present"`
	Symlinks               int            `json:"symlink_count"`
	SuspiciousEntries      int            `json:"suspicious_entry_count"`
	LargeFiles             int            `json:"large_file_count"`
	Truncated              bool           `json:"truncated"`
	DurationMilliseconds   int64          `json:"inspection_duration_ms"`
	SourceTrust            string         `json:"source_trust"`
}

type Result struct {
	WorkspaceID string    `json:"workspace_id"`
	Status      string    `json:"status"`
	Artifact    *Artifact `json:"inspection_artifact,omitempty"`
	FailureCode string    `json:"failure_code,omitempty"`
}

type Client interface {
	Inspect(context.Context, Request) (Result, error)
	Cancel(context.Context, string) error
}

type HTTPClient struct {
	baseURL, secret string
	http            *http.Client
}

func New(baseURL, secret string, timeout time.Duration) *HTTPClient {
	return &HTTPClient{baseURL: strings.TrimRight(baseURL, "/"), secret: secret, http: &http.Client{Timeout: timeout}}
}

func (c *HTTPClient) Inspect(ctx context.Context, input Request) (Result, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return Result{}, err
	}
	var result Result
	if err := c.do(ctx, http.MethodPost, "/internal/v1/inspections", body, &result); err != nil {
		return Result{}, err
	}
	return result, nil
}

func (c *HTTPClient) Cancel(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/internal/v1/inspections/"+id+"/cancel", []byte(`{}`), nil)
}

func (c *HTTPClient) do(ctx context.Context, method, path string, body []byte, output any) error {
	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(c.secret))
	mac.Write([]byte(timestamp + "\n" + method + "\n" + path + "\n"))
	mac.Write(body)
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DevPilot-Timestamp", timestamp)
	req.Header.Set("X-DevPilot-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: request failed", ErrUnavailable)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}
	if output != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(output); err != nil {
			return fmt.Errorf("%w: invalid response", ErrUnavailable)
		}
	}
	return nil
}
