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
}
