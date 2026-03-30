package evaluation

// RunConfig defines a complete evaluation run configuration, loadable from YAML.
type RunConfig struct {
	Profile     ProfileConfig     `yaml:"profile"`
	Agent       AgentRunConfig    `yaml:"agent"`
	Environment EnvironmentConfig `yaml:"environment"`
	Evaluation  EvalConfig        `yaml:"evaluation"`
	Output      OutputConfig      `yaml:"output"`
}

// ProfileConfig specifies the profile directory.
type ProfileConfig struct {
	Path string `yaml:"path"`
}

// AgentRunConfig specifies how to reach the agent under test.
type AgentRunConfig struct {
	Adapter string `yaml:"adapter"`           // http, mcp, cli
	URL     string `yaml:"url"`               // endpoint URL (for http and mcp)
	Token   string `yaml:"token,omitempty"`   // bearer token (optional)
	Command string `yaml:"command,omitempty"` // binary path (for cli adapter)
}

// EnvironmentConfig specifies the environment provider.
type EnvironmentConfig struct {
	Provider string `yaml:"provider"` // provider name (informational)
	URL      string `yaml:"url"`      // provider HTTP endpoint
}

// EvalConfig specifies evaluation parameters.
type EvalConfig struct {
	Tier      int      `yaml:"tier"`
	Timeout   string   `yaml:"timeout,omitempty"`   // duration string, default "5m"
	Parallel  int      `yaml:"parallel,omitempty"`  // default 1
	Scenarios []string `yaml:"scenarios,omitempty"` // optional: specific scenario IDs to run
}

// OutputConfig specifies report output.
type OutputConfig struct {
	Format string `yaml:"format,omitempty"` // yaml or json, default yaml
	Path   string `yaml:"path,omitempty"`   // output file path, empty = stdout
}
