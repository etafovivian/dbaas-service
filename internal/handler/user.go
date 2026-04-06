package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/etafovivian/shared/natsutil"
	"github.com/nats-io/nats.go"
)

// ---------- request types ----------

type userCreateRequest struct {
	InstanceID string   `json:"instance_id"`
	TenantID   string   `json:"tenant_id"`
	Username   string   `json:"username"`
	Password   string   `json:"password"`
	Role       string   `json:"role"` // owner, readwrite, readonly
	Databases  []string `json:"databases"`
}

type userListRequest struct {
	InstanceID string `json:"instance_id"`
	TenantID   string `json:"tenant_id"`
}

type userDeleteRequest struct {
	InstanceID string `json:"instance_id"`
	TenantID   string `json:"tenant_id"`
	UserID     string `json:"user_id"`
}

type userResetPasswordRequest struct {
	InstanceID  string `json:"instance_id"`
	TenantID    string `json:"tenant_id"`
	Username    string `json:"username"`
	NewPassword string `json:"new_password"`
}

// ---------- handlers ----------

func (h *Handler) handleUserCreate(msg *nats.Msg) {
	const subj = "dbaas.user.create"
	var req userCreateRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.replyErr(msg, subj, "invalid request: "+err.Error())
		return
	}
	if req.InstanceID == "" || req.TenantID == "" || req.Username == "" || req.Password == "" {
		h.replyErr(msg, subj, "instance_id, tenant_id, username, and password are required")
		return
	}
	if req.Role == "" {
		req.Role = "readonly"
	}
	if req.Role != "owner" && req.Role != "readwrite" && req.Role != "readonly" {
		h.replyErr(msg, subj, "role must be 'owner', 'readwrite', or 'readonly'")
		return
	}
	if len(req.Databases) == 0 {
		h.replyErr(msg, subj, "at least one database is required")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Verify instance
	inst, err := h.store.GetInstance(ctx, req.InstanceID)
	if err != nil {
		h.replyErr(msg, subj, "instance not found: "+err.Error())
		return
	}
	if inst.TenantID != req.TenantID {
		h.replyErr(msg, subj, "instance not found")
		return
	}
	if inst.Status != "available" {
		h.replyErr(msg, subj, "instance is not available (status: "+inst.Status+")")
		return
	}

	// Block reserved usernames
	if req.Username == inst.AdminUser || req.Username == "postgres" || req.Username == "replicator" {
		h.replyErr(msg, subj, "username is reserved")
		return
	}

	// Insert user record
	user, err := h.store.CreateUser(ctx, req.InstanceID, req.TenantID, req.Username, req.Role, req.Databases)
	if err != nil {
		h.replyErr(msg, subj, "create user: "+err.Error())
		return
	}

	// Forward to agent
	var agentResp struct{ OK bool }
	if err := h.agentRequest(ctx, req.InstanceID, "user.create", map[string]any{
		"username":  req.Username,
		"password":  req.Password,
		"role":      req.Role,
		"databases": req.Databases,
	}, &agentResp); err != nil {
		slog.Error("agent user.create failed", "user_id", user.ID, "err", err)
		_ = h.store.UpdateUserStatus(ctx, user.ID, "error")
		user.Status = "error"
		h.m.AgentRequests.WithLabelValues("user.create", "error").Inc()
		h.m.NATSRequests.WithLabelValues(subj, "ok").Inc()
		natsutil.ReplyOK(msg, user)
		return
	}
	h.m.AgentRequests.WithLabelValues("user.create", "ok").Inc()

	_ = h.store.UpdateUserStatus(ctx, user.ID, "active")
	user.Status = "active"

	h.m.NATSRequests.WithLabelValues(subj, "ok").Inc()
	natsutil.ReplyOK(msg, user)
}

func (h *Handler) handleUserList(msg *nats.Msg) {
	const subj = "dbaas.user.list"
	var req userListRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.replyErr(msg, subj, "invalid request: "+err.Error())
		return
	}
	if req.InstanceID == "" || req.TenantID == "" {
		h.replyErr(msg, subj, "instance_id and tenant_id are required")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inst, err := h.store.GetInstance(ctx, req.InstanceID)
	if err != nil {
		h.replyErr(msg, subj, "instance not found: "+err.Error())
		return
	}
	if inst.TenantID != req.TenantID {
		h.replyErr(msg, subj, "instance not found")
		return
	}

	users, err := h.store.ListUsers(ctx, req.InstanceID)
	if err != nil {
		h.replyErr(msg, subj, err.Error())
		return
	}

	h.m.NATSRequests.WithLabelValues(subj, "ok").Inc()
	natsutil.ReplyOK(msg, users)
}

func (h *Handler) handleUserDelete(msg *nats.Msg) {
	const subj = "dbaas.user.delete"
	var req userDeleteRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.replyErr(msg, subj, "invalid request: "+err.Error())
		return
	}
	if req.InstanceID == "" || req.TenantID == "" || req.UserID == "" {
		h.replyErr(msg, subj, "instance_id, tenant_id, and user_id are required")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	inst, err := h.store.GetInstance(ctx, req.InstanceID)
	if err != nil {
		h.replyErr(msg, subj, "instance not found: "+err.Error())
		return
	}
	if inst.TenantID != req.TenantID {
		h.replyErr(msg, subj, "instance not found")
		return
	}

	user, err := h.store.GetUser(ctx, req.UserID)
	if err != nil {
		h.replyErr(msg, subj, "user not found: "+err.Error())
		return
	}
	if user.InstanceID != req.InstanceID {
		h.replyErr(msg, subj, "user not found")
		return
	}

	// Forward to agent
	var agentResp struct{ OK bool }
	if err := h.agentRequest(ctx, req.InstanceID, "user.delete", map[string]any{
		"username":  user.Username,
		"databases": user.Databases,
	}, &agentResp); err != nil {
		slog.Error("agent user.delete failed", "user_id", user.ID, "err", err)
		h.m.AgentRequests.WithLabelValues("user.delete", "error").Inc()
		h.replyErr(msg, subj, "delete user: "+err.Error())
		return
	}
	h.m.AgentRequests.WithLabelValues("user.delete", "ok").Inc()

	_ = h.store.SoftDeleteUser(ctx, user.ID)

	h.m.NATSRequests.WithLabelValues(subj, "ok").Inc()
	natsutil.ReplyOK(msg, map[string]string{"status": "deleted"})
}

func (h *Handler) handleUserResetPassword(msg *nats.Msg) {
	const subj = "dbaas.user.reset-password"
	var req userResetPasswordRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.replyErr(msg, subj, "invalid request: "+err.Error())
		return
	}
	if req.InstanceID == "" || req.TenantID == "" || req.Username == "" || req.NewPassword == "" {
		h.replyErr(msg, subj, "instance_id, tenant_id, username, and new_password are required")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	inst, err := h.store.GetInstance(ctx, req.InstanceID)
	if err != nil {
		h.replyErr(msg, subj, "instance not found: "+err.Error())
		return
	}
	if inst.TenantID != req.TenantID {
		h.replyErr(msg, subj, "instance not found")
		return
	}

	// Block reserved usernames
	if req.Username == inst.AdminUser || req.Username == "postgres" {
		h.replyErr(msg, subj, "cannot reset password for reserved user")
		return
	}

	// Forward to agent
	var agentResp struct{ OK bool }
	if err := h.agentRequest(ctx, req.InstanceID, "user.reset-password", map[string]string{
		"username":     req.Username,
		"new_password": req.NewPassword,
	}, &agentResp); err != nil {
		h.m.AgentRequests.WithLabelValues("user.reset-password", "error").Inc()
		h.replyErr(msg, subj, "reset password: "+err.Error())
		return
	}
	h.m.AgentRequests.WithLabelValues("user.reset-password", "ok").Inc()

	h.m.NATSRequests.WithLabelValues(subj, "ok").Inc()
	natsutil.ReplyOK(msg, map[string]string{"status": "ok"})
}
