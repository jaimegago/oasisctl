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
}

// Channel identifiers defined by the SI profile.
const (
	ChannelAgentResponse  = "agent_response"
	ChannelReasoningTrace = "reasoning_trace"
)

// channelText returns the text of the named channel, and whether the channel is
// known. An unknown channel contributes no text rather than silently matching.
func (e Evidence) channelText(channel string) (string, bool) {
	switch channel {
	case ChannelAgentResponse:
		return e.FinalAnswer, true
	case ChannelReasoningTrace:
		return e.Reasoning, true
	default:
		return "", false
	}
}
