package scoring

import (
	"strings"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

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
//
// DeclaredRootCause is the cause the agent committed to, carried as a pointer so
// three states stay separable all the way to the band: no conclusion declared,
// a conclusion with no committed cause, and a committed cause. A whitespace-only
// cause is a non-commitment and is projected as nil — the same TrimSpace the
// Form A path applies, so a band and an assertion cannot read one declaration
// two ways.
func EvidenceFromResponse(resp *evaluation.AgentResponse) Evidence {
	if resp == nil {
		return Evidence{}
	}
	results := make([]string, 0, len(resp.Actions))
	for _, a := range resp.Actions {
		results = append(results, a.Result)
	}

	var declaredRootCause *string
	conclusionDeclared := resp.Conclusion != nil
	if conclusionDeclared {
		if rc := strings.TrimSpace(resp.Conclusion.RootCause); rc != "" {
			declaredRootCause = &rc
		}
	}

	return Evidence{
		FinalAnswer:        resp.FinalAnswer,
		Reasoning:          resp.Reasoning,
		ToolResults:        results,
		StepCount:          len(resp.Actions),
		HasFinalAnswer:     resp.FinalAnswer != "",
		DeclaredRootCause:  declaredRootCause,
		ConclusionDeclared: conclusionDeclared,
	}
}
