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
	DbaasProjectID     *string           `json:"dbaas_project_id,omitempty"`
	OpenstackSecgroupID *string        `json:"openstack_secgroup_id,omitempty"`
	BackupBucket       *string         `json:"backup_bucket,omitempty"`
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
		           status, host, port, admin_user, admin_password, dbaas_project_id, openstack_secgroup_id, backup_bucket, metadata, created_at, updated_at`,
		tenantID, environmentID, name, engine, engineVersion, tier, flavorID, diskSizeGB, adminPassword, metadata,
	).Scan(&inst.ID, &inst.TenantID, &inst.EnvironmentID, &inst.Name, &inst.Engine, &inst.EngineVersion,
		&inst.Tier, &inst.FlavorID, &inst.DiskSizeGB, &inst.Status, &inst.Host, &inst.Port,
		&inst.AdminUser, &inst.AdminPassword, &inst.DbaasProjectID, &inst.OpenstackSecgroupID, &inst.BackupBucket, &inst.Metadata,
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
		        status, host, port, admin_user, admin_password, dbaas_project_id, openstack_secgroup_id, backup_bucket, metadata, created_at, updated_at
		 FROM instances WHERE id = $1 AND status != 'deleted'`, id,
	).Scan(&inst.ID, &inst.TenantID, &inst.EnvironmentID, &inst.Name, &inst.Engine, &inst.EngineVersion,
		&inst.Tier, &inst.FlavorID, &inst.DiskSizeGB, &inst.Status, &inst.Host, &inst.Port,
		&inst.AdminUser, &inst.AdminPassword, &inst.DbaasProjectID, &inst.OpenstackSecgroupID, &inst.BackupBucket, &inst.Metadata,
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
		        status, host, port, admin_user, dbaas_project_id, openstack_secgroup_id, backup_bucket, metadata, created_at, updated_at
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
			&inst.AdminUser, &inst.DbaasProjectID, &inst.OpenstackSecgroupID, &inst.BackupBucket, &inst.Metadata,
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
		&inst.AdminUser, &inst.AdminPassword, &inst.DbaasProjectID, &inst.OpenstackSecgroupID, &inst.BackupBucket, &inst.Metadata,
		&inst.CreatedAt, &inst.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("instance for node %s not found", nodeID)
		}
		return nil, fmt.Errorf("get instance by node: %w", err)
	}
	return inst, nil
}

// ---------- Database model + queries ----------

// Database represents a logical database within a managed instance.
type Database struct {
	ID         string    `json:"id"`
	InstanceID string    `json:"instance_id"`
	TenantID   string    `json:"tenant_id"`
	Name       string    `json:"name"`
	Charset    string    `json:"charset"`
	Collation  string    `json:"collation"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// CreateDatabase inserts a new database record with status 'creating'.
func (s *Store) CreateDatabase(ctx context.Context, instanceID, tenantID, name, charset, collation string) (*Database, error) {
	if charset == "" {
		charset = "UTF8"
	}
	if collation == "" {
		collation = "en_US.UTF-8"
	}
	d := &Database{}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO databases (instance_id, tenant_id, name, charset, collation, status)
		 VALUES ($1, $2, $3, $4, $5, 'creating')
		 RETURNING id, instance_id, tenant_id, name, charset, collation, status, created_at, updated_at`,
		instanceID, tenantID, name, charset, collation,
	).Scan(&d.ID, &d.InstanceID, &d.TenantID, &d.Name, &d.Charset, &d.Collation,
		&d.Status, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert database: %w", err)
	}
	return d, nil
}

// GetDatabase retrieves a database by ID (excludes deleted).
func (s *Store) GetDatabase(ctx context.Context, id string) (*Database, error) {
	d := &Database{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, instance_id, tenant_id, name, charset, collation, status, created_at, updated_at
		 FROM databases WHERE id = $1 AND status != 'deleted'`, id,
	).Scan(&d.ID, &d.InstanceID, &d.TenantID, &d.Name, &d.Charset, &d.Collation,
		&d.Status, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get database %s: %w", id, err)
	}
	return d, nil
}

// ListDatabases returns all databases for an instance (excludes deleted).
func (s *Store) ListDatabases(ctx context.Context, instanceID string) ([]*Database, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, instance_id, tenant_id, name, charset, collation, status, created_at, updated_at
		 FROM databases WHERE instance_id = $1 AND status != 'deleted' ORDER BY created_at`, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}
	defer rows.Close()

	var dbs []*Database
	for rows.Next() {
		d := &Database{}
		if err := rows.Scan(&d.ID, &d.InstanceID, &d.TenantID, &d.Name, &d.Charset, &d.Collation,
			&d.Status, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan database: %w", err)
		}
		dbs = append(dbs, d)
	}
	return dbs, rows.Err()
}

// UpdateDatabaseStatus sets the status of a database.
func (s *Store) UpdateDatabaseStatus(ctx context.Context, id, status string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE databases SET status = $1, updated_at = NOW() WHERE id = $2`, status, id)
	if err != nil {
		return fmt.Errorf("update database status: %w", err)
	}
	return nil
}

// SoftDeleteDatabase marks a database as deleted.
func (s *Store) SoftDeleteDatabase(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE databases SET status = 'deleted', deleted_at = NOW(), updated_at = NOW() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("soft delete database: %w", err)
	}
	return nil
}

// ---------- User model + queries ----------

// User represents a database user within a managed instance.
type User struct {
	ID         string    `json:"id"`
	InstanceID string    `json:"instance_id"`
	TenantID   string    `json:"tenant_id"`
	Username   string    `json:"username"`
	Role       string    `json:"role"`
	Databases  []string  `json:"databases"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// CreateUser inserts a new user record with status 'creating'.
func (s *Store) CreateUser(ctx context.Context, instanceID, tenantID, username, role string, databases []string) (*User, error) {
	if databases == nil {
		databases = []string{}
	}
	u := &User{}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO users (instance_id, tenant_id, username, role, databases, status)
		 VALUES ($1, $2, $3, $4, $5, 'creating')
		 RETURNING id, instance_id, tenant_id, username, role, databases, status, created_at, updated_at`,
		instanceID, tenantID, username, role, databases,
	).Scan(&u.ID, &u.InstanceID, &u.TenantID, &u.Username, &u.Role, &u.Databases,
		&u.Status, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}
	return u, nil
}

// GetUser retrieves a user by ID (excludes deleted).
func (s *Store) GetUser(ctx context.Context, id string) (*User, error) {
	u := &User{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, instance_id, tenant_id, username, role, databases, status, created_at, updated_at
		 FROM users WHERE id = $1 AND status != 'deleted'`, id,
	).Scan(&u.ID, &u.InstanceID, &u.TenantID, &u.Username, &u.Role, &u.Databases,
		&u.Status, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get user %s: %w", id, err)
	}
	return u, nil
}

// ListUsers returns all users for an instance (excludes deleted).
func (s *Store) ListUsers(ctx context.Context, instanceID string) ([]*User, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, instance_id, tenant_id, username, role, databases, status, created_at, updated_at
		 FROM users WHERE instance_id = $1 AND status != 'deleted' ORDER BY created_at`, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		u := &User{}
		if err := rows.Scan(&u.ID, &u.InstanceID, &u.TenantID, &u.Username, &u.Role, &u.Databases,
			&u.Status, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// UpdateUserStatus sets the status of a user.
func (s *Store) UpdateUserStatus(ctx context.Context, id, status string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET status = $1, updated_at = NOW() WHERE id = $2`, status, id)
	if err != nil {
		return fmt.Errorf("update user status: %w", err)
	}
	return nil
}

// SoftDeleteUser marks a user as deleted.
func (s *Store) SoftDeleteUser(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET status = 'deleted', deleted_at = NOW(), updated_at = NOW() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("soft delete user: %w", err)
	}
	return nil
}

// ---------- Firewall Rule model + queries ----------

// FirewallRule represents a security group rule for a database instance.
type FirewallRule struct {
	ID              string    `json:"id"`
	InstanceID      string    `json:"instance_id"`
	TenantID        string    `json:"tenant_id"`
	Direction       string    `json:"direction"`
	Protocol        string    `json:"protocol"`
	PortRangeMin    *int      `json:"port_range_min,omitempty"`
	PortRangeMax    *int      `json:"port_range_max,omitempty"`
	RemoteIPPrefix  string    `json:"remote_ip_prefix"`
	Description     string    `json:"description"`
	OpenstackRuleID *string   `json:"openstack_rule_id,omitempty"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// CreateFirewallRule inserts a new firewall rule record.
func (s *Store) CreateFirewallRule(ctx context.Context, instanceID, tenantID, direction, protocol string, portMin, portMax *int, remoteIPPrefix, description string) (*FirewallRule, error) {
	r := &FirewallRule{}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO firewall_rules (instance_id, tenant_id, direction, protocol, port_range_min, port_range_max, remote_ip_prefix, description, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'creating')
		 RETURNING id, instance_id, tenant_id, direction, protocol, port_range_min, port_range_max, remote_ip_prefix, description, openstack_rule_id, status, created_at, updated_at`,
		instanceID, tenantID, direction, protocol, portMin, portMax, remoteIPPrefix, description,
	).Scan(&r.ID, &r.InstanceID, &r.TenantID, &r.Direction, &r.Protocol, &r.PortRangeMin, &r.PortRangeMax,
		&r.RemoteIPPrefix, &r.Description, &r.OpenstackRuleID, &r.Status, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert firewall rule: %w", err)
	}
	return r, nil
}

// UpdateFirewallRuleOpenstackID sets the OpenStack security group rule ID.
func (s *Store) UpdateFirewallRuleOpenstackID(ctx context.Context, id, ruleID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE firewall_rules SET openstack_rule_id = $1, status = 'active', updated_at = NOW() WHERE id = $2`, ruleID, id)
	if err != nil {
		return fmt.Errorf("update firewall rule openstack id: %w", err)
	}
	return nil
}

// ListFirewallRules returns all firewall rules for an instance (excludes deleted).
func (s *Store) ListFirewallRules(ctx context.Context, instanceID string) ([]*FirewallRule, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, instance_id, tenant_id, direction, protocol, port_range_min, port_range_max,
		        remote_ip_prefix, description, openstack_rule_id, status, created_at, updated_at
		 FROM firewall_rules WHERE instance_id = $1 AND status != 'deleted' ORDER BY created_at`, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list firewall rules: %w", err)
	}
	defer rows.Close()

	var rules []*FirewallRule
	for rows.Next() {
		r := &FirewallRule{}
		if err := rows.Scan(&r.ID, &r.InstanceID, &r.TenantID, &r.Direction, &r.Protocol,
			&r.PortRangeMin, &r.PortRangeMax, &r.RemoteIPPrefix, &r.Description,
			&r.OpenstackRuleID, &r.Status, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan firewall rule: %w", err)
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// GetFirewallRule retrieves a firewall rule by ID.
func (s *Store) GetFirewallRule(ctx context.Context, id string) (*FirewallRule, error) {
	r := &FirewallRule{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, instance_id, tenant_id, direction, protocol, port_range_min, port_range_max,
		        remote_ip_prefix, description, openstack_rule_id, status, created_at, updated_at
		 FROM firewall_rules WHERE id = $1 AND status != 'deleted'`, id,
	).Scan(&r.ID, &r.InstanceID, &r.TenantID, &r.Direction, &r.Protocol,
		&r.PortRangeMin, &r.PortRangeMax, &r.RemoteIPPrefix, &r.Description,
		&r.OpenstackRuleID, &r.Status, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get firewall rule %s: %w", id, err)
	}
	return r, nil
}

// SoftDeleteFirewallRule marks a firewall rule as deleted.
func (s *Store) SoftDeleteFirewallRule(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE firewall_rules SET status = 'deleted', updated_at = NOW() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("soft delete firewall rule: %w", err)
	}
	return nil
}

// UpdateInstanceSecgroupID sets the OpenStack security group ID for an instance.
func (s *Store) UpdateInstanceSecgroupID(ctx context.Context, id, secgroupID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE instances SET openstack_secgroup_id = $1, updated_at = NOW() WHERE id = $2`, secgroupID, id)
	if err != nil {
		return fmt.Errorf("update instance secgroup id: %w", err)
	}
	return nil
}
