package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	agentcfg "github.com/etafovivian/dbaas-service/internal/agent/config"
	agentengine "github.com/etafovivian/dbaas-service/internal/agent/engine"
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

	// Create engine-specific handler
	// To add MariaDB: check cfg env var for engine type and create the right engine
	var eng agentengine.AgentEngine
	eng = pgagent.New(cfg.PGAdminUser, cfg.PGAdminPass)

	// Wait for database to be ready (engine-specific)
	if err := eng.WaitReady(ctx); err != nil {
		slog.Error("database not ready", "err", err)
		os.Exit(1)
	}
	defer eng.Close(ctx)
	slog.Info("database is ready", "engine", eng.Name())

	// Create handler and register subscriptions
	h := agenthandler.New(nc, eng, cfg)
	if err := h.RegisterSubscriptions(); err != nil {
		slog.Error("failed to register subscriptions", "err", err)
		os.Exit(1)
	}

	// Notify dbaas-service that we're ready
	if err := h.PublishReady(); err != nil {
		slog.Error("failed to publish ready", "err", err)
		// Don't exit — the service will detect via heartbeat eventually
	} else {
		slog.Info("published ready to dbaas-service")
	}

	// Start heartbeat loop
	go h.HeartbeatLoop(ctx)

	slog.Info("dbaas-agent running")
	<-ctx.Done()
	slog.Info("dbaas-agent shutting down")
}
