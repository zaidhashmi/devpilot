package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/devpilot/devpilot/services/api/internal/config"
	"github.com/devpilot/devpilot/services/api/internal/database"
	"github.com/devpilot/devpilot/services/api/internal/httpapi"
	"github.com/devpilot/devpilot/services/api/internal/platform"
)

type App struct {
	config config.Config
	logger *slog.Logger
}

func New(cfg config.Config, logger *slog.Logger) *App {
	return &App{config: cfg, logger: logger}
}

func (a *App) Run(ctx context.Context) error {
	db, err := database.Open(ctx, a.config.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	platformService := platform.New(db, a.config.SessionTTL)
	server := &http.Server{
		Addr:              a.config.Address,
		Handler:           httpapi.New(a.logger, db, platformService, a.config.CookieSecure, a.config.AllowedOrigin),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errorsChannel := make(chan error, 1)
	go func() {
		a.logger.Info("api listening", "address", a.config.Address, "environment", a.config.Environment)
		errorsChannel <- server.ListenAndServe()
	}()

	select {
	case err := <-errorsChannel:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		a.logger.Info("shutdown requested")
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), a.config.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		return err
	}

	return nil
}
