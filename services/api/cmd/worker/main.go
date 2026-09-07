package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/devpilot/devpilot/services/api/internal/config"
	"github.com/devpilot/devpilot/services/api/internal/database"
	"github.com/devpilot/devpilot/services/api/internal/githubapp"
	"github.com/devpilot/devpilot/services/api/internal/platform"
	"github.com/devpilot/devpilot/services/api/internal/runner"
	"github.com/google/uuid"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel()}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database unavailable", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	service := platform.New(db, cfg.SessionTTL)
	if cfg.GitHub.Enabled {
		client, clientErr := githubapp.NewHTTPClient(cfg.GitHub.AppID, cfg.GitHub.ClientID, cfg.GitHub.ClientSecret, cfg.GitHub.PrivateKeyPEM, cfg.GitHub.APIURL, cfg.GitHub.OAuthURL, nil)
		if clientErr != nil {
			logger.Error("github client", "error", clientErr)
			os.Exit(1)
		}
		service.SetGitHubClient(client)
	}
	if cfg.Runner.SharedSecret != "" {
		service.SetRunnerClient(runner.New(cfg.Runner.URL, cfg.Runner.SharedSecret, cfg.Runner.RequestTimeout))
	}
	workerID := "worker-" + uuid.NewString()
	semaphore := make(chan struct{}, cfg.Worker.Concurrency)
	ticker := time.NewTicker(cfg.Worker.PollInterval)
	defer ticker.Stop()
	var wg sync.WaitGroup
	logger.Info("worker started", "worker_id", workerID, "concurrency", cfg.Worker.Concurrency)
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			logger.Info("worker stopped")
			return
		case <-ticker.C:
			select {
			case semaphore <- struct{}{}:
			default:
				continue
			}
			event, ok, claimErr := service.ClaimOutbox(ctx, workerID, cfg.Worker.StaleClaim)
			if claimErr != nil {
				<-semaphore
				logger.Error("outbox claim failed", "error", claimErr)
				continue
			}
			if !ok {
				<-semaphore
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-semaphore }()
				if processErr := service.ProcessOutbox(ctx, event, cfg.Worker); processErr != nil {
					logger.Error("outbox processing failed", "event_id", event.ID, "event_type", event.EventType, "error", processErr)
				}
			}()
		}
	}
}
