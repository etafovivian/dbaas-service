// Package config loads runtime configuration for the dbaas-agent from environment variables.
// These are injected via /etc/dbaas/instance.env by cloud-init.
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all runtime configuration for the dbaas-agent.
type Config struct {
	InstanceID       string
	NodeID           string
	NodeIndex        int
	NATSUrl          string
	PGVersion        string
	PGInternalUser   string // dbaas-agent superuser (hidden from tenant)
	PGInternalPass   string
	PGAdminUser      string // Tenant-facing admin user
	PGAdminPass      string
	Tier             string
	AgentVersion     string
}

// Load reads agent configuration from environment variables.
func Load() (*Config, error) {
	nodeIndex, _ := strconv.Atoi(os.Getenv("NODE_INDEX"))

	cfg := &Config{
		InstanceID:     os.Getenv("INSTANCE_ID"),
		NodeID:         os.Getenv("NODE_ID"),
		NodeIndex:      nodeIndex,
		NATSUrl:        os.Getenv("NATS_URL"),
		PGVersion:      os.Getenv("PG_VERSION"),
		PGInternalUser: os.Getenv("PG_INTERNAL_USER"),
		PGInternalPass: os.Getenv("PG_INTERNAL_PASSWORD"),
		PGAdminUser:    os.Getenv("PG_ADMIN_USER"),
		PGAdminPass:    os.Getenv("PG_ADMIN_PASSWORD"),
		Tier:           os.Getenv("TIER"),
		AgentVersion:   os.Getenv("AGENT_VERSION"),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	var missing []string
	if c.InstanceID == "" {
		missing = append(missing, "INSTANCE_ID")
	}
	if c.NodeID == "" {
		missing = append(missing, "NODE_ID")
	}
	if c.NATSUrl == "" {
		missing = append(missing, "NATS_URL")
	}
	if c.PGInternalUser == "" {
		missing = append(missing, "PG_INTERNAL_USER")
	}
	if c.PGInternalPass == "" {
		missing = append(missing, "PG_INTERNAL_PASSWORD")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %v", missing)
	}
	return nil
}
