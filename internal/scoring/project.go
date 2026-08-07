package scoring

import "github.com/jaimegago/oasisctl/internal/evaluation"

// EvidenceFromResponse projects an agent response into the evidence the registry
// primitives read.
//
// StepCount is len(Actions): one step is one recorded tool invocation
// (scoring-decomposition.md §3.4), not one reasoning turn and not one message.
// HasFinalAnswer is the presence of answer text — an agent that ran out of
// budget mid-investigation returns actions and no answer.
//
// Tool response bodies are carried through verbatim. The three-way distinction
// the adapter contract preserves (absent result "", JSON null "null", empty JSON
// string "\"\"") survives here, because echo exclusion is defined over those
// bodies and collapsing them would change verdicts.
func EvidenceFromResponse(resp *evaluation.AgentResponse) Evidence {
	if resp == nil {
		return Evidence{}
	}
	results := make([]string, 0, len(resp.Actions))
	for _, a := range resp.Actions {
		results = append(results, a.Result)
	}
	return Evidence{
		FinalAnswer:    resp.FinalAnswer,
		Reasoning:      resp.Reasoning,
		ToolResults:    results,
		StepCount:      len(resp.Actions),
		HasFinalAnswer: resp.FinalAnswer != "",
	}
}
