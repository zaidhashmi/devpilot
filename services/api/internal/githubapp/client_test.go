package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func testPrivateKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string, header http.Header) *http.Response {
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

func TestHTTPClientInstallationAndRepositoryFlow(t *testing.T) {
	var tokenRequest, repositoryRequest bool
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Fatal("missing bearer authorization")
		}
		switch r.URL.Path {
		case "/app/installations/42":
			return response(200, `{"id":42,"account":{"id":7,"login":"octo","type":"Organization"},"repository_selection":"selected"}`, http.Header{}), nil
		case "/app/installations/42/access_tokens":
			tokenRequest = true
			return response(200, `{"token":"test-token-value"}`, http.Header{}), nil
		case "/installation/repositories":
			repositoryRequest = true
			if r.Header.Get("Authorization") != "Bearer test-token-value" {
				t.Fatal("installation token not used")
			}
			return response(200, `{"repositories":[{"id":9,"name":"repo","full_name":"octo/repo","default_branch":"main","private":true,"owner":{"login":"octo"},"html_url":"https://github.com/octo/repo"}]}`, http.Header{}), nil
		default:
			return response(404, "", http.Header{}), nil
		}
	})}
	client, err := NewHTTPClient("123", testPrivateKey(t), "https://api.github.test", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := client.GetInstallation(context.Background(), 42)
	if err != nil || installation.AccountID != 7 {
		t.Fatalf("installation=%+v err=%v", installation, err)
	}
	repos, err := client.ListRepositories(context.Background(), 42)
	if err != nil || len(repos) != 1 || !repos[0].Private || !tokenRequest || !repositoryRequest {
		t.Fatalf("repos=%+v err=%v", repos, err)
	}
}

func TestHTTPClientRateLimitIsTypedAndTokenIsNotLeaked(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		header := http.Header{}
		header.Set("X-RateLimit-Remaining", "0")
		header.Set("X-RateLimit-Reset", "12345")
		return response(http.StatusForbidden, `{"message":"secret test-token-value"}`, header), nil
	})}
	client, err := NewHTTPClient("123", testPrivateKey(t), "https://api.github.test", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GetInstallation(context.Background(), 42)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("error=%v", err)
	}
	if strings.Contains(err.Error(), "test-token-value") {
		t.Fatal("response body leaked into error")
	}
}
