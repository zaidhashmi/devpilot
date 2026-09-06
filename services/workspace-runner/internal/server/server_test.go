package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/devpilot/devpilot/services/workspace-runner/internal/inspect"
	"github.com/devpilot/devpilot/services/workspace-runner/internal/model"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

type fakeProvider struct {
	mu         sync.Mutex
	roots      []string
	block      bool
	terminated bool
	cleaned    int
}
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func (f *fakeProvider) Inspect(ctx context.Context, id, root, meta string) (model.Artifact, error) {
	f.mu.Lock()
	f.roots = append(f.roots, root)
	f.mu.Unlock()
	if f.block {
		<-ctx.Done()
		return model.Artifact{}, ctx.Err()
	}
	parts := bytes.SplitN([]byte(meta), []byte("\n"), 2)
	return inspect.Directory(root, string(parts[0]), string(parts[1]))
}
func (f *fakeProvider) Terminate(context.Context, string) error { f.terminated = true; return nil }
func (f *fakeProvider) Cleanup(context.Context, string) error   { f.cleaned++; return nil }
func archiveData(t *testing.T) []byte {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	body := []byte("module example")
	_ = tw.WriteHeader(&tar.Header{Name: "repo/go.mod", Typeflag: tar.TypeReg, Size: int64(len(body)), Mode: 0600})
	_, _ = tw.Write(body)
	_ = tw.Close()
	_ = gz.Close()
	return b.Bytes()
}
func signedRequest(t *testing.T, target, secret string, input model.Request) *http.Request {
	t.Helper()
	body, _ := json.Marshal(input)
	req, _ := http.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "\n" + http.MethodPost + "\n" + req.URL.Path + "\n"))
	mac.Write(body)
	req.Header.Set("X-DevPilot-Timestamp", ts)
	req.Header.Set("X-DevPilot-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	return req
}
func TestAuthenticatedInspectionAndCleanup(t *testing.T) {
	p := &fakeProvider{}
	archiveClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(archiveData(t)))}, nil
	})}
	handler := NewWithHTTPClient("a-secret-that-is-at-least-thirty-two-bytes", p, archiveClient)
	api := httptest.NewServer(handler)
	defer api.Close()
	input := model.Request{WorkspaceID: "018f-test", Repository: "octo/repo", CommitSHA: "0123456789abcdef0123456789abcdef01234567", ArchiveURL: "https://codeload.github.com/ephemeral-capability", Limits: model.Limits{MaxArchiveBytes: 1024, MaxExpandedBytes: 1024, MaxFileBytes: 1024, MaxFiles: 10, MaxDepth: 10, AcquisitionTimeoutSeconds: 2, InspectionTimeoutSeconds: 2, OverallTimeoutSeconds: 3}}
	resp, err := http.DefaultClient.Do(signedRequest(t, api.URL+"/internal/v1/inspections", "a-secret-that-is-at-least-thirty-two-bytes", input))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result model.Result
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "succeeded" || result.Artifact == nil || result.Artifact.RegularFiles != 1 {
		t.Fatalf("result=%+v", result)
	}
	for _, root := range p.roots {
		if _, err := os.Stat(filepath.Dir(root)); !os.IsNotExist(err) {
			t.Fatalf("temporary workspace remains: %s", root)
		}
	}
}
func TestRejectsUnsignedRequest(t *testing.T) {
	api := httptest.NewServer(New("a-secret-that-is-at-least-thirty-two-bytes", &fakeProvider{}))
	defer api.Close()
	resp, err := http.Post(api.URL+"/internal/v1/inspections", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}
func TestUnsafeArchiveFailsAndCleansUp(t *testing.T) {
	p := &fakeProvider{}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader([]byte("malformed")))}, nil
	})}
	handler := NewWithHTTPClient("a-secret-that-is-at-least-thirty-two-bytes", p, client)
	api := httptest.NewServer(handler)
	defer api.Close()
	input := model.Request{WorkspaceID: "unsafe", Repository: "octo/repo", CommitSHA: "0123456789abcdef0123456789abcdef01234567", ArchiveURL: "https://codeload.github.com/capability", Limits: model.Limits{MaxArchiveBytes: 1024, MaxExpandedBytes: 1024, MaxFileBytes: 1024, MaxFiles: 10, MaxDepth: 10, AcquisitionTimeoutSeconds: 1, InspectionTimeoutSeconds: 1, OverallTimeoutSeconds: 2}}
	resp, err := http.DefaultClient.Do(signedRequest(t, api.URL+"/internal/v1/inspections", "a-secret-that-is-at-least-thirty-two-bytes", input))
	if err != nil {
		t.Fatal(err)
	}
	var result model.Result
	_ = json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if result.Status != "failed" || result.FailureCode != "unsafe_archive" || p.cleaned != 1 {
		t.Fatalf("result=%+v cleaned=%d", result, p.cleaned)
	}
}
func TestArchiveHostAllowlist(t *testing.T) {
	if !archiveHostAllowed("codeload.github.com") || archiveHostAllowed("metadata.internal") || archiveHostAllowed("codeload.github.com.attacker.test") {
		t.Fatal("archive host allowlist is unsafe")
	}
}
func TestInspectionTimeoutTriggersCleanup(t *testing.T) {
	p := &fakeProvider{block: true}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(archiveData(t)))}, nil
	})}
	s := &Server{provider: p, client: client, cancels: map[string]context.CancelFunc{}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := s.run(ctx, model.Request{WorkspaceID: "timeout", Repository: "octo/repo", CommitSHA: "0123456789abcdef0123456789abcdef01234567", ArchiveURL: "https://codeload.github.com/cap", Limits: model.Limits{MaxArchiveBytes: 1024, MaxExpandedBytes: 1024, MaxFileBytes: 1024, MaxFiles: 10, MaxDepth: 10, AcquisitionTimeoutSeconds: 1, InspectionTimeoutSeconds: 2, OverallTimeoutSeconds: 3}})
	if result.Status != "timed_out" || result.FailureCode != "inspection_timeout" || p.cleaned != 1 {
		t.Fatalf("result=%+v cleaned=%d", result, p.cleaned)
	}
}
