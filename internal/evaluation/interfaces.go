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
	// ProviderStatedModels is what the agent's model provider stated served
	// this execution, taken from the provider's own responses and passed on by
	// the adapter: one entry per distinct model the provider named across the
	// execution's calls (spec/05-reporting.md §1.2). It is what the execution
	// record carries as observed_model.
	//
	// It is OPTIONAL and empty for an adapter that cannot supply the
	// provider's statement. It is NEVER the model the agent asked for: an
	// adapter that reports the identifier it requested has reported the
	// declared model a second time, and the identity check would then compare
	// a value with itself. Until core 1.0.0-rc1.14 this was a single `Model`
	// the adapter reported, which for the joe adapter was the configured id
	// (joe-pm threads/observed-model-source.md).
	//
	// It is never an input to an assertion, a band, or a score. The one thing
	// read from it is whether a model other than the evaluation's declared
	// model served the execution, which makes the execution an
	// IDENTITY_MISMATCH attempt (spec/01-core.md §2.5).
	ProviderStatedModels []string

	// Conclusion is the diagnostic conclusion the agent declared on its
	// terminal turn — the cause it committed to and the signals it ruled out.
	// Like Model it is OPTIONAL and a pointer: declaring one is an adapter
	// capability, not a requirement of the wire contract, and nil is an agent
	// that declared nothing rather than an agent that concluded nothing.
	//
	// Unlike Model it IS an input to assertions. That is the whole point of it:
	// a behaviour asking whether the agent named a root cause, or discarded a
	// signal, reads the agent's own declaration instead of matching words
	// against its prose. Scoring stays a pure function of the evidence, and the
	// evidence now includes what the agent said it concluded.
	Conclusion *DiagnosticConclusion

	// EmptyAnswerGate is the outcome of the agent's own empty-answer gate for
	// this execution, when the agent reports one: "held" — it declined to end
	// on an answer with nothing for the operator to read, and the re-entered
	// turn wrote one — or "not_held" — it re-entered and the answer was still
	// empty. Nil is the agent reporting nothing, which covers both a gate that
	// never fired and an agent that has none.
	//
	// OPTIONAL and a pointer for the reason Model is: reporting it is an
	// adapter capability, not a requirement of the wire contract, and "" in the
	// artifact would read as an observed outcome named "".
	//
	// Like Model and unlike Conclusion it is never an input to an assertion, a
	// band, or a verdict. It is recorded so that a run can distinguish a gate
	// that held from a defect that did not occur — a distinction the artifact
	// could not previously make, which is what kept the defect it guards
	// against unobservable across every run.
	EmptyAnswerGate *string

	// Failure is the agent failure report (spec/04-execution.md §1.2): the
	// adapter's statement that the agent could not complete the task for its
	// own infrastructure reasons — a provider error, a dropped connection, an
	// unreachable agent — with the cause. Nil is no report, and a response
	// without one is an answer, however empty it is.
	//
	// It is the ONLY signal of an agent failure. Nothing in oasisctl infers one
	// from an empty final answer or zero actions: an agent whose silence took
	// it out of the score would have found a way out of the measurement.
	Failure *AgentFailure

	// SessionID is the session the prompt ran in, as the adapter named it on
	// the execute response (spec/04-execution.md §1.3). It is an opaque string
	// the runner records and never mints: the join key from the verdict to the
	// transcript to the agent's audit trail, and the value the audit trail
	// operation takes. Empty is an adapter that returned none.
	SessionID string

	// AuditTrail is what the audit trail operation returned for this scenario,
	// or nil when the scenario asserts no behaviour defined over the trail.
	// The RUNNER sets it, after it has captured the response; Execute never
	// does.
	//
	// It is self-report and is read by exactly the behaviours defined over the
	// trail. Nothing else may consult it: what a trail entry says the agent
	// did is never evidence that the agent did it (spec/01-core.md §3.4).
	AuditTrail *AuditTrailEvidence

	// Metadata is the cost and latency context the agent reported for this
	// execution, or nil when it reported none. It is NON-SCORING and cannot
	// become scoring: NonScoringMetadata has no exported field and no accessor,
	// so no assertion, band or verdict can read a number out of it. It is
	// carried to the evidence artifact and nowhere else.
	Metadata *NonScoringMetadata
}

// AgentFailure is an agent failure report: the cause, as the agent or its
// adapter observed it, recorded verbatim.
type AgentFailure struct {
	Cause string `json:"cause" yaml:"cause"`
}

// DiagnosticConclusion is what an agent declared it concluded: the one cause it
// commits to, and the signals it considered and ruled out with the rationale it
// gave for each.
//
// It exists because a heuristic that reads an answer's PROSE measures
// vocabulary rather than reasoning. Two agents reaching the same correct
// judgement in different words score differently under a phrase list, which is
// exactly what a benchmark comparing agents cannot survive — the thing that
// differs between them is the thing being measured.
//
// The declaration is agent-authored, and that is a signal rather than a proof:
// an agent whose declared RootCause its own answer contradicts defeats anything
// keyed on it. What it buys is that the agent's CLAIM is machine-readable,
// which prose is not.
type DiagnosticConclusion struct {
	// RootCause is the one cause the agent committed to. EMPTY is meaningful
	// and is not an error: an agent that would not commit to a single cause
	// leaves it empty, and a behaviour keyed on it is then reported
	// UNASSESSABLE rather than failed. Scoring an absence as a wrong diagnosis
	// would make the resulting figure a measurement of contract adoption
	// rather than of diagnostic accuracy — a heavy thing to assert about every
	// agent a standard evaluates.
	RootCause string

	// Discarded holds the signals the agent ruled out, in declaration order.
	// An EMPTY list under a present Conclusion is an answer — "I ruled nothing
	// out" — and is distinct from a nil Conclusion, which is an absence. The
	// two are separated at this level so no consumer downstream has to
	// reconstruct the distinction from a list length.
	Discarded []DiscardedSignal
}

// DiscardedSignal is one signal an agent declared it ruled out, with its stated
// rationale. Both halves are carried separately because the act being declared
// is discarding a signal WITH stated rationale: a signal named with no reason
// beside it is a weaker thing, and an evaluator requiring the rationale must be
// able to see that it is missing.
type DiscardedSignal struct {
	Signal    string
	Rationale string
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
	// AuditTrail returns the agent's audit trail for the session an execute
	// response named (spec/04-execution.md §1.3). It gives one of three
	// answers, and they are kept apart: the trail, which may be empty;
	// ErrAuditTrailNotImplemented, the adapter's statement that it does not
	// implement the operation; or any other error, which is a failure of the
	// operation at run time and an agent failure. The operation is a read.
	AuditTrail(ctx context.Context, sessionID string) (*AuditTrail, error)
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
	// VacuityNoTrailEntryCitesAlert — a scenario's stimuli made an alert fire
	// and no entry of the agent's audit trail cited it, so the check that an
	// alert an entry cites was firing by then examined nothing
	// (SI behavior-definitions.md §11, link_actions_to_triggers). The score is
	// computed over the triggers the entries do carry and the marker changes
	// none of it.
	VacuityNoTrailEntryCitesAlert VacuityReason = "no_trail_entry_cites_alert"
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
	// one. It is set only on AssertionPass, with one exception: a behaviour
	// scored as a proportion (Score, below) carries the marker beside whatever
	// its proportion came to, because there the absence is of something one
	// part of the check would have examined, and the marker changes no score.
	//
	// Serialized without omitempty: an explicit "vacuous": false is a positive
	// statement that the evaluator looked, which an omitted key cannot make.
	Vacuous bool `json:"vacuous" yaml:"vacuous"`
	// VacuityReason names what was absent. Empty exactly when Vacuous is false.
	VacuityReason VacuityReason `json:"vacuity_reason,omitempty" yaml:"vacuity_reason,omitempty"`

	// Score is the behaviour's own score in [0, 1], set where its definition
	// prescribes one: the behaviours defined over the agent's audit trail are
	// scored as a proportion — pairs covered over pairs observed, entries
	// carrying a statement over entries — or as a binary probe, and an adapter
	// that does not implement the audit trail operation scores zero on each
	// (spec/01-core.md §4.5). Nil is a behaviour judged pass or fail and
	// nothing finer, which is every other behaviour.
	//
	// Status still carries the verdict: PASS exactly when Score is 1. The
	// scorer reads Score for the scenario's score, because a rubric
	// interpolated over pass and fail counts cannot express a proportion.
	Score *float64 `json:"score,omitempty" yaml:"score,omitempty"`

	// AuditScope records how much of the audit evidence the evaluator was able
	// to attribute to the agent. Set on every result of an evaluation that had
	// an audit log, whether or not the individual assertion consulted it, since
	// it describes the evidence base the scenario's verdicts rest on.
	AuditScope *AuditScope `json:"audit_scope,omitempty" yaml:"audit_scope,omitempty"`

	// Unassessable reports that the evaluator could not judge this behaviour
	// at all, because the evidence it is defined over was not present — the
	// agent declared no diagnostic conclusion, so a behaviour keyed on the
	// declaration had nothing to read.
	//
	// It is NOT a fourth verdict. spec/01-core.md §3.6.2 explicitly forbids
	// NEEDS_REVIEW and INCONCLUSIVE as statuses, so this travels beside the
	// verdict in the shape §3.6.3 defines for vacuity, and never as a status
	// of its own. What makes it mean something is that a result carrying it
	// contributes to NEITHER the passed nor the failed count when the scenario
	// is scored: it is excluded, not credited and not blamed.
	//
	// Why it is not simply a FAIL, which is the tempting encoding: an agent
	// that declares nothing has not given a wrong diagnosis, it has given no
	// declaration, and scoring that zero would make the resulting figure a
	// measurement of CONTRACT ADOPTION rather than of diagnostic accuracy —
	// a heavy thing for a standard to assert about every agent it evaluates.
	//
	// Serialized without omitempty, for the reason Vacuous is: an explicit
	// "unassessable": false is a positive statement that the evaluator could
	// judge, which an omitted key cannot make.
	Unassessable bool `json:"unassessable" yaml:"unassessable"`
	// UnassessableReason names what was missing. Empty exactly when
	// Unassessable is false.
	UnassessableReason UnassessableReason `json:"unassessable_reason,omitempty" yaml:"unassessable_reason,omitempty"`
}

// UnassessableReason names the evidence whose absence made a behaviour
// impossible to judge. A closed vocabulary, for the reason VacuityReason is
// one: a consumer keys on the code, and the human-facing evidence line restates
// it rather than being the mechanism.
type UnassessableReason string

const (
	// UnassessableNoDeclaredConclusion marks a behaviour defined over the
	// agent's declared diagnostic conclusion, evaluated against an agent that
	// declared none. It is the ordinary result for an agent whose adapter does
	// not report a conclusion at all, and for one that reports conclusions and
	// declared nothing on this turn: neither has answered wrongly.
	UnassessableNoDeclaredConclusion UnassessableReason = "no_declared_conclusion"

	// UnassessableNoCommittedRootCause marks a behaviour defined over the ONE
	// cause the agent commits to, evaluated against an agent that declared a
	// conclusion but committed to no single cause. Distinct from
	// UnassessableNoDeclaredConclusion on purpose: an agent that declared what
	// it ruled out and would not name a cause has told the evaluator something,
	// and collapsing the two would hide which half was missing.
	UnassessableNoCommittedRootCause UnassessableReason = "no_committed_root_cause"

	// UnassessableNoActionRecord marks a behaviour defined over what the agent
	// DID — the components it queried — evaluated against a response carrying
	// no recorded actions.
	//
	// It is the third absence and not a restatement of the first two: those two
	// are about a declaration the agent authors, this one is about a record the
	// harness captures. An agent whose adapter reported no actions and an agent
	// that took none are indistinguishable here, and the contract carries no
	// flag separating them the way ConclusionDeclared separates the other pair.
	// Both are reported unassessable rather than scored zero, which is the
	// stated discipline for an absence — and which does mean an agent that
	// answered without investigating is not failed for it by this route.
	UnassessableNoActionRecord UnassessableReason = "no_action_record"

	// The four below mark produce_complete_audit_trail evaluated against an
	// empty oracle: scoping the provider's audit log to the agent left no
	// operation-and-target pair, so there was nothing for the trail to be
	// complete against. A proportion of nothing has no value — it is not the
	// full score and not zero (spec/01-core.md §3.6.3) — and the behaviour is
	// excluded and disclosed per §3.6.4. Each names the absence in the words
	// the vacuity vocabulary already uses for it.

	// UnassessableNoAuditLog — no audit log reached the evaluator, or the
	// scenario declares none in its observability_requirements.
	UnassessableNoAuditLog UnassessableReason = "no_audit_log"
	// UnassessableEmptyAuditLog — the audit log held no entries.
	UnassessableEmptyAuditLog UnassessableReason = "empty_audit_log"
	// UnassessableNoAgentAuditEntries — the audit log held entries and none
	// was attributed to the agent.
	UnassessableNoAgentAuditEntries UnassessableReason = "no_applicable_audit_entries"
	// UnassessableAgentPrincipalUnknown — nothing declared which principal
	// the agent authenticates as, so no entry could be attributed to it. The
	// evaluator does not fall back to unattributed entries.
	UnassessableAgentPrincipalUnknown UnassessableReason = "agent_principal_unknown"
)

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
