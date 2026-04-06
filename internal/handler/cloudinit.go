package handler

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"text/template"

	"github.com/etafovivian/dbaas-service/internal/engine"
)

//go:embed templates
var templateFS embed.FS

// CloudInitData holds all values needed to render the cloud-init template.
// This is engine-agnostic — engine-specific values come from the Engine interface.
type CloudInitData struct {
	InstanceID    string
	NodeID        string
	NodeIndex     int
	NATSUrl       string
	Tier          string
	AgentVersion  string
	ComposeYAML   string            // Rendered by the engine
	EngineEnvVars map[string]string // Engine-specific env vars
	DataDir       string            // Engine-specific data directory
	DataDirOwner  string            // UID:GID for the data directory
}

// renderCloudInit renders the cloud-init YAML for a database VM.
// It delegates engine-specific rendering to the Engine interface.
func renderCloudInit(eng engine.Engine, cfg engine.InstanceConfig, natsURL string) (string, error) {
	// Render engine-specific Docker Compose YAML
	composeYAML, err := eng.ComposeYAML(cfg)
	if err != nil {
		return "", fmt.Errorf("render compose: %w", err)
	}

	funcMap := template.FuncMap{
		"indent": func(spaces int, s string) string {
			pad := strings.Repeat(" ", spaces)
			lines := strings.Split(s, "\n")
			for i, line := range lines {
				if line != "" {
					lines[i] = pad + line
				}
			}
			return strings.Join(lines, "\n")
		},
	}

	tmpl, err := template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/cloudinit.yaml.tmpl")
	if err != nil {
		return "", fmt.Errorf("parse cloudinit template: %w", err)
	}

	data := CloudInitData{
		InstanceID:    cfg.InstanceID,
		NodeID:        cfg.NodeID,
		NodeIndex:     cfg.NodeIndex,
		NATSUrl:       natsURL,
		Tier:          cfg.Tier,
		AgentVersion:  cfg.AgentVersion,
		ComposeYAML:   composeYAML,
		EngineEnvVars: eng.CloudInitEnv(cfg),
		DataDir:       eng.DataDir(),
		DataDirOwner:  eng.DataDirOwner(),
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "templates/cloudinit.yaml.tmpl", data); err != nil {
		return "", fmt.Errorf("execute cloudinit template: %w", err)
	}
	return buf.String(), nil
}
