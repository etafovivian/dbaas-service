// Package postgres implements the engine.Engine interface for PostgreSQL.
// All PostgreSQL-specific behavior — Docker Compose templates, data paths,
// default ports, connection pooling (pgBouncer), HA (Patroni), and backups
// (pgBackRest) — is contained here.
package postgres

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"

	"github.com/etafovivian/dbaas-service/internal/engine"
)

//go:embed templates
var templateFS embed.FS

// Engine implements engine.Engine for PostgreSQL.
type Engine struct{}

// New creates a PostgreSQL engine.
func New() *Engine {
	return &Engine{}
}

func (e *Engine) Name() string           { return "postgres" }
func (e *Engine) DefaultVersion() string  { return "18" }
func (e *Engine) DefaultPort() int        { return 5432 }
func (e *Engine) DefaultAdminUser() string { return "dbadmin" }
func (e *Engine) DataDir() string         { return "/var/lib/postgresql/data" }
func (e *Engine) DataDirOwner() string    { return "999:999" }

// ComposeYAML renders the Docker Compose file for a PostgreSQL instance.
func (e *Engine) ComposeYAML(cfg engine.InstanceConfig) (string, error) {
	tmplName := "templates/docker-compose.single.yml.tmpl"
	if cfg.Tier == "ha" {
		tmplName = "templates/docker-compose.ha.yml.tmpl"
	}

	tmpl, err := template.ParseFS(templateFS, tmplName)
	if err != nil {
		return "", fmt.Errorf("parse postgres compose template %s: %w", tmplName, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return "", fmt.Errorf("execute postgres compose template: %w", err)
	}
	return buf.String(), nil
}

// CloudInitEnv returns PostgreSQL-specific environment variables.
// The agent uses InternalUser/InternalPassword to connect to PG.
// AdminUser/AdminPassword are the tenant-facing credentials.
func (e *Engine) CloudInitEnv(cfg engine.InstanceConfig) map[string]string {
	return map[string]string{
		"PG_VERSION":           cfg.EngineVersion,
		"PG_INTERNAL_USER":     cfg.InternalUser,
		"PG_INTERNAL_PASSWORD": cfg.InternalPassword,
		"PG_ADMIN_USER":        cfg.AdminUser,
		"PG_ADMIN_PASSWORD":    cfg.AdminPassword,
	}
}
