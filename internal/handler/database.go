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

type databaseCreateRequest struct {
	InstanceID string `json:"instance_id"`
	TenantID   string `json:"tenant_id"`
	Name       string `json:"name"`
	Charset    string `json:"charset,omitempty"`
	Collation  string `json:"collation,omitempty"`
}

type databaseListRequest struct {
	InstanceID string `json:"instance_id"`
	TenantID   string `json:"tenant_id"`
}

type databaseDeleteRequest struct {
	InstanceID string `json:"instance_id"`
	TenantID   string `json:"tenant_id"`
	DatabaseID string `json:"database_id"`
}

// ---------- handlers ----------

func (h *Handler) handleDatabaseCreate(msg *nats.Msg) {
	const subj = "dbaas.database.create"
	var req databaseCreateRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.replyErr(msg, subj, "invalid request: "+err.Error())
		return
	}
	if req.InstanceID == "" || req.TenantID == "" || req.Name == "" {
		h.replyErr(msg, subj, "instance_id, tenant_id, and name are required")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Verify instance exists and is available
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

	// Insert DB record
	db, err := h.store.CreateDatabase(ctx, req.InstanceID, req.TenantID, req.Name, req.Charset, req.Collation)
	if err != nil {
		h.replyErr(msg, subj, "create database: "+err.Error())
		return
	}

	// Forward to agent
	var agentResp struct{ OK bool }
	if err := h.agentRequest(ctx, req.InstanceID, "database.create", map[string]string{
		"name":      req.Name,
		"charset":   db.Charset,
		"collation": db.Collation,
	}, &agentResp); err != nil {
		slog.Error("agent database.create failed", "database_id", db.ID, "err", err)
		_ = h.store.UpdateDatabaseStatus(ctx, db.ID, "error")
		db.Status = "error"
		h.m.AgentRequests.WithLabelValues("database.create", "error").Inc()
		h.m.NATSRequests.WithLabelValues(subj, "ok").Inc()
		natsutil.ReplyOK(msg, db)
		return
	}
	h.m.AgentRequests.WithLabelValues("database.create", "ok").Inc()

	// Update status to active
	_ = h.store.UpdateDatabaseStatus(ctx, db.ID, "active")
	db.Status = "active"

	h.m.NATSRequests.WithLabelValues(subj, "ok").Inc()
	natsutil.ReplyOK(msg, db)
}

func (h *Handler) handleDatabaseList(msg *nats.Msg) {
	const subj = "dbaas.database.list"
	var req databaseListRequest
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

	// Verify ownership
	inst, err := h.store.GetInstance(ctx, req.InstanceID)
	if err != nil {
		h.replyErr(msg, subj, "instance not found: "+err.Error())
		return
	}
	if inst.TenantID != req.TenantID {
		h.replyErr(msg, subj, "instance not found")
		return
	}

	dbs, err := h.store.ListDatabases(ctx, req.InstanceID)
	if err != nil {
		h.replyErr(msg, subj, err.Error())
		return
	}

	h.m.NATSRequests.WithLabelValues(subj, "ok").Inc()
	natsutil.ReplyOK(msg, dbs)
}

func (h *Handler) handleDatabaseDelete(msg *nats.Msg) {
	const subj = "dbaas.database.delete"
	var req databaseDeleteRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.replyErr(msg, subj, "invalid request: "+err.Error())
		return
	}
	if req.InstanceID == "" || req.TenantID == "" || req.DatabaseID == "" {
		h.replyErr(msg, subj, "instance_id, tenant_id, and database_id are required")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Verify ownership
	inst, err := h.store.GetInstance(ctx, req.InstanceID)
	if err != nil {
		h.replyErr(msg, subj, "instance not found: "+err.Error())
		return
	}
	if inst.TenantID != req.TenantID {
		h.replyErr(msg, subj, "instance not found")
		return
	}

	db, err := h.store.GetDatabase(ctx, req.DatabaseID)
	if err != nil {
		h.replyErr(msg, subj, "database not found: "+err.Error())
		return
	}
	if db.InstanceID != req.InstanceID {
		h.replyErr(msg, subj, "database not found")
		return
	}

	// Forward to agent
	var agentResp struct{ OK bool }
	if err := h.agentRequest(ctx, req.InstanceID, "database.delete", map[string]string{
		"name": db.Name,
	}, &agentResp); err != nil {
		slog.Error("agent database.delete failed", "database_id", db.ID, "err", err)
		_ = h.store.UpdateDatabaseStatus(ctx, db.ID, "error")
		h.m.AgentRequests.WithLabelValues("database.delete", "error").Inc()
		h.replyErr(msg, subj, "delete database: "+err.Error())
		return
	}
	h.m.AgentRequests.WithLabelValues("database.delete", "ok").Inc()

	_ = h.store.SoftDeleteDatabase(ctx, db.ID)

	h.m.NATSRequests.WithLabelValues(subj, "ok").Inc()
	natsutil.ReplyOK(msg, map[string]string{"status": "deleted"})
}
