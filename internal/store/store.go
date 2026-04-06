// Package store provides PostgreSQL persistence for the dbaas-service.
// All queries use pgx/v5 directly — no ORM.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store wraps a pgx pool and provides typed query methods.
type Store struct {
	pool *pgxpool.Pool
}

// New creates a Store backed by the given connection pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ---------- Model types ----------

// Instance represents a managed database instance.
type Instance struct {
	ID              string            `json:"id"`
	TenantID        string            `json:"tenant_id"`
	EnvironmentID   string            `json:"environment_id"`
	Name            string            `json:"name"`
	Engine          string            `json:"engine"`
	EngineVersion   string            `json:"engine_version"`
	Tier            string            `json:"tier"`
	FlavorID        string            `json:"flavor_id"`
	DiskSizeGB      int               `json:"disk_size_gb"`
	Status          string            `json:"status"`
	Host            *string           `json:"host,omitempty"`
	Port            int               `json:"port"`
	AdminUser       string            `json:"admin_user"`
	AdminPassword   string            `json:"-"` // never exposed in API responses
	DbaasProjectID  *string           `json:"dbaas_project_id,omitempty"`
	BackupBucket    *string           `json:"backup_bucket,omitempty"`
	Metadata        map[string]string `json:"metadata"`
	Nodes           []*InstanceNode   `json:"nodes,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// InstanceNode represents a single VM backing a database instance.
type InstanceNode struct {
	ID                string     `json:"id"`
	InstanceID        string     `json:"instance_id"`
	NodeIndex         int        `json:"node_index"`
	Role              string     `json:"role"`
	OpenstackServerID *string    `json:"openstack_server_id,omitempty"`
	OpenstackVolumeID *string    `json:"openstack_volume_id,omitempty"`
	PrivateIP         *string    `json:"private_ip,omitempty"`
	MgmtIP            *string    `json:"mgmt_ip,omitempty"`
	Status            string     `json:"status"`
	AgentConnected    bool       `json:"agent_connected"`
	LastHeartbeat     *time.Time `json:"last_heartbeat,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// ---------- Instance queries ----------

// CreateInstance inserts a new instance with status 'creating' and returns it.
func (s *Store) CreateInstance(ctx context.Context, tenantID, environmentID, name, engine, engineVersion, tier, flavorID string, diskSizeGB int, adminPassword string, metadata map[string]string) (*Instance, error) {
	if metadata == nil {
		metadata = make(map[string]string)
	}
	inst := &Instance{}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO instances (tenant_id, environment_id, name, engine, engine_version, tier, flavor_id, disk_size_gb, admin_password, metadata, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'creating')
		 RETURNING id, tenant_id, environment_id, name, engine, engine_version, tier, flavor_id, disk_size_gb,
		           status, host, port, admin_user, admin_password, dbaas_project_id, backup_bucket, metadata, created_at, updated_at`,
		tenantID, environmentID, name, engine, engineVersion, tier, flavorID, diskSizeGB, adminPassword, metadata,
	).Scan(&inst.ID, &inst.TenantID, &inst.EnvironmentID, &inst.Name, &inst.Engine, &inst.EngineVersion,
		&inst.Tier, &inst.FlavorID, &inst.DiskSizeGB, &inst.Status, &inst.Host, &inst.Port,
		&inst.AdminUser, &inst.AdminPassword, &inst.DbaasProjectID, &inst.BackupBucket, &inst.Metadata,
		&inst.CreatedAt, &inst.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert instance: %w", err)
	}
	return inst, nil
}

// GetInstance retrieves an instance by ID (excludes deleted), without nodes.
func (s *Store) GetInstance(ctx context.Context, id string) (*Instance, error) {
	inst := &Instance{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, tenant_id, environment_id, name, engine, engine_version, tier, flavor_id, disk_size_gb,
		        status, host, port, admin_user, admin_password, dbaas_project_id, backup_bucket, metadata, created_at, updated_at
		 FROM instances WHERE id = $1 AND status != 'deleted'`, id,
	).Scan(&inst.ID, &inst.TenantID, &inst.EnvironmentID, &inst.Name, &inst.Engine, &inst.EngineVersion,
		&inst.Tier, &inst.FlavorID, &inst.DiskSizeGB, &inst.Status, &inst.Host, &inst.Port,
		&inst.AdminUser, &inst.AdminPassword, &inst.DbaasProjectID, &inst.BackupBucket, &inst.Metadata,
		&inst.CreatedAt, &inst.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get instance %s: %w", id, err)
	}
	return inst, nil
}

// ListInstances returns all instances for a tenant and environment (excludes deleted).
func (s *Store) ListInstances(ctx context.Context, tenantID, environmentID string) ([]*Instance, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, tenant_id, environment_id, name, engine, engine_version, tier, flavor_id, disk_size_gb,
		        status, host, port, admin_user, dbaas_project_id, backup_bucket, metadata, created_at, updated_at
		 FROM instances WHERE tenant_id = $1 AND environment_id = $2 AND status != 'deleted' ORDER BY created_at`, tenantID, environmentID)
	if err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}
	defer rows.Close()

	var instances []*Instance
	for rows.Next() {
		inst := &Instance{}
		if err := rows.Scan(&inst.ID, &inst.TenantID, &inst.EnvironmentID, &inst.Name, &inst.Engine, &inst.EngineVersion,
			&inst.Tier, &inst.FlavorID, &inst.DiskSizeGB, &inst.Status, &inst.Host, &inst.Port,
			&inst.AdminUser, &inst.DbaasProjectID, &inst.BackupBucket, &inst.Metadata,
			&inst.CreatedAt, &inst.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan instance: %w", err)
		}
		instances = append(instances, inst)
	}
	return instances, rows.Err()
}

// UpdateInstanceStatus sets the status of an instance.
func (s *Store) UpdateInstanceStatus(ctx context.Context, id, status string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE instances SET status = $1, updated_at = NOW() WHERE id = $2`, status, id)
	if err != nil {
		return fmt.Errorf("update instance status: %w", err)
	}
	return nil
}

// UpdateInstanceHost sets the connection host and marks the instance as available.
func (s *Store) UpdateInstanceHost(ctx context.Context, id, host string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE instances SET host = $1, status = 'available', updated_at = NOW() WHERE id = $2`, host, id)
	if err != nil {
		return fmt.Errorf("update instance host: %w", err)
	}
	return nil
}

// UpdateInstanceProjectID sets the Keystone project ID for the dbaas project.
func (s *Store) UpdateInstanceProjectID(ctx context.Context, id, projectID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE instances SET dbaas_project_id = $1, updated_at = NOW() WHERE id = $2`, projectID, id)
	if err != nil {
		return fmt.Errorf("update instance project id: %w", err)
	}
	return nil
}

// UpdateInstanceBackupBucket sets the S3 backup bucket name.
func (s *Store) UpdateInstanceBackupBucket(ctx context.Context, id, bucket string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE instances SET backup_bucket = $1, updated_at = NOW() WHERE id = $2`, bucket, id)
	if err != nil {
		return fmt.Errorf("update instance backup bucket: %w", err)
	}
	return nil
}

// SoftDeleteInstance marks an instance as deleted.
func (s *Store) SoftDeleteInstance(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE instances SET status = 'deleted', deleted_at = NOW(), updated_at = NOW() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("soft delete instance: %w", err)
	}
	return nil
}

// ---------- Instance Node queries ----------

// CreateNode inserts a new instance node with status 'creating'.
func (s *Store) CreateNode(ctx context.Context, instanceID string, nodeIndex int) (*InstanceNode, error) {
	node := &InstanceNode{}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO instance_nodes (instance_id, node_index, status)
		 VALUES ($1, $2, 'creating')
		 RETURNING id, instance_id, node_index, role, openstack_server_id, openstack_volume_id,
		           private_ip, mgmt_ip, status, agent_connected, last_heartbeat, created_at, updated_at`,
		instanceID, nodeIndex,
	).Scan(&node.ID, &node.InstanceID, &node.NodeIndex, &node.Role, &node.OpenstackServerID, &node.OpenstackVolumeID,
		&node.PrivateIP, &node.MgmtIP, &node.Status, &node.AgentConnected, &node.LastHeartbeat,
		&node.CreatedAt, &node.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert instance node: %w", err)
	}
	return node, nil
}

// GetNodesByInstance returns all nodes for an instance.
func (s *Store) GetNodesByInstance(ctx context.Context, instanceID string) ([]*InstanceNode, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, instance_id, node_index, role, openstack_server_id, openstack_volume_id,
		        private_ip, mgmt_ip, status, agent_connected, last_heartbeat, created_at, updated_at
		 FROM instance_nodes WHERE instance_id = $1 AND status != 'deleted' ORDER BY node_index`, instanceID)
	if err != nil {
		return nil, fmt.Errorf("get nodes: %w", err)
	}
	defer rows.Close()

	var nodes []*InstanceNode
	for rows.Next() {
		n := &InstanceNode{}
		if err := rows.Scan(&n.ID, &n.InstanceID, &n.NodeIndex, &n.Role, &n.OpenstackServerID, &n.OpenstackVolumeID,
			&n.PrivateIP, &n.MgmtIP, &n.Status, &n.AgentConnected, &n.LastHeartbeat,
			&n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// GetNode retrieves a single node by ID.
func (s *Store) GetNode(ctx context.Context, nodeID string) (*InstanceNode, error) {
	n := &InstanceNode{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, instance_id, node_index, role, openstack_server_id, openstack_volume_id,
		        private_ip, mgmt_ip, status, agent_connected, last_heartbeat, created_at, updated_at
		 FROM instance_nodes WHERE id = $1`, nodeID,
	).Scan(&n.ID, &n.InstanceID, &n.NodeIndex, &n.Role, &n.OpenstackServerID, &n.OpenstackVolumeID,
		&n.PrivateIP, &n.MgmtIP, &n.Status, &n.AgentConnected, &n.LastHeartbeat,
		&n.CreatedAt, &n.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get node %s: %w", nodeID, err)
	}
	return n, nil
}

// UpdateNodeStatus sets the status of a node.
func (s *Store) UpdateNodeStatus(ctx context.Context, nodeID, status string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE instance_nodes SET status = $1, updated_at = NOW() WHERE id = $2`, status, nodeID)
	if err != nil {
		return fmt.Errorf("update node status: %w", err)
	}
	return nil
}

// UpdateNodeRole sets the role (primary/replica) of a node.
func (s *Store) UpdateNodeRole(ctx context.Context, nodeID, role string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE instance_nodes SET role = $1, updated_at = NOW() WHERE id = $2`, role, nodeID)
	if err != nil {
		return fmt.Errorf("update node role: %w", err)
	}
	return nil
}

// UpdateNodeOpenStackIDs sets the Nova server and Cinder volume IDs for a node.
func (s *Store) UpdateNodeOpenStackIDs(ctx context.Context, nodeID, serverID, volumeID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE instance_nodes SET openstack_server_id = $1, openstack_volume_id = $2, updated_at = NOW()
		 WHERE id = $3`, serverID, volumeID, nodeID)
	if err != nil {
		return fmt.Errorf("update node openstack ids: %w", err)
	}
	return nil
}

// UpdateNodeIPs sets the private and management IPs of a node.
func (s *Store) UpdateNodeIPs(ctx context.Context, nodeID, privateIP, mgmtIP string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE instance_nodes SET private_ip = $1, mgmt_ip = $2, updated_at = NOW()
		 WHERE id = $3`, privateIP, mgmtIP, nodeID)
	if err != nil {
		return fmt.Errorf("update node ips: %w", err)
	}
	return nil
}

// UpdateNodeAgentConnected sets the agent_connected flag and last_heartbeat.
func (s *Store) UpdateNodeAgentConnected(ctx context.Context, nodeID string, connected bool) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE instance_nodes SET agent_connected = $1, last_heartbeat = NOW(), status = CASE WHEN $1 THEN 'ready' ELSE status END, updated_at = NOW()
		 WHERE id = $2`, connected, nodeID)
	if err != nil {
		return fmt.Errorf("update node agent connected: %w", err)
	}
	return nil
}

// UpdateNodeHeartbeat updates the last_heartbeat timestamp and role for a node.
func (s *Store) UpdateNodeHeartbeat(ctx context.Context, nodeID, role string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE instance_nodes SET last_heartbeat = NOW(), role = $1, updated_at = NOW()
		 WHERE id = $2`, role, nodeID)
	if err != nil {
		return fmt.Errorf("update node heartbeat: %w", err)
	}
	return nil
}

// CountReadyNodes returns the number of nodes with agent_connected=true for an instance.
func (s *Store) CountReadyNodes(ctx context.Context, instanceID string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM instance_nodes WHERE instance_id = $1 AND agent_connected = TRUE`, instanceID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count ready nodes: %w", err)
	}
	return count, nil
}

// GetInstanceByNodeID retrieves the parent instance for a given node ID.
func (s *Store) GetInstanceByNodeID(ctx context.Context, nodeID string) (*Instance, error) {
	inst := &Instance{}
	err := s.pool.QueryRow(ctx,
		`SELECT i.id, i.tenant_id, i.environment_id, i.name, i.engine, i.engine_version, i.tier,
		        i.flavor_id, i.disk_size_gb, i.status, i.host, i.port, i.admin_user, i.admin_password,
		        i.dbaas_project_id, i.backup_bucket, i.metadata, i.created_at, i.updated_at
		 FROM instances i
		 JOIN instance_nodes n ON n.instance_id = i.id
		 WHERE n.id = $1 AND i.status != 'deleted'`, nodeID,
	).Scan(&inst.ID, &inst.TenantID, &inst.EnvironmentID, &inst.Name, &inst.Engine, &inst.EngineVersion,
		&inst.Tier, &inst.FlavorID, &inst.DiskSizeGB, &inst.Status, &inst.Host, &inst.Port,
		&inst.AdminUser, &inst.AdminPassword, &inst.DbaasProjectID, &inst.BackupBucket, &inst.Metadata,
		&inst.CreatedAt, &inst.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("instance for node %s not found", nodeID)
		}
		return nil, fmt.Errorf("get instance by node: %w", err)
	}
	return inst, nil
}
