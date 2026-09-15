package agentclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClientAuthenticatesWithoutLeakingCapabilities(t *testing.T) {
	secret := "service-secret-not-for-errors-123456789"
	capability := "source-capability-not-for-errors"
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			t.Fatal("service authentication missing")
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), capability) {
			t.Fatal("capability missing from private runtime request")
		}
		return &http.Response{StatusCode: 502, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("provider leaked " + secret + " " + capability))}, nil
	})
	client := New("https://runtime.invalid", secret, time.Second)
	client.http.Transport = transport
	_, err := client.Analyze(context.Background(), Request{SourceCapability: capability})
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), capability) {
		t.Fatalf("unsafe error=%v", err)
	}
}

func TestRuntimeTransportFailureIsUnavailableAndSanitized(t *testing.T) {
	secret := "test-agent-service-token-32-characters"
	client := New("https://runtime.invalid", secret, time.Second)
	client.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed with " + secret)
	})

	_, err := client.ResolveProvider(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ResolveProvider() error = %v, want ErrUnavailable", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("transport error leaked the service token")
	}
}
