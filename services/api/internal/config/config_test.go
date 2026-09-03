package config

import "testing"

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
