package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
)

type agentDatabaseCreateRequest struct {
	Name      string `json:"name"`
	Charset   string `json:"charset"`
	Collation string `json:"collation"`
}

type agentDatabaseDeleteRequest struct {
	Name string `json:"name"`
}

func (h *Handler) handleDatabaseCreate(msg *nats.Msg) {
	var req agentDatabaseCreateRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.replyAgentErr(msg, "invalid request: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := h.engine.CreateDatabase(ctx, req.Name, req.Charset, req.Collation); err != nil {
		slog.Error("create database failed", "name", req.Name, "err", err)
		h.replyAgentErr(msg, err.Error())
		return
	}

	slog.Info("database created", "name", req.Name)
	h.replyAgentOK(msg)
}

func (h *Handler) handleDatabaseDelete(msg *nats.Msg) {
	var req agentDatabaseDeleteRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.replyAgentErr(msg, "invalid request: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := h.engine.DropDatabase(ctx, req.Name); err != nil {
		slog.Error("drop database failed", "name", req.Name, "err", err)
		h.replyAgentErr(msg, err.Error())
		return
	}

	slog.Info("database dropped", "name", req.Name)
	h.replyAgentOK(msg)
}
