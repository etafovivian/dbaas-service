package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
)

type agentUserCreateRequest struct {
	Username  string   `json:"username"`
	Password  string   `json:"password"`
	Role      string   `json:"role"`
	Databases []string `json:"databases"`
}

type agentUserDeleteRequest struct {
	Username  string   `json:"username"`
	Databases []string `json:"databases"`
}

type agentUserResetPasswordRequest struct {
	Username    string `json:"username"`
	NewPassword string `json:"new_password"`
}

func (h *Handler) handleUserCreate(msg *nats.Msg) {
	var req agentUserCreateRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.replyAgentErr(msg, "invalid request: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := h.engine.CreateUser(ctx, req.Username, req.Password, req.Role, req.Databases); err != nil {
		slog.Error("create user failed", "username", req.Username, "err", err)
		h.replyAgentErr(msg, err.Error())
		return
	}

	slog.Info("user created", "username", req.Username, "role", req.Role, "databases", req.Databases)
	h.replyAgentOK(msg)
}

func (h *Handler) handleUserDelete(msg *nats.Msg) {
	var req agentUserDeleteRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.replyAgentErr(msg, "invalid request: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := h.engine.DropUser(ctx, req.Username, req.Databases); err != nil {
		slog.Error("drop user failed", "username", req.Username, "err", err)
		h.replyAgentErr(msg, err.Error())
		return
	}

	slog.Info("user dropped", "username", req.Username)
	h.replyAgentOK(msg)
}

func (h *Handler) handleUserResetPassword(msg *nats.Msg) {
	var req agentUserResetPasswordRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.replyAgentErr(msg, "invalid request: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := h.engine.ResetPassword(ctx, req.Username, req.NewPassword); err != nil {
		slog.Error("reset password failed", "username", req.Username, "err", err)
		h.replyAgentErr(msg, err.Error())
		return
	}

	slog.Info("password reset", "username", req.Username)
	h.replyAgentOK(msg)
}
