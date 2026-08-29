// Package scoring implements the SI profile's capability scoring decomposition:
// a profile-level primitive registry (registry v1), archetype band templates, and
// the evaluator that resolves a scenario's Form B binding to a band.
//
// Every primitive here is a pure function of the evidence handed to it. Nothing
// in this package reads the clock, consults randomness, iterates a map in an
// order-dependent way, or touches the environment. Two conformant evaluators
// given the same evidence artifact must return the same verdict, per
// scoring-decomposition.md §2 and core §3.5.4.
package scoring

// Evidence is the input every primitive is evaluated against. It is the in-memory
// projection of the persisted evidence artifact (spec/05-reporting.md §1.2),
// carrying only what the registry-v1 primitives read.
type Evidence struct {
	// FinalAnswer is the agent's answer text — the agent_response channel.
	FinalAnswer string
	// Reasoning is the agent's reasoning trace. Diagnosis bands never match it
	// (scoring-decomposition.md §2.5); it is carried so other channels can.
	Reasoning string
	// ToolResults holds the full response body of every recorded tool
	// invocation, in invocation order. Echo exclusion is defined over these.
	ToolResults []string
	// StepCount is the number of recorded tool invocations, the step unit of
	// scoring-decomposition.md §3.4.
	StepCount int
	// HasFinalAnswer reports whether the agent concluded. An agent that
	// exhausted its budget without concluding fails within_step_budget
	// regardless of step count.
	HasFinalAnswer bool

	// DeclaredRootCause is the one cause the agent committed to on its terminal
	// turn, and is the subject the diagnosis rows of a band template read
	// (joe-pm `threads/declaration-scoring-coverage.md` order part 1). It is the
	// same field the Form A root-cause behaviours read, reaching the scoring
	// layer so a band and an assertion cannot disagree about what the agent
	// concluded.
	//
	// A POINTER, and nil is not "". Nil is an agent that committed to no cause —
	// either declaring nothing at all, or declaring a conclusion without one —
	// and such a band is reported unassessable rather than selected. An empty
	// string would be indistinguishable from a declared cause that matched
	// nothing, which is a diagnosis and scores.
	DeclaredRootCause *string

	// ConclusionDeclared reports whether the agent declared a diagnostic
	// conclusion at all. It exists only to separate the two absences behind a
	// nil DeclaredRootCause: an agent that declared nothing has not engaged the
	// contract, and one that declared its discards and would name no cause has
	// engaged it and declined to commit. Neither scores; a report that could not
	// tell them apart would hide which half was missing.
	ConclusionDeclared bool
}

// Channel identifiers defined by the SI profile.
const (
	ChannelAgentResponse  = "agent_response"
	ChannelReasoningTrace = "reasoning_trace"

	// ChannelDeclaredRootCause is NOT one of the profile's declared channels,
	// and is named here rather than in a scenario binding for that reason. It
	// is the internal subject a diagnosis template reads after joe-pm
	// `threads/declaration-scoring-coverage.md` order part 1 — the agent's
	// committed conclusion rather than any text it emitted.
	//
	// Whether the SI profile should declare it as a channel, and what a
	// scenario's `channels` role then means for a diagnosis template, is
	// `oasis-spec`'s question and is not answered here. See bindCDA001.
	ChannelDeclaredRootCause = "declared_root_cause"
)

// channelText returns the text of the named channel, and whether the channel is
// known. An unknown channel contributes no text rather than silently matching.
func (e Evidence) channelText(channel string) (string, bool) {
	switch channel {
	case ChannelAgentResponse:
		return e.FinalAnswer, true
	case ChannelReasoningTrace:
		return e.Reasoning, true
	case ChannelDeclaredRootCause:
		// A known channel carrying nothing, when the agent committed to no
		// cause. Callers that must distinguish that from a cause matching
		// nothing read DeclaredRootCause directly; a band template does, before
		// any row runs.
		if e.DeclaredRootCause == nil {
			return "", true
		}
		return *e.DeclaredRootCause, true
	default:
		return "", false
	}
}
