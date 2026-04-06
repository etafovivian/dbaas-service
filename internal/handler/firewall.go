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

type firewallAddRuleRequest struct {
	InstanceID     string `json:"instance_id"`
	TenantID       string `json:"tenant_id"`
	Direction      string `json:"direction"`       // ingress or egress
	Protocol       string `json:"protocol"`        // tcp, udp, icmp
	PortRangeMin   *int   `json:"port_range_min"`
	PortRangeMax   *int   `json:"port_range_max"`
	RemoteIPPrefix string `json:"remote_ip_prefix"` // CIDR, e.g. "10.0.0.0/24"
	Description    string `json:"description"`
}

type firewallListRulesRequest struct {
	InstanceID string `json:"instance_id"`
	TenantID   string `json:"tenant_id"`
}

type firewallRemoveRuleRequest struct {
	InstanceID string `json:"instance_id"`
	TenantID   string `json:"tenant_id"`
	RuleID     string `json:"rule_id"`
}

// ---------- OpenStack response types ----------

type osSecgroupRuleResponse struct {
	ID string `json:"id"`
}

// ---------- handlers ----------

func (h *Handler) handleFirewallAddRule(msg *nats.Msg) {
	const subj = "dbaas.firewall.add-rule"
	var req firewallAddRuleRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.replyErr(msg, subj, "invalid request: "+err.Error())
		return
	}
	if req.InstanceID == "" || req.TenantID == "" || req.RemoteIPPrefix == "" {
		h.replyErr(msg, subj, "instance_id, tenant_id, and remote_ip_prefix are required")
		return
	}
	if req.Direction == "" {
		req.Direction = "ingress"
	}
	if req.Protocol == "" {
		req.Protocol = "tcp"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Verify instance and get security group ID
	inst, err := h.store.GetInstance(ctx, req.InstanceID)
	if err != nil {
		h.replyErr(msg, subj, "instance not found: "+err.Error())
		return
	}
	if inst.TenantID != req.TenantID {
		h.replyErr(msg, subj, "instance not found")
		return
	}
	if inst.OpenstackSecgroupID == nil || *inst.OpenstackSecgroupID == "" {
		h.replyErr(msg, subj, "instance has no security group")
		return
	}

	// Insert firewall rule record
	rule, err := h.store.CreateFirewallRule(ctx, req.InstanceID, req.TenantID,
		req.Direction, req.Protocol, req.PortRangeMin, req.PortRangeMax, req.RemoteIPPrefix, req.Description)
	if err != nil {
		h.replyErr(msg, subj, "create rule: "+err.Error())
		return
	}

	// Create OpenStack security group rule
	var osResp osSecgroupRuleResponse
	if err := h.osRequest("openstack.neutron.create-security-group-rule", map[string]any{
		"security_group_id": *inst.OpenstackSecgroupID,
		"direction":         req.Direction,
		"protocol":          req.Protocol,
		"port_range_min":    req.PortRangeMin,
		"port_range_max":    req.PortRangeMax,
		"remote_ip_prefix":  req.RemoteIPPrefix,
		"description":       req.Description,
		"tenant_id":         req.TenantID,
		"environment_id":    inst.EnvironmentID,
	}, &osResp); err != nil {
		slog.Error("create security group rule failed", "rule_id", rule.ID, "err", err)
		h.m.OpenStackRequests.WithLabelValues("create-sg-rule", "error").Inc()
		h.replyErr(msg, subj, "create openstack rule: "+err.Error())
		return
	}
	h.m.OpenStackRequests.WithLabelValues("create-sg-rule", "ok").Inc()

	_ = h.store.UpdateFirewallRuleOpenstackID(ctx, rule.ID, osResp.ID)
	rule.Status = "active"
	rule.OpenstackRuleID = &osResp.ID

	h.m.NATSRequests.WithLabelValues(subj, "ok").Inc()
	natsutil.ReplyOK(msg, rule)
}

func (h *Handler) handleFirewallListRules(msg *nats.Msg) {
	const subj = "dbaas.firewall.list-rules"
	var req firewallListRulesRequest
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

	rules, err := h.store.ListFirewallRules(ctx, req.InstanceID)
	if err != nil {
		h.replyErr(msg, subj, err.Error())
		return
	}

	h.m.NATSRequests.WithLabelValues(subj, "ok").Inc()
	natsutil.ReplyOK(msg, rules)
}

func (h *Handler) handleFirewallRemoveRule(msg *nats.Msg) {
	const subj = "dbaas.firewall.remove-rule"
	var req firewallRemoveRuleRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.replyErr(msg, subj, "invalid request: "+err.Error())
		return
	}
	if req.InstanceID == "" || req.TenantID == "" || req.RuleID == "" {
		h.replyErr(msg, subj, "instance_id, tenant_id, and rule_id are required")
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

	rule, err := h.store.GetFirewallRule(ctx, req.RuleID)
	if err != nil {
		h.replyErr(msg, subj, "rule not found: "+err.Error())
		return
	}
	if rule.InstanceID != req.InstanceID {
		h.replyErr(msg, subj, "rule not found")
		return
	}

	// Delete OpenStack security group rule
	if rule.OpenstackRuleID != nil && *rule.OpenstackRuleID != "" {
		if err := h.osRequest("openstack.neutron.delete-security-group-rule", map[string]string{
			"rule_id":        *rule.OpenstackRuleID,
			"tenant_id":      req.TenantID,
			"environment_id": inst.EnvironmentID,
		}, nil); err != nil {
			slog.Error("delete security group rule failed", "rule_id", rule.ID, "err", err)
			h.m.OpenStackRequests.WithLabelValues("delete-sg-rule", "error").Inc()
			h.replyErr(msg, subj, "delete openstack rule: "+err.Error())
			return
		}
		h.m.OpenStackRequests.WithLabelValues("delete-sg-rule", "ok").Inc()
	}

	_ = h.store.SoftDeleteFirewallRule(ctx, rule.ID)

	h.m.NATSRequests.WithLabelValues(subj, "ok").Inc()
	natsutil.ReplyOK(msg, map[string]string{"status": "deleted"})
}
