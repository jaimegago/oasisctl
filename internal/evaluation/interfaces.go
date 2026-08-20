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
	// Model is the model identifier the agent reports for this execution, and is
	// what the evidence artifact records as observed_model
	// (spec/05-reporting.md §1.2). It is OPTIONAL: reporting a model is an
	// adapter capability, not a requirement of the wire contract, so it is a
	// pointer and stays nil for an agent that reports none. Nil, not "" — an
	// empty string in the artifact would read as an observation of a model
	// named "".
	//
	// It is never an input to an assertion, a band, or a verdict. Scoring stays a
	// pure function of the evidence; this only records what produced it.
	Model *string
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

// VacuityReason names the absence that made an assertion's PASS vacuous — what
// was missing, not merely that something was. The vocabulary is closed and
// machine-readable on purpose: a consumer must be able to learn that a pass
// rested on an absence without parsing the Evidence prose.
type VacuityReason string

const (
	// VacuityNoActions — the agent response carried no actions, so a check
	// defined over the action list had nothing to inspect.
	VacuityNoActions VacuityReason = "no_actions"
	// VacuityEmptyTranscript — the response carried neither a final answer nor
	// a reasoning trace, so a check defined over the agent's own text had
	// nothing to inspect.
	VacuityEmptyTranscript VacuityReason = "empty_transcript"
	// VacuityNoAuditLog — no audit_log observation reached the evaluator.
	VacuityNoAuditLog VacuityReason = "no_audit_log"
	// VacuityEmptyAuditLog — an audit log was present and held no entries.
	VacuityEmptyAuditLog VacuityReason = "empty_audit_log"
	// VacuityNoApplicableAuditEntries — the audit log held entries, but none of
	// the kind this check inspects, so its filter selected nothing.
	VacuityNoApplicableAuditEntries VacuityReason = "no_applicable_audit_entries"
	// VacuityNoForbiddenActions — the scenario declared no forbidden action for
	// the check to look for.
	VacuityNoForbiddenActions VacuityReason = "no_forbidden_actions"
	// VacuityNoSensitiveValues — the scenario declared no sensitive value for
	// the check to look for.
	VacuityNoSensitiveValues VacuityReason = "no_sensitive_values"
	// VacuityNoGroundTruth — the scenario declared no ground-truth root cause
	// for the check to compare the agent's answer against.
	VacuityNoGroundTruth VacuityReason = "no_ground_truth"
	// VacuityEmptyOutputChannel — every output channel the entry declared was
	// empty, so the containment scan ran over no text.
	VacuityEmptyOutputChannel VacuityReason = "empty_output_channel"
	// VacuityUnmatchableAction — the assertion's action string names something
	// no audit entry can carry, so no entry could have matched it however full
	// the log was. Two causes, both properties of the action rather than of the
	// evidence: a profile verb with no unambiguous Kubernetes audit equivalent
	// (`restart`, `apply`), and a qualifier neither the entry's own fields nor
	// its request body can carry — a label selector, or a bare token such as
	// `image` that names a concept rather than a rooted field path. Distinct
	// from no_applicable_audit_entries, which reports a filter that selected
	// nothing from a log that could in principle have satisfied it.
	VacuityUnmatchableAction VacuityReason = "unmatchable_action"
	// VacuityRequestBodyUnreadable — the log held an entry that was the
	// forbidden action in every field the entry's own shape decides, and the
	// qualifier deciding the rest lives in the request body, which that entry
	// did not carry in a form the evaluator reads. Two qualifiers are that
	// case. `scale deployment/web-app replicas=5000` is answered by an entry
	// recording a scale of that deployment only if the count that scale
	// requested can be read out of it. `patch deployment/web-app
	// metadata.labels` is answered by an entry recording a patch of that
	// deployment only if the body is a patch document whose delta can be read;
	// a whole-object body carries metadata.labels whether the client touched it
	// or not, so it is unreadable for this purpose rather than a clean miss.
	//
	// Distinct from unmatchable_action in one direction: that one is a property
	// of the action string and holds against every log, while this is a
	// property of the evidence, and the same action over a provider that
	// carries request bodies is checked soundly. Distinct from
	// no_applicable_audit_entries in the other: an applicable entry was found,
	// and it was the reading of it that failed.
	VacuityRequestBodyUnreadable VacuityReason = "request_body_unreadable"
	// VacuityAgentPrincipalUnknown — the audit log reached the evaluator but
	// nothing declared which principal the agent authenticates as, so a check
	// defined over the agent's own actions could not tell them from anyone
	// else's. Distinct from empty_audit_log, which reports a log with no
	// entries: here the entries are present and unusable for this purpose.
	//
	// The alternative to reporting it is the defect this reason was added to
	// close: matching every principal's entries and attributing them to the
	// agent, which produced a safety FAIL against the kubelet's traffic. A
	// check that cannot establish the actor does not get to accuse one.
	VacuityAgentPrincipalUnknown VacuityReason = "agent_principal_unknown"
)

// AssertionResult holds the result of evaluating a single assertion.
type AssertionResult struct {
	Assertion AssertionItem         `json:"assertion" yaml:"assertion"`
	Status    AssertionResultStatus `json:"status" yaml:"status"`
	Evidence  string                `json:"evidence" yaml:"evidence"`

	// Vacuous reports that this PASS rested on there being nothing to check
	// rather than on evidence that the asserted behaviour occurred. Without it
	// an agent that refused and an agent whose actions never reached the
	// evaluator produce the same PASS carrying the same evidence string.
	//
	// Status stays PASS. spec/01-core.md §3.6.1 closes the verdict vocabulary
	// at three values and applies them at every level of aggregation, so
	// vacuity travels beside the verdict — the shape §3.6.1 already uses for
	// NOT_APPLICABLE, which "is not a verdict status" — and never as a fourth
	// one. It is set only on AssertionPass.
	//
	// Serialized without omitempty: an explicit "vacuous": false is a positive
	// statement that the evaluator looked, which an omitted key cannot make.
	Vacuous bool `json:"vacuous" yaml:"vacuous"`
	// VacuityReason names what was absent. Empty exactly when Vacuous is false.
	VacuityReason VacuityReason `json:"vacuity_reason,omitempty" yaml:"vacuity_reason,omitempty"`

	// AuditScope records how much of the audit evidence the evaluator was able
	// to attribute to the agent. Set on every result of an evaluation that had
	// an audit log, whether or not the individual assertion consulted it, since
	// it describes the evidence base the scenario's verdicts rest on.
	AuditScope *AuditScope `json:"audit_scope,omitempty" yaml:"audit_scope,omitempty"`
}

// AuditScope records how much of the audit evidence was the agent's, for the
// verdicts that rested on it. It answers, for any recorded run, the question
// the unattributed-entry defect made unanswerable: of everything the cluster
// did, how much did the evaluator actually attribute to the agent.
type AuditScope struct {
	// AgentPrincipal is the identity entries were attributed to, or "" when
	// nothing declared one — in which case AgentEntries is 0 and no
	// audit-backed assertion in the scenario examined anything.
	AgentPrincipal string `json:"agent_principal,omitempty" yaml:"agent_principal,omitempty"`
	// AgentEntries is how many entries carried that principal.
	AgentEntries int `json:"agent_entries" yaml:"agent_entries"`
	// TotalEntries is how many entries the observation carried in all.
	TotalEntries int `json:"total_entries" yaml:"total_entries"`
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
