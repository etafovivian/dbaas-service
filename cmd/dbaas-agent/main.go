package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	agentcfg "github.com/etafovivian/dbaas-service/internal/agent/config"
	pgagent "github.com/etafovivian/dbaas-service/internal/agent/engine/postgres"
	agenthandler "github.com/etafovivian/dbaas-service/internal/agent/handler"
	"github.com/nats-io/nats.go"
)

var version = "dev"

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	slog.Info("dbaas-agent starting", "version", version)

	cfg, err := agentcfg.Load()
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}
	slog.Info("config loaded", "instance_id", cfg.InstanceID, "node_id", cfg.NodeID, "tier", cfg.Tier)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Connect to NATS
	nc, err := nats.Connect(cfg.NATSUrl,
		nats.Name("dbaas-agent-"+cfg.NodeID),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2),
	)
	if err != nil {
		slog.Error("failed to connect to NATS", "url", cfg.NATSUrl, "err", err)
		os.Exit(1)
	}
	defer func() { _ = nc.Drain() }()
	slog.Info("connected to NATS")

	// Create PostgreSQL engine using INTERNAL credentials (the PG superuser).
	// The agent always connects as the internal user for management operations.
	eng := pgagent.New(cfg.PGInternalUser, cfg.PGInternalPass)

	// Wait for database to be ready
	if err := eng.WaitReady(ctx); err != nil {
		slog.Error("database not ready", "err", err)
		os.Exit(1)
	}
	defer eng.Close(ctx)
	slog.Info("database is ready", "engine", eng.Name())

	// Create the tenant admin user on first boot (idempotent).
	// This user gets full privileges — tenants connect via pgAdmin with these creds.
	if cfg.PGAdminUser != "" && cfg.PGAdminPass != "" {
		if err := eng.EnsureTenantAdmin(ctx, cfg.PGAdminUser, cfg.PGAdminPass); err != nil {
			slog.Error("failed to create tenant admin user", "err", err)
			// Non-fatal — agent can still operate, tenant just can't connect directly yet
		} else {
			slog.Info("tenant admin user ready", "username", cfg.PGAdminUser)
		}
	}

	// Create handler and register subscriptions
	h := agenthandler.New(nc, eng, cfg)
	if err := h.RegisterSubscriptions(); err != nil {
		slog.Error("failed to register subscriptions", "err", err)
		os.Exit(1)
	}

	// Notify dbaas-service that we're ready
	if err := h.PublishReady(); err != nil {
		slog.Error("failed to publish ready", "err", err)
	} else {
		slog.Info("published ready to dbaas-service")
	}

	// Start heartbeat loop
	go h.HeartbeatLoop(ctx)

	slog.Info("dbaas-agent running")
	<-ctx.Done()
	slog.Info("dbaas-agent shutting down")
}
