// Package postgres implements the agent engine interface for PostgreSQL.
// All PostgreSQL-specific agent behavior — connection via pgx, role detection
// via pg_is_in_recovery(), health checks — is contained here.
package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

// Engine implements the agent PostgreSQL engine.
type Engine struct {
	conn     *pgx.Conn
	user     string
	password string
}

// New creates a PostgreSQL agent engine.
func New(user, password string) *Engine {
	return &Engine{
		user:     user,
		password: password,
	}
}

// WaitReady connects to the local PostgreSQL instance with retry/backoff.
func (e *Engine) WaitReady(ctx context.Context) error {
	dsn := fmt.Sprintf("postgres://%s:%s@localhost:5432/postgres?sslmode=disable", e.user, e.password)

	for i := range 60 { // 60 * 5s = 5 minutes max
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if i > 0 {
			time.Sleep(5 * time.Second)
		}

		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			slog.Warn("waiting for PostgreSQL", "attempt", i+1, "err", err)
			continue
		}

		if err = conn.Ping(ctx); err != nil {
			slog.Warn("PostgreSQL not ready", "attempt", i+1, "err", err)
			conn.Close(ctx)
			continue
		}

		slog.Info("PostgreSQL is ready", "attempt", i+1)
		e.conn = conn
		return nil
	}
	return fmt.Errorf("PostgreSQL did not become ready within timeout")
}

// DetectRole queries pg_is_in_recovery() to determine if this node is primary or replica.
func (e *Engine) DetectRole(ctx context.Context) string {
	if e.conn == nil {
		return "unknown"
	}

	var isInRecovery bool
	err := e.conn.QueryRow(ctx, "SELECT pg_is_in_recovery()").Scan(&isInRecovery)
	if err != nil {
		slog.Warn("failed to detect PostgreSQL role", "err", err)
		return "unknown"
	}
	if isInRecovery {
		return "replica"
	}
	return "primary"
}

// Name returns the engine identifier.
func (e *Engine) Name() string { return "postgres" }

// Close releases the PostgreSQL connection.
func (e *Engine) Close(ctx context.Context) {
	if e.conn != nil {
		e.conn.Close(ctx)
	}
}

// dsn returns a connection string for the given database.
func (e *Engine) dsn(database string) string {
	return fmt.Sprintf("postgres://%s:%s@localhost:5432/%s?sslmode=disable", e.user, e.password, database)
}

// CreateDatabase creates a logical database. Runs outside a transaction (required by PG).
func (e *Engine) CreateDatabase(ctx context.Context, name, charset, collation string) error {
	// CREATE DATABASE cannot run inside a transaction — use a fresh connection.
	conn, err := pgx.Connect(ctx, e.dsn("postgres"))
	if err != nil {
		return fmt.Errorf("connect for create database: %w", err)
	}
	defer conn.Close(ctx)

	// Use quoted identifier to prevent SQL injection.
	sql := fmt.Sprintf(`CREATE DATABASE %q ENCODING '%s' LC_COLLATE '%s' LC_CTYPE '%s'`,
		name, charset, collation, collation)
	if _, err := conn.Exec(ctx, sql); err != nil {
		return fmt.Errorf("create database %q: %w", name, err)
	}

	// Revoke public CONNECT — only explicitly granted users can connect.
	revoke := fmt.Sprintf(`REVOKE CONNECT ON DATABASE %q FROM PUBLIC`, name)
	if _, err := conn.Exec(ctx, revoke); err != nil {
		slog.Warn("failed to revoke public connect", "database", name, "err", err)
	}

	return nil
}

// DropDatabase drops a logical database after terminating active connections.
func (e *Engine) DropDatabase(ctx context.Context, name string) error {
	conn, err := pgx.Connect(ctx, e.dsn("postgres"))
	if err != nil {
		return fmt.Errorf("connect for drop database: %w", err)
	}
	defer conn.Close(ctx)

	// Terminate active connections.
	if _, err := conn.Exec(ctx,
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid != pg_backend_pid()`, name); err != nil {
		slog.Warn("failed to terminate connections", "database", name, "err", err)
	}

	sql := fmt.Sprintf(`DROP DATABASE IF EXISTS %q`, name)
	if _, err := conn.Exec(ctx, sql); err != nil {
		return fmt.Errorf("drop database %q: %w", name, err)
	}
	return nil
}

// CreateUser creates a PostgreSQL role with role-based grants on specified databases.
func (e *Engine) CreateUser(ctx context.Context, username, password, role string, databases []string) error {
	// Create the role on the postgres database (main connection).
	createSQL := fmt.Sprintf(
		`CREATE ROLE %q LOGIN PASSWORD '%s' NOSUPERUSER NOCREATEDB NOCREATEROLE`,
		username, password)
	if _, err := e.conn.Exec(ctx, createSQL); err != nil {
		return fmt.Errorf("create role %q: %w", username, err)
	}

	// Block access to system database.
	revokeSys := fmt.Sprintf(`REVOKE CONNECT ON DATABASE postgres FROM %q`, username)
	if _, err := e.conn.Exec(ctx, revokeSys); err != nil {
		slog.Warn("failed to revoke system db access", "username", username, "err", err)
	}

	// Grant access per database.
	for _, dbName := range databases {
		if err := e.grantDatabaseAccess(ctx, username, dbName, role); err != nil {
			return fmt.Errorf("grant on %q: %w", dbName, err)
		}
	}
	return nil
}

// grantDatabaseAccess grants role-based privileges on a specific database.
func (e *Engine) grantDatabaseAccess(ctx context.Context, username, dbName, role string) error {
	// Grant CONNECT on the database (from postgres connection).
	grantConnect := fmt.Sprintf(`GRANT CONNECT ON DATABASE %q TO %q`, dbName, username)
	if _, err := e.conn.Exec(ctx, grantConnect); err != nil {
		return fmt.Errorf("grant connect: %w", err)
	}

	// Connect to the target database to set schema-level grants.
	dbConn, err := pgx.Connect(ctx, e.dsn(dbName))
	if err != nil {
		return fmt.Errorf("connect to %q: %w", dbName, err)
	}
	defer dbConn.Close(ctx)

	var grants []string
	switch role {
	case "owner":
		grants = []string{
			fmt.Sprintf(`GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO %q`, username),
			fmt.Sprintf(`GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO %q`, username),
			fmt.Sprintf(`ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL PRIVILEGES ON TABLES TO %q`, username),
			fmt.Sprintf(`ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL PRIVILEGES ON SEQUENCES TO %q`, username),
			fmt.Sprintf(`GRANT CREATE ON SCHEMA public TO %q`, username),
		}
	case "readwrite":
		grants = []string{
			fmt.Sprintf(`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO %q`, username),
			fmt.Sprintf(`GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO %q`, username),
			fmt.Sprintf(`ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %q`, username),
			fmt.Sprintf(`ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE ON SEQUENCES TO %q`, username),
		}
	case "readonly":
		grants = []string{
			fmt.Sprintf(`GRANT SELECT ON ALL TABLES IN SCHEMA public TO %q`, username),
			fmt.Sprintf(`ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO %q`, username),
		}
	}

	for _, g := range grants {
		if _, err := dbConn.Exec(ctx, g); err != nil {
			return fmt.Errorf("exec grant: %w", err)
		}
	}
	return nil
}

// DropUser revokes all privileges and drops the PostgreSQL role.
func (e *Engine) DropUser(ctx context.Context, username string, databases []string) error {
	// Revoke per-database privileges.
	for _, dbName := range databases {
		dbConn, err := pgx.Connect(ctx, e.dsn(dbName))
		if err != nil {
			slog.Warn("connect to revoke failed", "database", dbName, "err", err)
			continue
		}
		revokes := []string{
			fmt.Sprintf(`REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM %q`, username),
			fmt.Sprintf(`REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM %q`, username),
			fmt.Sprintf(`ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE ALL ON TABLES FROM %q`, username),
			fmt.Sprintf(`ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE ALL ON SEQUENCES FROM %q`, username),
		}
		for _, r := range revokes {
			if _, err := dbConn.Exec(ctx, r); err != nil {
				slog.Warn("revoke failed", "sql", r, "err", err)
			}
		}
		dbConn.Close(ctx)

		revokeConnect := fmt.Sprintf(`REVOKE CONNECT ON DATABASE %q FROM %q`, dbName, username)
		if _, err := e.conn.Exec(ctx, revokeConnect); err != nil {
			slog.Warn("revoke connect failed", "database", dbName, "err", err)
		}
	}

	// Drop the role.
	dropSQL := fmt.Sprintf(`DROP ROLE IF EXISTS %q`, username)
	if _, err := e.conn.Exec(ctx, dropSQL); err != nil {
		return fmt.Errorf("drop role %q: %w", username, err)
	}
	return nil
}

// ResetPassword changes the password for a database user.
func (e *Engine) ResetPassword(ctx context.Context, username, newPassword string) error {
	sql := fmt.Sprintf(`ALTER ROLE %q PASSWORD '%s'`, username, newPassword)
	if _, err := e.conn.Exec(ctx, sql); err != nil {
		return fmt.Errorf("reset password for %q: %w", username, err)
	}
	return nil
}
