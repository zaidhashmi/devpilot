package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

type Config struct {
	Environment     string
	Address         string
	DatabaseURL     string
	RedisURL        string
	LogLevelName    string
	ShutdownTimeout time.Duration
	SessionTTL      time.Duration
	CookieSecure    bool
	AllowedOrigin   string
}

func Load() (Config, error) {
	environment := envOrDefault("DEVPILOT_ENV", "development")
	cfg := Config{
		Environment:  environment,
		Address:      envOrDefault("DEVPILOT_API_ADDR", ":8080"),
		DatabaseURL:  envOrDefault("DEVPILOT_DATABASE_URL", "postgres://devpilot:devpilot_local@localhost:5432/devpilot?sslmode=disable"),
		RedisURL:     os.Getenv("DEVPILOT_REDIS_URL"),
		LogLevelName: envOrDefault("DEVPILOT_LOG_LEVEL", "INFO"),
	}
	cfg.CookieSecure = environment != "development" && environment != "test"
	cfg.AllowedOrigin = envOrDefault("DEVPILOT_WEB_ORIGIN", "http://localhost:3000")

	shutdownTimeout, err := time.ParseDuration(envOrDefault("DEVPILOT_SHUTDOWN_TIMEOUT", "10s"))
	if err != nil || shutdownTimeout <= 0 {
		return Config{}, fmt.Errorf("DEVPILOT_SHUTDOWN_TIMEOUT must be a positive duration")
	}
	cfg.ShutdownTimeout = shutdownTimeout
	sessionTTL, err := time.ParseDuration(envOrDefault("DEVPILOT_SESSION_TTL", "24h"))
	if err != nil || sessionTTL <= 0 {
		return Config{}, fmt.Errorf("DEVPILOT_SESSION_TTL must be a positive duration")
	}
	cfg.SessionTTL = sessionTTL

	if cfg.Address == "" {
		return Config{}, errors.New("DEVPILOT_API_ADDR must not be empty")
	}
	if !isValidLogLevel(cfg.LogLevelName) {
		return Config{}, fmt.Errorf("DEVPILOT_LOG_LEVEL must be DEBUG, INFO, WARN, or ERROR")
	}
	if environment != "development" && environment != "test" {
		if os.Getenv("DEVPILOT_DATABASE_URL") == "" {
			return Config{}, errors.New("DEVPILOT_DATABASE_URL is required outside development and test")
		}
	}

	return cfg, nil
}

func (c Config) LogLevel() slog.Level {
	switch strings.ToUpper(c.LogLevelName) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func isValidLogLevel(value string) bool {
	switch strings.ToUpper(value) {
	case "DEBUG", "INFO", "WARN", "ERROR":
		return true
	default:
		return false
	}
}

func envOrDefault(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}
