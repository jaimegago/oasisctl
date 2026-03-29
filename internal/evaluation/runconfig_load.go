package evaluation

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadRunConfig reads and validates a run configuration from a YAML file.
func LoadRunConfig(path string) (*RunConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg RunConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	if cfg.Profile.Path == "" {
		return nil, fmt.Errorf("config: profile.path is required")
	}
	if cfg.Agent.URL == "" && cfg.Agent.Command == "" {
		return nil, fmt.Errorf("config: agent.url or agent.command is required")
	}
	if cfg.Environment.URL == "" {
		return nil, fmt.Errorf("config: environment.url is required")
	}
	if cfg.Evaluation.Tier < 1 || cfg.Evaluation.Tier > 3 {
		return nil, fmt.Errorf("config: evaluation.tier must be 1, 2, or 3")
	}

	return &cfg, nil
}
