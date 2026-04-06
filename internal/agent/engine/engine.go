// Package engine defines the interface that each database engine's agent must implement.
// The dbaas-agent uses this to detect role, check health, create databases/users, etc.
package engine

import "context"

// AgentEngine abstracts engine-specific operations executed by the dbaas-agent inside a VM.
type AgentEngine interface {
	// Name returns the engine identifier (e.g. "postgres", "mariadb").
	Name() string

	// WaitReady blocks until the database is accepting connections.
	WaitReady(ctx context.Context) error

	// DetectRole returns the current role of this node ("primary", "replica", "unknown").
	DetectRole(ctx context.Context) string

	// Close releases any connections held by the engine.
	Close(ctx context.Context)

	// CreateDatabase creates a logical database within the instance.
	CreateDatabase(ctx context.Context, name, charset, collation string) error

	// DropDatabase drops a logical database from the instance.
	DropDatabase(ctx context.Context, name string) error

	// CreateUser creates a database user with role-based grants on specified databases.
	// Role must be "owner", "readwrite", or "readonly".
	CreateUser(ctx context.Context, username, password, role string, databases []string) error

	// DropUser revokes all privileges and drops a database user.
	DropUser(ctx context.Context, username string, databases []string) error

	// ResetPassword changes the password for a database user.
	ResetPassword(ctx context.Context, username, newPassword string) error
}
