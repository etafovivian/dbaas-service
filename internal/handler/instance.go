package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	sharedcfg "github.com/etafovivian/shared/config"
	"github.com/etafovivian/shared/natsutil"
	"github.com/etafovivian/dbaas-service/internal/engine"
	"github.com/etafovivian/dbaas-service/internal/store"
	"github.com/nats-io/nats.go"
)

// ---------- request types ----------

type instanceCreateRequest struct {
	TenantID      string            `json:"tenant_id"`
	EnvironmentID string            `json:"environment_id,omitempty"`
	Name          string            `json:"name"`
	Engine        string            `json:"engine"`
	EngineVersion string            `json:"engine_version"`
	Tier          string            `json:"tier"`
	FlavorID      string            `json:"flavor_id"`
	DiskSizeGB    int               `json:"disk_size_gb"`
	NetworkID     string            `json:"network_id"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

type instanceGetRequest struct {
	InstanceID string `json:"instance_id"`
}

type instanceListRequest struct {
	TenantID      string `json:"tenant_id"`
	EnvironmentID string `json:"environment_id,omitempty"`
}

type instanceDeleteRequest struct {
	InstanceID string `json:"instance_id"`
	TenantID   string `json:"tenant_id"`
}

// ---------- agent callback types ----------

type agentReadyRequest struct {
	InstanceID string `json:"instance_id"`
	NodeID     string `json:"node_id"`
}

type agentHeartbeatRequest struct {
	InstanceID        string `json:"instance_id"`
	NodeID            string `json:"node_id"`
	Role              string `json:"role"`
	ReplicationLagBytes int64  `json:"replication_lag_bytes"`
}

// ---------- cross-service response types ----------

type tenantGetResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

type osServerResponse struct {
	ID        string                       `json:"id"`
	Status    string                       `json:"status"`
	Addresses map[string][]osServerAddress `json:"addresses"`
}

type osServerAddress struct {
	Addr    string `json:"addr"`
	Version int    `json:"version"`
	Type    string `json:"OS-EXT-IPS:type"`
}

type osVolumeResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type osProjectResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ---------- instance response ----------

type instanceResponse struct {
	ID            string            `json:"id"`
	TenantID      string            `json:"tenant_id"`
	EnvironmentID string            `json:"environment_id"`
	Name          string            `json:"name"`
	Engine        string            `json:"engine"`
	EngineVersion string            `json:"engine_version"`
	Tier          string            `json:"tier"`
	FlavorID      string            `json:"flavor_id"`
	DiskSizeGB    int               `json:"disk_size_gb"`
	Status        string            `json:"status"`
	Host          *string           `json:"host,omitempty"`
	Port          int               `json:"port"`
	AdminUser     string            `json:"admin_user"`
	Metadata      map[string]string `json:"metadata"`
	Nodes         []*store.InstanceNode `json:"nodes,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

func instanceToResponse(inst *store.Instance) *instanceResponse {
	return &instanceResponse{
		ID:            inst.ID,
		TenantID:      inst.TenantID,
		EnvironmentID: inst.EnvironmentID,
		Name:          inst.Name,
		Engine:        inst.Engine,
		EngineVersion: inst.EngineVersion,
		Tier:          inst.Tier,
		FlavorID:      inst.FlavorID,
		DiskSizeGB:    inst.DiskSizeGB,
		Status:        inst.Status,
		Host:          inst.Host,
		Port:          inst.Port,
		AdminUser:     inst.AdminUser,
		Metadata:      inst.Metadata,
		Nodes:         inst.Nodes,
		CreatedAt:     inst.CreatedAt,
		UpdatedAt:     inst.UpdatedAt,
	}
}

// ---------- handlers ----------

func (h *Handler) handleInstanceCreate(msg *nats.Msg) {
	const subj = "dbaas.instance.create"
	var req instanceCreateRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.replyErr(msg, subj, "invalid request: "+err.Error())
		return
	}
	if req.TenantID == "" || req.Name == "" || req.FlavorID == "" || req.DiskSizeGB <= 0 || req.NetworkID == "" {
		h.replyErr(msg, subj, "tenant_id, name, flavor_id, network_id, and disk_size_gb (> 0) are required")
		return
	}
	if req.EnvironmentID == "" {
		req.EnvironmentID = sharedcfg.DefaultEnvironmentID
	}
	if req.Engine == "" {
		req.Engine = "postgres"
	}
	// Validate engine is registered
	eng, engErr := h.engines.Get(req.Engine)
	if engErr != nil {
		h.replyErr(msg, subj, engErr.Error())
		return
	}
	if req.EngineVersion == "" {
		req.EngineVersion = eng.DefaultVersion()
	}
	if req.Tier == "" {
		req.Tier = "single"
	}
	if req.Tier != "single" && req.Tier != "ha" {
		h.replyErr(msg, subj, "tier must be 'single' or 'ha'")
		return
	}

	// Generate admin password
	adminPass, err := generatePassword(32)
	if err != nil {
		h.replyErr(msg, subj, "generate password: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Insert instance with status='creating'
	inst, err := h.store.CreateInstance(ctx, req.TenantID, req.EnvironmentID, req.Name,
		req.Engine, req.EngineVersion, req.Tier, req.FlavorID, req.DiskSizeGB, adminPass, req.Metadata)
	if err != nil {
		h.replyErr(msg, subj, "create instance: "+err.Error())
		return
	}

	// Reply immediately — provisioning continues in background
	h.m.NATSRequests.WithLabelValues(subj, "ok").Inc()
	natsutil.ReplyOK(msg, instanceToResponse(inst))

	// Launch async provisioning
	go h.provisionInstance(inst, req.NetworkID)
}

// provisionInstance handles the asynchronous provisioning of VMs and volumes.
func (h *Handler) provisionInstance(inst *store.Instance, networkID string) {
	start := time.Now()
	log := slog.With("instance_id", inst.ID, "name", inst.Name, "tier", inst.Tier)
	log.Info("starting instance provisioning")

	ctx := h.shutdownCtx

	// 1. Resolve tenant name for project naming
	var tenant tenantGetResponse
	if err := h.iamRequest("iam.tenant.get", map[string]string{"tenant_id": inst.TenantID}, &tenant); err != nil {
		log.Error("failed to resolve tenant", "err", err)
		_ = h.store.UpdateInstanceStatus(ctx, inst.ID, "error")
		return
	}

	// 2. Ensure dbaas Keystone project exists
	projectName := fmt.Sprintf("dbaas-%s", tenant.Name)
	projectID, err := h.ensureDbaasProject(ctx, projectName, inst.TenantID, inst.EnvironmentID)
	if err != nil {
		log.Error("failed to ensure dbaas project", "err", err)
		_ = h.store.UpdateInstanceStatus(ctx, inst.ID, "error")
		return
	}
	if err := h.store.UpdateInstanceProjectID(ctx, inst.ID, projectID); err != nil {
		log.Error("failed to update project id", "err", err)
	}

	// 3. Create security group for this instance
	secgroupID, sgErr := h.createInstanceSecgroup(ctx, inst)
	if sgErr != nil {
		log.Error("failed to create security group", "err", sgErr)
		_ = h.store.UpdateInstanceStatus(ctx, inst.ID, "error")
		return
	}
	if err := h.store.UpdateInstanceSecgroupID(ctx, inst.ID, secgroupID); err != nil {
		log.Error("failed to update secgroup id", "err", err)
	}

	// 4. Determine node count
	nodeCount := 1
	if inst.Tier == "ha" {
		nodeCount = 3
	}

	// 5. Provision each node
	var nodes []*store.InstanceNode
	for i := range nodeCount {
		node, nodeErr := h.provisionNode(ctx, inst, i, networkID)
		if nodeErr != nil {
			log.Error("failed to provision node", "node_index", i, "err", nodeErr)
			_ = h.store.UpdateInstanceStatus(ctx, inst.ID, "error")
			return
		}
		nodes = append(nodes, node)
	}

	// 5. Wait for all agents to report ready
	log.Info("waiting for agents to connect", "node_count", nodeCount)
	if err := h.waitForAgents(ctx, inst.ID, nodeCount); err != nil {
		log.Error("agents did not connect in time", "err", err)
		_ = h.store.UpdateInstanceStatus(ctx, inst.ID, "error")
		return
	}

	// 6. Set primary role on first node
	if len(nodes) > 0 {
		_ = h.store.UpdateNodeRole(ctx, nodes[0].ID, "primary")
	}

	// 7. Set instance as available with connection host
	host := ""
	if nodes[0].PrivateIP != nil {
		host = *nodes[0].PrivateIP
	}
	if err := h.store.UpdateInstanceHost(ctx, inst.ID, host); err != nil {
		log.Error("failed to update instance host", "err", err)
		return
	}

	h.m.ProvisionDuration.WithLabelValues(inst.Tier).Observe(time.Since(start).Seconds())
	log.Info("instance provisioned successfully", "host", host, "duration", time.Since(start))
}

// ensureDbaasProject creates or reuses a Keystone project named dbaas-{org-name}.
func (h *Handler) ensureDbaasProject(ctx context.Context, projectName, tenantID, environmentID string) (string, error) {
	// Try to list existing projects matching the name
	var projects []osProjectResponse
	listErr := h.osRequest("openstack.keystone.list-projects", map[string]string{
		"name":           projectName,
		"tenant_id":      tenantID,
		"environment_id": environmentID,
	}, &projects)
	if listErr == nil && len(projects) > 0 {
		return projects[0].ID, nil
	}

	// Project doesn't exist — create it
	var project osProjectResponse
	if err := h.osRequest("openstack.keystone.create-project", map[string]any{
		"name":           projectName,
		"description":    fmt.Sprintf("DBaaS resources for tenant %s", tenantID),
		"tenant_id":      tenantID,
		"environment_id": environmentID,
	}, &project); err != nil {
		return "", fmt.Errorf("create project %s: %w", projectName, err)
	}
	return project.ID, nil
}

// createInstanceSecgroup creates a security group for a dbaas instance with default rules.
func (h *Handler) createInstanceSecgroup(ctx context.Context, inst *store.Instance) (string, error) {
	sgName := fmt.Sprintf("dbaas-sg-%s", inst.ID)

	// Resolve engine for default port
	eng, err := h.engines.Get(inst.Engine)
	if err != nil {
		return "", fmt.Errorf("get engine: %w", err)
	}
	dbPort := eng.DefaultPort()

	// Create security group
	var sgResp struct {
		ID string `json:"id"`
	}
	if err := h.osRequest("openstack.neutron.create-security-group", map[string]any{
		"name":           sgName,
		"description":    fmt.Sprintf("DBaaS firewall for instance %s", inst.Name),
		"tenant_id":      inst.TenantID,
		"environment_id": inst.EnvironmentID,
	}, &sgResp); err != nil {
		return "", fmt.Errorf("create security group: %w", err)
	}

	// Add default rules: allow DB port and pgBouncer port from 0.0.0.0/0
	// Tenants can restrict via dbaas.firewall.add-rule later
	defaultRules := []map[string]any{
		{
			"security_group_id": sgResp.ID,
			"direction":         "ingress",
			"protocol":          "tcp",
			"port_range_min":    dbPort,
			"port_range_max":    dbPort,
			"remote_ip_prefix":  "0.0.0.0/0",
			"description":       "Allow database port",
			"tenant_id":         inst.TenantID,
			"environment_id":    inst.EnvironmentID,
		},
		{
			"security_group_id": sgResp.ID,
			"direction":         "ingress",
			"protocol":          "tcp",
			"port_range_min":    6432,
			"port_range_max":    6432,
			"remote_ip_prefix":  "0.0.0.0/0",
			"description":       "Allow connection pooler port",
			"tenant_id":         inst.TenantID,
			"environment_id":    inst.EnvironmentID,
		},
	}

	for _, rule := range defaultRules {
		if err := h.osRequest("openstack.neutron.create-security-group-rule", rule, nil); err != nil {
			slog.Warn("failed to create default sg rule", "err", err)
		}
	}

	return sgResp.ID, nil
}

// provisionNode creates a single VM + Cinder volume for a database node.
func (h *Handler) provisionNode(ctx context.Context, inst *store.Instance, nodeIndex int, networkID string) (*store.InstanceNode, error) {
	log := slog.With("instance_id", inst.ID, "node_index", nodeIndex)

	// Insert node row
	node, err := h.store.CreateNode(ctx, inst.ID, nodeIndex)
	if err != nil {
		return nil, fmt.Errorf("create node row: %w", err)
	}

	// Create Cinder volume
	volumeName := fmt.Sprintf("dbaas-%s-node-%d", inst.ID, nodeIndex)
	var volResp osVolumeResponse
	if err := h.osRequest("openstack.cinder.create-volume", map[string]any{
		"name":           volumeName,
		"size_gb":        inst.DiskSizeGB,
		"tenant_id":      inst.TenantID,
		"environment_id": inst.EnvironmentID,
	}, &volResp); err != nil {
		h.m.OpenStackRequests.WithLabelValues("create-volume", "error").Inc()
		_ = h.store.UpdateNodeStatus(ctx, node.ID, "error")
		return nil, fmt.Errorf("create volume: %w", err)
	}
	h.m.OpenStackRequests.WithLabelValues("create-volume", "ok").Inc()
	log.Info("volume created", "volume_id", volResp.ID)

	// Render cloud-init using the engine
	eng, engErr := h.engines.Get(inst.Engine)
	if engErr != nil {
		_ = h.store.UpdateNodeStatus(ctx, node.ID, "error")
		return nil, fmt.Errorf("get engine: %w", engErr)
	}
	cloudInit, err := renderCloudInit(eng, engine.InstanceConfig{
		InstanceID:    inst.ID,
		NodeID:        node.ID,
		NodeIndex:     nodeIndex,
		EngineVersion: inst.EngineVersion,
		AdminUser:     inst.AdminUser,
		AdminPassword: inst.AdminPassword,
		Tier:          inst.Tier,
		AgentVersion:  h.agentVersion,
	}, h.nc.Opts.Url)
	if err != nil {
		_ = h.store.UpdateNodeStatus(ctx, node.ID, "error")
		return nil, fmt.Errorf("render cloud-init: %w", err)
	}

	// Create VM
	serverName := fmt.Sprintf("dbaas-%s-node-%d", inst.ID, nodeIndex)
	var serverResp osServerResponse
	if err := h.osRequest("openstack.nova.create-server", map[string]any{
		"name":           serverName,
		"flavor_id":      inst.FlavorID,
		"image_id":       h.imageID,
		"network_ids":    []string{networkID, h.mgmtNetID},
		"user_data":      cloudInit,
		"config_drive":   true,
		"tenant_id":      inst.TenantID,
		"environment_id": inst.EnvironmentID,
		"metadata": map[string]string{
			"dbaas_instance_id": inst.ID,
			"dbaas_node_id":     node.ID,
		},
	}, &serverResp); err != nil {
		h.m.OpenStackRequests.WithLabelValues("create-server", "error").Inc()
		_ = h.store.UpdateNodeStatus(ctx, node.ID, "error")
		return nil, fmt.Errorf("create server: %w", err)
	}
	h.m.OpenStackRequests.WithLabelValues("create-server", "ok").Inc()
	log.Info("server created", "server_id", serverResp.ID)

	// Store OpenStack IDs
	if err := h.store.UpdateNodeOpenStackIDs(ctx, node.ID, serverResp.ID, volResp.ID); err != nil {
		return nil, fmt.Errorf("update node openstack ids: %w", err)
	}

	// Poll for VM to become ACTIVE and extract IPs
	privateIP, mgmtIP, err := h.waitForServer(ctx, serverResp.ID, inst.TenantID, inst.EnvironmentID)
	if err != nil {
		_ = h.store.UpdateNodeStatus(ctx, node.ID, "error")
		return nil, fmt.Errorf("wait for server: %w", err)
	}
	log.Info("server active", "private_ip", privateIP, "mgmt_ip", mgmtIP)

	if err := h.store.UpdateNodeIPs(ctx, node.ID, privateIP, mgmtIP); err != nil {
		return nil, fmt.Errorf("update node ips: %w", err)
	}
	node.PrivateIP = &privateIP
	node.MgmtIP = &mgmtIP

	// Attach volume to server
	if err := h.osRequest("openstack.cinder.attach-volume", map[string]any{
		"volume_id":      volResp.ID,
		"server_id":      serverResp.ID,
		"tenant_id":      inst.TenantID,
		"environment_id": inst.EnvironmentID,
	}, nil); err != nil {
		h.m.OpenStackRequests.WithLabelValues("attach-volume", "error").Inc()
		_ = h.store.UpdateNodeStatus(ctx, node.ID, "error")
		return nil, fmt.Errorf("attach volume: %w", err)
	}
	h.m.OpenStackRequests.WithLabelValues("attach-volume", "ok").Inc()
	log.Info("volume attached")

	return node, nil
}

// waitForServer polls openstack.nova.get-server until the VM is ACTIVE, then extracts IPs.
func (h *Handler) waitForServer(ctx context.Context, serverID, tenantID, environmentID string) (privateIP, mgmtIP string, err error) {
	for i := range 60 { // 60 * 5s = 5 minutes max
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		default:
		}

		if i > 0 {
			time.Sleep(5 * time.Second)
		}

		var server osServerResponse
		if getErr := h.osRequest("openstack.nova.get-server", map[string]string{
			"server_id":      serverID,
			"tenant_id":      tenantID,
			"environment_id": environmentID,
		}, &server); getErr != nil {
			slog.Warn("get-server failed, retrying", "attempt", i+1, "err", getErr)
			continue
		}

		if server.Status == "ERROR" {
			return "", "", fmt.Errorf("server entered ERROR state")
		}
		if server.Status != "ACTIVE" {
			continue
		}

		// Extract IPs from addresses map
		// First network = tenant, second = mgmt
		netIdx := 0
		for _, addrs := range server.Addresses {
			for _, a := range addrs {
				if a.Version == 4 && a.Type == "fixed" {
					if netIdx == 0 {
						privateIP = a.Addr
					} else {
						mgmtIP = a.Addr
					}
				}
			}
			netIdx++
		}
		if privateIP != "" {
			return privateIP, mgmtIP, nil
		}
	}
	return "", "", fmt.Errorf("server %s did not become ACTIVE within timeout", serverID)
}

// waitForAgents polls the DB until all nodes have agent_connected=true.
func (h *Handler) waitForAgents(ctx context.Context, instanceID string, expectedCount int) error {
	for range 120 { // 120 * 5s = 10 minutes max
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		count, err := h.store.CountReadyNodes(ctx, instanceID)
		if err != nil {
			slog.Warn("count ready nodes failed", "err", err)
		} else if count >= expectedCount {
			return nil
		}

		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("timeout waiting for %d agents to connect", expectedCount)
}

// ---------- get / list / delete ----------

func (h *Handler) handleInstanceGet(msg *nats.Msg) {
	const subj = "dbaas.instance.get"
	var req instanceGetRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.replyErr(msg, subj, "invalid request: "+err.Error())
		return
	}
	if req.InstanceID == "" {
		h.replyErr(msg, subj, "instance_id is required")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inst, err := h.store.GetInstance(ctx, req.InstanceID)
	if err != nil {
		h.replyErr(msg, subj, err.Error())
		return
	}

	// Fetch nodes
	nodes, err := h.store.GetNodesByInstance(ctx, req.InstanceID)
	if err != nil {
		h.replyErr(msg, subj, "get nodes: "+err.Error())
		return
	}
	inst.Nodes = nodes

	h.m.NATSRequests.WithLabelValues(subj, "ok").Inc()
	natsutil.ReplyOK(msg, instanceToResponse(inst))
}

func (h *Handler) handleInstanceList(msg *nats.Msg) {
	const subj = "dbaas.instance.list"
	var req instanceListRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.replyErr(msg, subj, "invalid request: "+err.Error())
		return
	}
	if req.TenantID == "" {
		h.replyErr(msg, subj, "tenant_id is required")
		return
	}
	if req.EnvironmentID == "" {
		req.EnvironmentID = sharedcfg.DefaultEnvironmentID
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	instances, err := h.store.ListInstances(ctx, req.TenantID, req.EnvironmentID)
	if err != nil {
		h.replyErr(msg, subj, err.Error())
		return
	}

	// Convert to response objects (without nodes for list view)
	resp := make([]*instanceResponse, len(instances))
	for i, inst := range instances {
		resp[i] = instanceToResponse(inst)
	}

	h.m.NATSRequests.WithLabelValues(subj, "ok").Inc()
	natsutil.ReplyOK(msg, resp)
}

func (h *Handler) handleInstanceDelete(msg *nats.Msg) {
	const subj = "dbaas.instance.delete"
	var req instanceDeleteRequest
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
		h.replyErr(msg, subj, err.Error())
		return
	}
	if inst.TenantID != req.TenantID {
		h.replyErr(msg, subj, "instance not found")
		return
	}

	// Mark as deleting
	if err := h.store.UpdateInstanceStatus(ctx, req.InstanceID, "deleting"); err != nil {
		h.replyErr(msg, subj, "update status: "+err.Error())
		return
	}

	h.m.NATSRequests.WithLabelValues(subj, "ok").Inc()
	natsutil.ReplyOK(msg, map[string]string{"status": "deleting"})

	// Async deletion
	go h.deleteInstance(inst)
}

// deleteInstance terminates VMs and volumes, then soft-deletes the instance.
func (h *Handler) deleteInstance(inst *store.Instance) {
	log := slog.With("instance_id", inst.ID)
	ctx := h.shutdownCtx

	nodes, err := h.store.GetNodesByInstance(ctx, inst.ID)
	if err != nil {
		log.Error("failed to get nodes for deletion", "err", err)
		return
	}

	for _, node := range nodes {
		// Terminate VM
		if node.OpenstackServerID != nil && *node.OpenstackServerID != "" {
			if err := h.osRequest("openstack.nova.terminate-server", map[string]string{
				"server_id":      *node.OpenstackServerID,
				"tenant_id":      inst.TenantID,
				"environment_id": inst.EnvironmentID,
			}, nil); err != nil {
				log.Error("failed to terminate server", "server_id", *node.OpenstackServerID, "err", err)
			} else {
				h.m.OpenStackRequests.WithLabelValues("terminate-server", "ok").Inc()
			}
		}

		// Delete volume
		if node.OpenstackVolumeID != nil && *node.OpenstackVolumeID != "" {
			// Wait a bit for server to terminate before deleting volume
			time.Sleep(5 * time.Second)
			if err := h.osRequest("openstack.cinder.delete-volume", map[string]string{
				"volume_id":      *node.OpenstackVolumeID,
				"tenant_id":      inst.TenantID,
				"environment_id": inst.EnvironmentID,
			}, nil); err != nil {
				log.Error("failed to delete volume", "volume_id", *node.OpenstackVolumeID, "err", err)
			} else {
				h.m.OpenStackRequests.WithLabelValues("delete-volume", "ok").Inc()
			}
		}

		_ = h.store.UpdateNodeStatus(ctx, node.ID, "deleted")
	}

	if err := h.store.SoftDeleteInstance(ctx, inst.ID); err != nil {
		log.Error("failed to soft delete instance", "err", err)
		return
	}
	log.Info("instance deleted successfully")
}

// ---------- agent callbacks ----------

func (h *Handler) handleAgentReady(msg *nats.Msg) {
	const subj = "dbaas.agent.ready"
	var req agentReadyRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.replyErr(msg, subj, "invalid request: "+err.Error())
		return
	}
	if req.NodeID == "" {
		h.replyErr(msg, subj, "node_id is required")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.store.UpdateNodeAgentConnected(ctx, req.NodeID, true); err != nil {
		h.replyErr(msg, subj, "update agent connected: "+err.Error())
		return
	}

	slog.Info("agent ready", "node_id", req.NodeID, "instance_id", req.InstanceID)
	h.m.NATSRequests.WithLabelValues(subj, "ok").Inc()
	natsutil.ReplyOK(msg, map[string]bool{"ok": true})
}

func (h *Handler) handleAgentHeartbeat(msg *nats.Msg) {
	const subj = "dbaas.agent.heartbeat"
	var req agentHeartbeatRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.replyErr(msg, subj, "invalid request: "+err.Error())
		return
	}
	if req.NodeID == "" {
		h.replyErr(msg, subj, "node_id is required")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.store.UpdateNodeHeartbeat(ctx, req.NodeID, req.Role); err != nil {
		h.replyErr(msg, subj, "update heartbeat: "+err.Error())
		return
	}

	h.m.NATSRequests.WithLabelValues(subj, "ok").Inc()
	natsutil.ReplyOK(msg, map[string]bool{"ok": true})
}
