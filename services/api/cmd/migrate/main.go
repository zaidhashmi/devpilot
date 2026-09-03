package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/devpilot/devpilot/services/api/internal/config"
	"github.com/devpilot/devpilot/services/api/internal/database"
	"github.com/jackc/pgx/v5"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("connect for migrations", "error", err)
		os.Exit(1)
	}
	defer connection.Close(context.Background()) //nolint:errcheck
	if err := database.Migrate(ctx, connection); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
	slog.Info("migrations applied")
}
