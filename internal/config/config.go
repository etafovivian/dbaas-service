// Package config loads and validates runtime configuration for the dbaas-service.
package config

import (
	"fmt"

	sharedcfg "github.com/etafovivian/shared/config"
)

// Config holds all runtime configuration for the dbaas-service.
type Config struct {
	DatabaseURL       string
	NATSUrl           string
	NATSCredsFile     string
	NATSCAFile        string
	MetricsPort       int
	CephS3Endpoint    string
	CephS3AccessKey   string
	CephS3SecretKey   string
	DbaasImageID      string // Glance image UUID for DB VMs
	DbaasMgmtNetID    string // dbaas-mgmt Neutron network UUID
	DbaasAgentVersion string // Docker image tag for dbaas-agent
}

// Load reads configuration from environment variables and returns a validated Config.
func Load() (*Config, error) {
	cfg := &Config{
		DatabaseURL:       sharedcfg.RequireEnv("DBAAS_SERVICE_DATABASE_URL"),
		NATSUrl:           sharedcfg.RequireEnv("NATS_URL"),
		NATSCredsFile:     sharedcfg.RequireEnv("NATS_CREDS_FILE"),
		NATSCAFile:        sharedcfg.OptionalEnv("NATS_CA_FILE", ""),
		MetricsPort:       sharedcfg.IntEnv("METRICS_PORT", 8100),
		CephS3Endpoint:    sharedcfg.OptionalEnv("CEPH_S3_ENDPOINT", ""),
		CephS3AccessKey:   sharedcfg.OptionalEnv("CEPH_S3_ACCESS_KEY", ""),
		CephS3SecretKey:   sharedcfg.OptionalEnv("CEPH_S3_SECRET_KEY", ""),
		DbaasImageID:      sharedcfg.RequireEnv("DBAAS_GLANCE_IMAGE_ID"),
		DbaasMgmtNetID:    sharedcfg.RequireEnv("DBAAS_MGMT_NETWORK_ID"),
		DbaasAgentVersion: sharedcfg.OptionalEnv("DBAAS_AGENT_VERSION", "latest"),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	var missing []string
	if c.DatabaseURL == "" {
		missing = append(missing, "DBAAS_SERVICE_DATABASE_URL")
	}
	if c.NATSUrl == "" {
		missing = append(missing, "NATS_URL")
	}
	if c.NATSCredsFile == "" {
		missing = append(missing, "NATS_CREDS_FILE")
	}
	if c.DbaasImageID == "" {
		missing = append(missing, "DBAAS_GLANCE_IMAGE_ID")
	}
	if c.DbaasMgmtNetID == "" {
		missing = append(missing, "DBAAS_MGMT_NETWORK_ID")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %v", missing)
	}
	return nil
}
