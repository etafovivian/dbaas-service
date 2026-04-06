// Package handler provides NATS subscription handlers for the dbaas-agent.
// The agent runs inside each database VM and executes commands from the dbaas-service.
// Engine-specific behavior (role detection, health checks) is delegated to the
// agent engine interface — see internal/agent/engine/.
package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	agentcfg "github.com/etafovivian/dbaas-service/internal/agent/config"
	agentengine "github.com/etafovivian/dbaas-service/internal/agent/engine"
	"github.com/nats-io/nats.go"
)

// Handler manages agent NATS subscriptions.
type Handler struct {
	nc     *nats.Conn
	engine agentengine.AgentEngine
	cfg    *agentcfg.Config
}

// New creates an agent Handler.
func New(nc *nats.Conn, eng agentengine.AgentEngine, cfg *agentcfg.Config) *Handler {
	return &Handler{
		nc:     nc,
		engine: eng,
		cfg:    cfg,
	}
}

// RegisterSubscriptions subscribes to all agent NATS subjects scoped to this node.
func (h *Handler) RegisterSubscriptions() error {
	prefix := fmt.Sprintf("dbaas.agent.%s", h.cfg.NodeID)
	subs := map[string]nats.MsgHandler{
		prefix + ".ping":           h.handlePing,
		prefix + ".database.create": h.handleDatabaseCreate,
		prefix + ".database.delete": h.handleDatabaseDelete,
		prefix + ".user.create":     h.handleUserCreate,
		prefix + ".user.delete":     h.handleUserDelete,
		prefix + ".user.reset-password": h.handleUserResetPassword,
	}

	for subj, hdlr := range subs {
		if _, err := h.nc.Subscribe(subj, hdlr); err != nil {
			return fmt.Errorf("subscribe %s: %w", subj, err)
		}
		slog.Info("agent subscribed", "subject", subj)
	}
	return nil
}

// PublishReady notifies the dbaas-service that this agent is online.
func (h *Handler) PublishReady() error {
	data, err := json.Marshal(map[string]string{
		"instance_id": h.cfg.InstanceID,
		"node_id":     h.cfg.NodeID,
	})
	if err != nil {
		return fmt.Errorf("marshal ready: %w", err)
	}

	reply, err := h.nc.Request("dbaas.agent.ready", data, 10*time.Second)
	if err != nil {
		return fmt.Errorf("publish ready: %w", err)
	}

	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(reply.Data, &resp); err != nil {
		return fmt.Errorf("unmarshal ready reply: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("ready rejected: %s", resp.Error)
	}
	return nil
}

// HeartbeatLoop sends periodic heartbeat messages to the dbaas-service.
func (h *Handler) HeartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.sendHeartbeat(ctx)
		}
	}
}

func (h *Handler) sendHeartbeat(ctx context.Context) {
	role := "primary" // single-node is always primary
	if h.cfg.Tier == "ha" {
		role = h.engine.DetectRole(ctx)
	}

	data, err := json.Marshal(map[string]any{
		"instance_id":           h.cfg.InstanceID,
		"node_id":               h.cfg.NodeID,
		"role":                  role,
		"replication_lag_bytes": 0,
	})
	if err != nil {
		slog.Error("marshal heartbeat failed", "err", err)
		return
	}

	if _, err := h.nc.Request("dbaas.agent.heartbeat", data, 5*time.Second); err != nil {
		slog.Warn("heartbeat failed", "err", err)
	}
}

// handlePing responds with a simple pong.
func (h *Handler) handlePing(msg *nats.Msg) {
	resp, _ := json.Marshal(map[string]any{
		"ok":          true,
		"instance_id": h.cfg.InstanceID,
		"node_id":     h.cfg.NodeID,
	})
	_ = msg.Respond(resp)
}

// replyAgentOK sends a success response.
func (h *Handler) replyAgentOK(msg *nats.Msg) {
	resp, _ := json.Marshal(map[string]any{"ok": true})
	_ = msg.Respond(resp)
}

// replyAgentErr sends an error response.
func (h *Handler) replyAgentErr(msg *nats.Msg, errMsg string) {
	resp, _ := json.Marshal(map[string]any{"ok": false, "error": errMsg})
	_ = msg.Respond(resp)
}
