package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	securearchive "github.com/devpilot/devpilot/services/workspace-runner/internal/archive"
	"github.com/devpilot/devpilot/services/workspace-runner/internal/model"
	"github.com/devpilot/devpilot/services/workspace-runner/internal/sandbox"
)

type Server struct {
	secret   string
	provider sandbox.Provider
	client   *http.Client
	mu       sync.Mutex
	cancels  map[string]context.CancelFunc
}

func New(secret string, p sandbox.Provider) http.Handler {
	return NewWithHTTPClient(secret, p, &http.Client{Timeout: 2 * time.Minute})
}
func NewWithHTTPClient(secret string, p sandbox.Provider, client *http.Client) http.Handler {
	s := &Server{secret: secret, provider: p, client: &http.Client{Timeout: 2 * time.Minute}, cancels: map[string]context.CancelFunc{}}
	s.client = client
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/v1/inspections", s.inspect)
	mux.HandleFunc("POST /internal/v1/inspections/{id}/cancel", s.cancel)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
		if err != nil {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		if !s.authenticate(r, body) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		mux.ServeHTTP(w, r)
	})
}
func (s *Server) authenticate(r *http.Request, body []byte) bool {
	ts, err := strconv.ParseInt(r.Header.Get("X-DevPilot-Timestamp"), 10, 64)
	if err != nil || time.Since(time.Unix(ts, 0)) > 5*time.Minute || time.Until(time.Unix(ts, 0)) > time.Minute {
		return false
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(r.Header.Get("X-DevPilot-Signature"), "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(s.secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10) + "\n" + r.Method + "\n" + r.URL.Path + "\n"))
	mac.Write(body)
	return hmac.Equal(provided, mac.Sum(nil))
}
func (s *Server) inspect(w http.ResponseWriter, r *http.Request) {
	var input model.Request
	if json.NewDecoder(r.Body).Decode(&input) != nil || input.WorkspaceID == "" || len(input.CommitSHA) != 40 || !validLimits(input.Limits) {
		http.Error(w, "invalid request", 422)
		return
	}
	u, err := url.Parse(input.ArchiveURL)
	if err != nil || u.Scheme != "https" || !archiveHostAllowed(u.Hostname()) {
		http.Error(w, "invalid request", 422)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(input.Limits.OverallTimeoutSeconds)*time.Second)
	s.mu.Lock()
	s.cancels[input.WorkspaceID] = cancel
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); delete(s.cancels, input.WorkspaceID); s.mu.Unlock() }()
	result := s.run(ctx, input)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
func validLimits(l model.Limits) bool {
	return l.MaxArchiveBytes > 0 && l.MaxArchiveBytes <= 200<<20 && l.MaxExpandedBytes > 0 && l.MaxExpandedBytes <= 1<<30 && l.MaxFileBytes > 0 && l.MaxFileBytes <= 100<<20 && l.MaxFiles > 0 && l.MaxFiles <= 100000 && l.MaxDepth > 0 && l.MaxDepth <= 100 && l.AcquisitionTimeoutSeconds > 0 && l.AcquisitionTimeoutSeconds <= 300 && l.InspectionTimeoutSeconds > 0 && l.InspectionTimeoutSeconds <= 300 && l.OverallTimeoutSeconds > 0 && l.OverallTimeoutSeconds <= 600
}
func archiveHostAllowed(host string) bool {
	return host == "codeload.github.com" || strings.HasSuffix(host, ".githubusercontent.com")
}
func (s *Server) run(ctx context.Context, input model.Request) (result model.Result) {
	result.WorkspaceID = input.WorkspaceID
	root, err := os.MkdirTemp("", "devpilot-workspace-")
	if err != nil {
		result.Status = "failed"
		result.FailureCode = "workspace_create_failed"
		return
	}
	defer os.RemoveAll(root)
	defer s.provider.Cleanup(context.Background(), input.WorkspaceID)
	archivePath := filepath.Join(root, "snapshot.tar.gz")
	f, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		result.Status = "failed"
		result.FailureCode = "archive_create_failed"
		return
	}
	acquisitionCtx, acquisitionCancel := context.WithTimeout(ctx, time.Duration(input.Limits.AcquisitionTimeoutSeconds)*time.Second)
	defer acquisitionCancel()
	req, err := http.NewRequestWithContext(acquisitionCtx, http.MethodGet, input.ArchiveURL, nil)
	if err != nil {
		f.Close()
		result.Status = "failed"
		result.FailureCode = "archive_download_failed"
		return
	}
	downloadClient := *s.client
	downloadClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || req.URL.Scheme != "https" || !archiveHostAllowed(req.URL.Hostname()) {
			return http.ErrUseLastResponse
		}
		return nil
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		f.Close()
		result.Status = statusForContext(acquisitionCtx)
		if result.Status == "timed_out" {
			result.FailureCode = "acquisition_timeout"
		} else {
			result.FailureCode = "archive_download_failed"
		}
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		f.Close()
		result.Status = "failed"
		result.FailureCode = "archive_download_failed"
		return
	}
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, input.Limits.MaxArchiveBytes+1))
	resp.Body.Close()
	f.Close()
	if copyErr != nil || n > input.Limits.MaxArchiveBytes {
		result.Status = "failed"
		result.FailureCode = "archive_size_limit"
		return
	}
	extracted := filepath.Join(root, "repository")
	if os.Mkdir(extracted, 0700) != nil {
		result.Status = "failed"
		result.FailureCode = "workspace_create_failed"
		return
	}
	source, err := os.Open(archivePath)
	if err != nil {
		result.Status = "failed"
		result.FailureCode = "archive_read_failed"
		return
	}
	extraction, err := securearchive.ExtractTarGzip(source, extracted, securearchive.Limits{MaxExpandedBytes: input.Limits.MaxExpandedBytes, MaxFileBytes: input.Limits.MaxFileBytes, MaxFiles: input.Limits.MaxFiles, MaxDepth: input.Limits.MaxDepth})
	source.Close()
	os.Remove(archivePath)
	if err != nil {
		result.Status = "failed"
		result.FailureCode = "unsafe_archive"
		return
	}
	inspectCtx, cancel := context.WithTimeout(ctx, time.Duration(input.Limits.InspectionTimeoutSeconds)*time.Second)
	defer cancel()
	artifact, err := s.provider.Inspect(inspectCtx, input.WorkspaceID, extracted, input.Repository+"\n"+input.CommitSHA)
	if err != nil {
		result.Status = statusForContext(inspectCtx)
		if result.Status == "timed_out" {
			result.FailureCode = "inspection_timeout"
		} else {
			result.FailureCode = "inspection_failed"
		}
		return
	}
	artifact.Symlinks += extraction.Symlinks
	artifact.SuspiciousEntries += extraction.Suspicious
	if extraction.MaxDepth > artifact.MaximumDepth {
		artifact.MaximumDepth = extraction.MaxDepth
	}
	result.Status = "succeeded"
	result.Artifact = &artifact
	return
}
func statusForContext(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timed_out"
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return "cancelled"
	}
	return "failed"
}
func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	cancel := s.cancels[id]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		_ = s.provider.Terminate(context.Background(), id)
	}
	w.WriteHeader(http.StatusNoContent)
}
