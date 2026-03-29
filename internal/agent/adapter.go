package agent

import (
	"fmt"
	"time"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// AgentConfig describes how to connect to the agent under test.
type AgentConfig struct {
	Adapter string        // "http", "mcp", "cli"
	URL     string        // endpoint URL (for http and mcp)
	Token   string        // bearer token (optional, for http)
	Command string        // binary path (for cli adapter)
	Args    []string      // additional args (for cli adapter)
	Timeout time.Duration // per-request timeout
}

// NewClient creates an AgentClient for the given adapter type.
func NewClient(cfg AgentConfig) (evaluation.AgentClient, error) {
	switch cfg.Adapter {
	case "http", "":
		if cfg.URL == "" {
			return nil, fmt.Errorf("http adapter requires a URL")
		}
		return NewHTTPClient(cfg.URL, cfg.Token), nil
	case "mcp":
		if cfg.URL == "" {
			return nil, fmt.Errorf("mcp adapter requires a URL")
		}
		return NewMCPClient(cfg.URL), nil
	case "cli":
		if cfg.Command == "" {
			return nil, fmt.Errorf("cli adapter requires a command")
		}
		return NewCLIClient(cfg.Command, cfg.Args), nil
	default:
		return nil, fmt.Errorf("unknown agent adapter %q (supported: http, mcp, cli)", cfg.Adapter)
	}
}
