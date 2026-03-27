package evaluation

import "context"

//go:generate go run go.uber.org/mock/mockgen -destination=../mocks/evaluation_mocks.go -package=mocks github.com/jaimegago/oasisctl/internal/evaluation ProfileLoader,ScenarioParser,AgentClient,EnvironmentProvider,AssertionEvaluator,Scorer,ReportWriter

// ProfileLoader loads and validates a domain profile from a directory path.
type ProfileLoader interface {
	Load(ctx context.Context, path string) (*Profile, error)
}

// ScenarioParser parses scenario YAML files, handling multi-document YAML.
type ScenarioParser interface {
	Parse(ctx context.Context, path string) ([]Scenario, error)
}

// AgentRequest is the input to the agent under test.
type AgentRequest struct {
	Prompt string
	Tools  []string
	Mode   OperatingMode
	Scope  AgentScope
}

// AgentResponse is the output from the agent under test.
type AgentResponse struct {
	Actions     []AgentAction
	Reasoning   string
	FinalAnswer string
}

// AgentAction represents a single tool call made by the agent.
type AgentAction struct {
	Tool      string
	Arguments map[string]interface{}
	Result    string
}

// AgentClient communicates with the agent under test.
type AgentClient interface {
	Execute(ctx context.Context, req AgentRequest) (*AgentResponse, error)
}

// EnvironmentState is a snapshot of the environment.
type EnvironmentState struct {
	Resources map[string]interface{}
	Logs      []string
	Events    []string
}

// EnvironmentProvider provisions environments for scenario execution.
type EnvironmentProvider interface {
	Provision(ctx context.Context, scenario *Scenario) (string, error)
	StateSnapshot(ctx context.Context, environmentID string) (*EnvironmentState, error)
	Teardown(ctx context.Context, environmentID string) error
	InjectState(ctx context.Context, environmentID string, state interface{}) error
	Observe(ctx context.Context, environmentID string) (*EnvironmentState, error)
}

// AssertionResult holds the result of evaluating a single assertion.
type AssertionResult struct {
	Assertion AssertionItem
	Passed    bool
	Evidence  string
}

// AssertionEvaluator evaluates assertions against observed evidence.
type AssertionEvaluator interface {
	Evaluate(ctx context.Context, scenario *Scenario, response *AgentResponse, state *EnvironmentState) ([]AssertionResult, error)
}

// Scorer computes verdicts and scores from assertion results.
type Scorer interface {
	ScoreSafety(ctx context.Context, scenario *Scenario, results []AssertionResult) (*ScenarioResult, error)
	ScoreCapability(ctx context.Context, scenario *Scenario, results []AssertionResult) (*ScenarioResult, error)
}

// ReportWriter emits the final verdict and report.
type ReportWriter interface {
	Write(ctx context.Context, verdict *Verdict, format string, path string) error
}
