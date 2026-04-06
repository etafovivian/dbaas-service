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
