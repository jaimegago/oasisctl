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
//
// Result is the tool's output as a string. Adapters that receive structured
// tool output encode it as compact JSON within this string, so an empty JSON
// string is `""`, a JSON null is `null`, and no result at all is "".
// ID, Error, ErrorCode and DurationMs are optional — absent when the agent's
// adapter does not report them.
type AgentAction struct {
	ID         string
	Tool       string
	Arguments  map[string]interface{}
	Result     string
	Error      string
	ErrorCode  string
	DurationMs int
}

// AgentClient communicates with the agent under test.
type AgentClient interface {
	Execute(ctx context.Context, req AgentRequest) (*AgentResponse, error)
	ReportIdentityAndConfiguration(ctx context.Context) (AgentIdentity, AgentConfiguration, error)
}

// EnvironmentProvider provisions environments for scenario execution.
type EnvironmentProvider interface {
	Conformance(ctx context.Context, profileID string) (*ConformanceResponse, error)
	Provision(ctx context.Context, req ProvisionRequest) (*ProvisionResponse, error)
	StateSnapshot(ctx context.Context, req StateSnapshotRequest) (*StateSnapshotResponse, error)
	Teardown(ctx context.Context, req TeardownRequest) error
	InjectState(ctx context.Context, req InjectStateRequest) error
	Observe(ctx context.Context, req ObserveRequest) (*ObserveResponse, error)
}

// AssertionResult holds the result of evaluating a single assertion.
type AssertionResult struct {
	Assertion AssertionItem         `json:"assertion" yaml:"assertion"`
	Status    AssertionResultStatus `json:"status" yaml:"status"`
	Evidence  string                `json:"evidence" yaml:"evidence"`
}

// AssertionEvaluator evaluates assertions against observed evidence.
type AssertionEvaluator interface {
	Evaluate(ctx context.Context, scenario *Scenario, response *AgentResponse, observations []ObserveResponse) ([]AssertionResult, error)
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
