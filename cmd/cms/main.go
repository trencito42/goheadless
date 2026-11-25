// Command cms is the CMS server binary. It also exposes the one-time
// owner bootstrap as a subcommand, since there is no public registration.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/stefan10xdd/strapi-killer/internal/auth"
	"github.com/stefan10xdd/strapi-killer/internal/authorization"
	"github.com/stefan10xdd/strapi-killer/internal/config"
	"github.com/stefan10xdd/strapi-killer/internal/content"
	httpapi "github.com/stefan10xdd/strapi-killer/internal/http"
	"github.com/stefan10xdd/strapi-killer/internal/projects"
	"github.com/stefan10xdd/strapi-killer/internal/schema"
	"github.com/stefan10xdd/strapi-killer/internal/storage"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if len(os.Args) > 1 && os.Args[1] == "bootstrap-owner" {
		if err := runBootstrapOwner(logger, os.Args[2:]); err != nil {
			logger.Error("bootstrap-owner failed", "error", err)
			os.Exit(1)
		}
		return
	}

	if err := runServe(logger); err != nil {
		logger.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}

func runServe(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := storage.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	if err := storage.Migrate(ctx, pool, "migrations"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	sessionTTL, err := time.ParseDuration(cfg.SessionTTL)
	if err != nil {
		return fmt.Errorf("parse session ttl: %w", err)
	}

	registry := schema.NewRegistry(pool)
	if err := registry.Warm(ctx); err != nil {
		return fmt.Errorf("warm schema registry: %w", err)
	}

	authSvc := auth.NewService(pool, sessionTTL)
	authzSvc := authorization.NewService(pool)
	projectsSvc := projects.NewService(pool)
	contentSvc := content.NewService(pool, registry)

	var corsOrigins []string
	if v := os.Getenv("CMS_CORS_ORIGINS"); v != "" {
		corsOrigins = strings.Split(v, ",")
	}

	router := httpapi.NewRouter(httpapi.Deps{
		Logger:      logger,
		AuthSvc:     authSvc,
		AuthzSvc:    authzSvc,
		ProjectsSvc: projectsSvc,
		Registry:    registry,
		ContentSvc:  contentSvc,
		CORSOrigins: corsOrigins,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func runBootstrapOwner(logger *slog.Logger, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: cms bootstrap-owner <email> <password>")
	}
	email, password := args[0], args[1]

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := storage.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	if err := storage.Migrate(ctx, pool, "migrations"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	sessionTTL, err := time.ParseDuration(cfg.SessionTTL)
	if err != nil {
		return fmt.Errorf("parse session ttl: %w", err)
	}

	authSvc := auth.NewService(pool, sessionTTL)
	user, err := authSvc.BootstrapOwner(ctx, email, password)
	if err != nil {
		return err
	}
	logger.Info("owner created", "id", user.ID, "email", user.Email)
	return nil
}
