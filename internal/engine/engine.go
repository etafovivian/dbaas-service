// Package engine defines the interface that each database engine must implement.
// Adding a new engine (e.g. MariaDB) means creating a new package under engine/
// that satisfies this interface — no changes to the core dbaas-service code.
package engine

// Engine abstracts all database-engine-specific behavior.
// The dbaas-service calls these methods during provisioning and management.
type Engine interface {
	// Name returns the engine identifier (e.g. "postgres", "mariadb").
	Name() string

	// DefaultVersion returns the default version for this engine (e.g. "18").
	DefaultVersion() string

	// DefaultPort returns the default client-facing port (e.g. 5432, 3306).
	DefaultPort() int

	// DefaultAdminUser returns the default admin username (e.g. "etafovivian_admin").
	DefaultAdminUser() string

	// ComposeYAML renders the Docker Compose file for the given instance config.
	ComposeYAML(cfg InstanceConfig) (string, error)

	// CloudInitEnv returns engine-specific environment variables for /etc/dbaas/instance.env.
	CloudInitEnv(cfg InstanceConfig) map[string]string

	// DataDir returns the mount path for the data volume inside the VM.
	DataDir() string

	// DataDirOwner returns the UID:GID that owns the data directory (e.g. "999:999" for postgres).
	DataDirOwner() string
}

// InstanceConfig holds engine-agnostic instance parameters passed to engine methods.
type InstanceConfig struct {
	InstanceID       string
	NodeID           string
	NodeIndex        int
	EngineVersion    string
	AdminUser        string // Tenant-facing admin user (full PG privileges)
	AdminPassword    string // Tenant-facing admin password
	InternalUser     string // dbaas-agent internal superuser (hidden from tenant)
	InternalPassword string // dbaas-agent internal password
	Tier             string
	AgentVersion     string
}
