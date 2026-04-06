package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	sharedcfg "github.com/etafovivian/shared/config"
	"github.com/etafovivian/shared/healthz"
	"github.com/etafovivian/shared/logger"
	"github.com/etafovivian/shared/natsutil"
	"github.com/etafovivian/shared/postgres"
	"github.com/etafovivian/dbaas-service/internal/config"
	"github.com/etafovivian/dbaas-service/internal/engine"
	pgengine "github.com/etafovivian/dbaas-service/internal/engine/postgres"
	"github.com/etafovivian/dbaas-service/internal/handler"
	"github.com/etafovivian/dbaas-service/internal/metrics"
	"github.com/etafovivian/dbaas-service/internal/store"
	"github.com/etafovivian/dbaas-service/migrations"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var version = "dev"

func main() {
	migrateOnly := flag.Bool("migrate-only", false, "run DB migrations and exit")
	flag.Parse()

	logger.New(sharedcfg.OptionalEnv("ENV", "production"))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	// Run migrations
	if err := postgres.RunMigrations(cfg.DatabaseURL, migrations.FS); err != nil {
		slog.Error("migration failed", "err", err)
		os.Exit(1)
	}
	if *migrateOnly {
		slog.Info("migrations complete, exiting")
		return
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// PostgreSQL pool
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to create pool", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	// NATS connection
	nc, err := natsutil.Connect(cfg.NATSUrl, cfg.NATSCredsFile)
	if err != nil {
		slog.Error("failed to connect to NATS", "err", err)
		os.Exit(1)
	}
	defer func() { _ = nc.Drain() }()

	// Register database engines
	engines := engine.NewRegistry(
		pgengine.New(),
		// Add new engines here: mariadb.New(), etc.
	)

	// Dependencies
	st := store.New(pool)
	m := metrics.New()
	h := handler.New(handler.Deps{
		Store:        st,
		NC:           nc,
		Metrics:      m,
		Engines:      engines,
		ImageID:      cfg.DbaasImageID,
		MgmtNetID:    cfg.DbaasMgmtNetID,
		AgentVersion: cfg.DbaasAgentVersion,
		S3Endpoint:   cfg.CephS3Endpoint,
		S3AccessKey:  cfg.CephS3AccessKey,
		S3SecretKey:  cfg.CephS3SecretKey,
	})

	// Register NATS subscriptions
	if err := h.RegisterSubscriptions(nc); err != nil {
		slog.Error("failed to register subscriptions", "err", err)
		os.Exit(1)
	}

	// HTTP server (metrics + healthz)
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/healthz", healthz.Handler())
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.MetricsPort),
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("metrics server listening", "port", cfg.MetricsPort, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("metrics server error", "err", err)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")

	h.Shutdown()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutCancel()
	_ = srv.Shutdown(shutCtx)
}
