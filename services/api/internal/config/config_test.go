package config

import (
	"strings"
	"testing"
)

func TestLoadDevelopmentDefaults(t *testing.T) {
	t.Setenv("DEVPILOT_ENV", "development")
	t.Setenv("DEVPILOT_API_ADDR", ":9090")
	t.Setenv("DEVPILOT_SHUTDOWN_TIMEOUT", "5s")
	t.Setenv("DEVPILOT_LOG_LEVEL", "DEBUG")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Address != ":9090" {
		t.Fatalf("Address = %q, want :9090", cfg.Address)
	}
	if cfg.DatabaseURL == "" {
		t.Fatal("DatabaseURL should have a safe development default")
	}
}

func TestLoadRequiresProductionDependencies(t *testing.T) {
	t.Setenv("DEVPILOT_ENV", "production")
	t.Setenv("DEVPILOT_DATABASE_URL", "")
	t.Setenv("DEVPILOT_REDIS_URL", "")

	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for missing production dependencies")
	}
}

func TestLoadRejectsInvalidTimeout(t *testing.T) {
	t.Setenv("DEVPILOT_ENV", "test")
	t.Setenv("DEVPILOT_SHUTDOWN_TIMEOUT", "never")

	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for invalid timeout")
	}
}

func TestLoadRequiresGitHubSecretsWhenEnabled(t *testing.T) {
	t.Setenv("DEVPILOT_ENV", "test")
	t.Setenv("DEVPILOT_GITHUB_ENABLED", "true")
	t.Setenv("DEVPILOT_GITHUB_APP_ID", "123")
	t.Setenv("DEVPILOT_GITHUB_CLIENT_ID", "")
	t.Setenv("DEVPILOT_GITHUB_CLIENT_SECRET", "")
	t.Setenv("DEVPILOT_GITHUB_APP_SLUG", "devpilot-test")
	t.Setenv("DEVPILOT_GITHUB_PRIVATE_KEY", "")
	t.Setenv("DEVPILOT_GITHUB_WEBHOOK_SECRET", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected missing GitHub credentials to fail")
	}
}

func TestLoadRequiresGitHubOAuthClientSecret(t *testing.T) {
	t.Setenv("DEVPILOT_ENV", "test")
	t.Setenv("DEVPILOT_GITHUB_ENABLED", "true")
	t.Setenv("DEVPILOT_GITHUB_APP_ID", "123")
	t.Setenv("DEVPILOT_GITHUB_CLIENT_ID", "Iv1.client")
	t.Setenv("DEVPILOT_GITHUB_CLIENT_SECRET", "")
	t.Setenv("DEVPILOT_GITHUB_APP_SLUG", "devpilot-test")
	t.Setenv("DEVPILOT_GITHUB_PRIVATE_KEY", "placeholder")
	t.Setenv("DEVPILOT_GITHUB_WEBHOOK_SECRET", "placeholder")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "DEVPILOT_GITHUB_CLIENT_SECRET") {
		t.Fatalf("expected explicit client secret configuration error, got %v", err)
	}
}

func TestLoadConfiguresAnalysisWithCanonicalServiceToken(t *testing.T) {
	t.Setenv("DEVPILOT_ENV", "test")
	t.Setenv("DEVPILOT_ANALYSIS_ENABLED", "true")
	t.Setenv("DEVPILOT_AGENT_SERVICE_TOKEN", "test-agent-service-token-32-characters")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Agent.Enabled || cfg.Agent.ServiceToken == "" {
		t.Fatal("canonical service token did not configure analysis client")
	}
}

func TestLoadFailsFastWhenAnalysisTokenMissing(t *testing.T) {
	t.Setenv("DEVPILOT_ENV", "test")
	t.Setenv("DEVPILOT_ANALYSIS_ENABLED", "true")
	t.Setenv("DEVPILOT_AGENT_SERVICE_TOKEN", "")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "DEVPILOT_AGENT_SERVICE_TOKEN") {
		t.Fatalf("expected canonical token configuration error, got %v", err)
	}
}

func TestLoadDoesNotAcceptLegacyRuntimeServiceTokenName(t *testing.T) {
	legacySecret := "legacy-agent-runtime-token-32-characters"
	t.Setenv("DEVPILOT_ENV", "test")
	t.Setenv("DEVPILOT_ANALYSIS_ENABLED", "true")
	t.Setenv("DEVPILOT_AGENT_SERVICE_TOKEN", "")
	t.Setenv("DEVPILOT_AGENT_RUNTIME_SERVICE_TOKEN", legacySecret)

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "DEVPILOT_AGENT_SERVICE_TOKEN") {
		t.Fatalf("expected legacy variable to be ignored, got %v", err)
	}
	if strings.Contains(err.Error(), legacySecret) {
		t.Fatal("configuration error leaked secret value")
	}
}
