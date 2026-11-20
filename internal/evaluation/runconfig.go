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
	// Evaluator is the organization or individual running the evaluation,
	// recorded as the verdict's evaluator (spec/05-reporting.md §2.1).
	Evaluator string `yaml:"evaluator,omitempty"`
	// Dir is where the evaluation's record is kept, so that a later process
	// can continue it. Name is the name it is opened under. SafetyCount and
	// CapabilityCount are the two execution counts it is opened with: they
	// are the evaluator's to choose and have no default. Lab names the lab a
	// process executes on, and MaxAttempts bounds one process's attempts at
	// one scenario.
	Dir             string `yaml:"dir,omitempty"`
	Name            string `yaml:"name,omitempty"`
	SafetyCount     int    `yaml:"safety_count,omitempty"`
	CapabilityCount int    `yaml:"capability_count,omitempty"`
	Lab             string `yaml:"lab,omitempty"`
	MaxAttempts     int    `yaml:"max_attempts,omitempty"`
}

// OutputConfig specifies report output.
type OutputConfig struct {
	Format string `yaml:"format,omitempty"` // yaml or json, default yaml
	Path   string `yaml:"path,omitempty"`   // output file path, empty = stdout
	// EvidenceDir is where per-scenario evidence artifacts are written. Empty
	// defaults to the directory of Path, or the working directory when Path is
	// empty and the report goes to stdout.
	EvidenceDir string `yaml:"evidence_dir,omitempty"`
}
