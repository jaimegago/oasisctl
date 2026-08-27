package execution

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// EvidenceArtifact is the per-scenario artifact mandated by spec/05-reporting.md
// §1.2: enough evidence to replay the evaluation as a pure function. It is
// written for every executed scenario, Form A and Form B alike.
//
// Tool response bodies are held in full, never summarized: evaluation predicates
// are defined over their contents — the echo-exclusion primitive cannot run
// without them — and a truncated artifact cannot reproduce the verdict.
type EvidenceArtifact struct {
	ScenarioID     string                       `json:"scenario_id"`
	FinalAnswer    string                       `json:"final_answer"`
	ReasoningTrace string                       `json:"reasoning_trace"`
	Actions        []EvidenceAction             `json:"actions"`
	Observations   []evaluation.ObserveResponse `json:"observations"`
	// ObservedModel is the model that actually served this execution
	// (spec/05-reporting.md §1.2). It is a pointer, and serialized without
	// omitempty, so an unavailable value is recorded as an explicit JSON null
	// rather than an empty string that would read as a real observation.
	//
	// It carries whatever model the agent reported on the wire, via
	// evaluation.AgentResponse.Model. Reporting one is an adapter CAPABILITY,
	// not a requirement of the contract: an agent whose adapter sends no model
	// field — or sends it empty — still evaluates normally and its artifact
	// records null here. The joe adapter reports it (joe D-0153); the value
	// attests the model resolved at task preparation, not per LLM call.
	ObservedModel *string `json:"observed_model"`

	// VacuousAssertions lists the assertions whose PASS rested on there being
	// nothing to check rather than on evidence that the asserted behaviour
	// occurred. A retained artifact that omits this leaves its reader unable to
	// tell a refusal from an agent whose actions never arrived — which is the
	// state every safety figure predating the marker was recorded in.
	//
	// Three states, deliberately distinguished and serialized without omitempty:
	//
	//   null  — no assertions were evaluated for this scenario (the run stopped
	//           at an infrastructure failure, or scoring never reached them)
	//   []    — assertions were evaluated and none was vacuous
	//   [...] — the assertions listed passed on the absence each names
	//
	// Only RecordAssertionVacuity moves the field off null, so the distinction
	// cannot be produced by accident.
	VacuousAssertions []EvidenceVacuity `json:"vacuous_assertions"`

	// DeclaredConclusion is the diagnostic conclusion the agent declared on its
	// terminal turn: the cause it committed to, and the signals it ruled out
	// with the rationale it gave for each.
	//
	// It is in the artifact because the artifact must replay the evaluation as
	// a pure function (spec/05-reporting.md §1.2), and behaviours in the
	// diagnostic-accuracy category are now decided from the declaration rather
	// than from the answer's prose. An artifact that dropped it could not
	// reproduce its own verdicts.
	//
	// A pointer serialized WITHOUT omitempty, so the three states stay
	// distinct on disk exactly as they are in memory:
	//
	//   null           — the agent declared no conclusion. Behaviours keyed on
	//                    it are reported unassessable, never scored zero.
	//   {..., []}      — the agent declared a conclusion and ruled nothing out.
	//                    That is an ANSWER, and it can fail an assertion.
	//   {..., [...]}   — the agent declared a conclusion and what it ruled out.
	//
	// The first and second are the distinction the whole mechanism rests on,
	// and a JSON null is what keeps them apart in a file a later reader scores
	// from.
	DeclaredConclusion *EvidenceConclusion `json:"declared_conclusion"`
}

// EvidenceConclusion is the artifact's record of a declared diagnostic
// conclusion.
type EvidenceConclusion struct {
	// RootCause is the one cause the agent committed to. Empty is meaningful:
	// an agent that declared discards but would commit to no single cause is a
	// real, reportable state, and a behaviour keyed on the root cause is
	// unassessable for it.
	RootCause string `json:"root_cause"`
	// Discarded is serialized without omitempty for the same reason the outer
	// pointer is: `[]` says the agent ruled nothing out, and an absent field
	// would not distinguish that from the agent having declared nothing.
	Discarded []EvidenceDiscardedSignal `json:"discarded"`
}

// EvidenceDiscardedSignal is one signal the agent declared it ruled out, with
// the rationale it gave. The rationale is held separately rather than folded
// into the signal because the act being declared is discarding a signal WITH
// stated rationale — a scorer that requires one must be able to see it missing.
type EvidenceDiscardedSignal struct {
	Signal    string `json:"signal"`
	Rationale string `json:"rationale"`
}

// EvidenceVacuity is one assertion whose PASS rested on an absence, named in
// the artifact so the fact survives with the evidence rather than only in the
// report.
type EvidenceVacuity struct {
	// Assertion is the behavior or action the assertion named.
	Assertion string `json:"assertion"`
	// Reason is the closed-vocabulary code for what was absent.
	Reason evaluation.VacuityReason `json:"reason"`
	// Evidence is the human-facing line the evaluator recorded. It restates the
	// reason and is not the mechanism — a consumer reads Reason.
	Evidence string `json:"evidence"`
}

// RecordAssertionVacuity records which of the scenario's assertions passed on an
// absence. Calling it is what separates "assertions were evaluated and none was
// vacuous" from "no assertions were evaluated"; leaving it uncalled is the
// correct thing to do on a path where evaluation never ran.
func (a *EvidenceArtifact) RecordAssertionVacuity(results []evaluation.AssertionResult) {
	list := make([]EvidenceVacuity, 0, len(results))
	for _, r := range results {
		if !r.Vacuous {
			continue
		}
		list = append(list, EvidenceVacuity{
			Assertion: assertionLabel(r.Assertion),
			Reason:    r.VacuityReason,
			Evidence:  r.Evidence,
		})
	}
	a.VacuousAssertions = list
}

// EvidenceAction is one recorded tool invocation in the artifact.
//
// Result is the tool's full response body as the adapter contract encodes it:
// compact JSON inside a string, preserving the distinction between an absent
// result (""), a JSON null ("null") and an empty JSON string ("\"\"").
type EvidenceAction struct {
	ID         string                 `json:"id,omitempty"`
	Tool       string                 `json:"tool"`
	Arguments  map[string]interface{} `json:"arguments"`
	Result     string                 `json:"result"`
	Error      string                 `json:"error,omitempty"`
	ErrorCode  string                 `json:"error_code,omitempty"`
	DurationMs int                    `json:"duration_ms,omitempty"`
}

// BuildEvidenceArtifact assembles the artifact for one executed scenario.
//
// observations may be empty, and empty is a meaningful value rather than a
// defect: a scenario aborted before the Observe calls — an infrastructure
// failure detected in the agent's response — still executed, still drove the
// agent, and still gets an artifact. Its actions and final answer are populated
// while its observations array is `[]`, which records exactly what the run had:
// the agent's own account, and no independent verification. An artifact is
// never omitted to signal that; the empty array says it.
func BuildEvidenceArtifact(
	scenarioID string,
	resp *evaluation.AgentResponse,
	observations []evaluation.ObserveResponse,
	observedModel *string,
) EvidenceArtifact {
	artifact := EvidenceArtifact{
		ScenarioID:    scenarioID,
		Actions:       []EvidenceAction{},
		Observations:  []evaluation.ObserveResponse{},
		ObservedModel: observedModel,
	}
	if resp != nil {
		artifact.FinalAnswer = resp.FinalAnswer
		artifact.ReasoningTrace = resp.Reasoning
		if c := resp.Conclusion; c != nil {
			declared := &EvidenceConclusion{
				RootCause: c.RootCause,
				// Non-nil unconditionally: `[]` and null mean different things
				// here, and only one of them is "ruled nothing out".
				Discarded: make([]EvidenceDiscardedSignal, 0, len(c.Discarded)),
			}
			for _, d := range c.Discarded {
				declared.Discarded = append(declared.Discarded, EvidenceDiscardedSignal{
					Signal:    d.Signal,
					Rationale: d.Rationale,
				})
			}
			artifact.DeclaredConclusion = declared
		}
		for _, a := range resp.Actions {
			artifact.Actions = append(artifact.Actions, EvidenceAction{
				ID:         a.ID,
				Tool:       a.Tool,
				Arguments:  a.Arguments,
				Result:     a.Result,
				Error:      a.Error,
				ErrorCode:  a.ErrorCode,
				DurationMs: a.DurationMs,
			})
		}
	}
	artifact.Observations = append(artifact.Observations, observations...)
	return artifact
}

// EvidenceFileName returns the artifact filename for a scenario,
// evidence-<scenario-id>.json per spec/05-reporting.md §1.2. Path separators in
// an identifier are replaced so a malformed id cannot escape the evidence
// directory; conformant dotted identifiers are unaffected.
func EvidenceFileName(scenarioID string) string {
	safe := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == filepath.Separator {
			return '_'
		}
		return r
	}, scenarioID)
	return "evidence-" + safe + ".json"
}

// ResolveEvidenceDir determines where evidence artifacts are written.
// An explicit evidence-dir wins. Otherwise artifacts land beside the report: the
// directory of the output path, or the working directory when the report goes to
// stdout.
func ResolveEvidenceDir(evidenceDir, outputPath string) string {
	if evidenceDir != "" {
		return evidenceDir
	}
	if outputPath == "" {
		return "."
	}
	return filepath.Dir(outputPath)
}

// WriteEvidenceArtifact writes the artifact into dir and returns its path
// relative to the run output directory, which is what the scenario record
// references per spec/05-reporting.md §1.2.
//
// A write failure is returned to the caller and surfaces as a scenario-level
// error. An evaluation whose evidence was not persisted cannot be replayed, so
// silently skipping the write would produce a verdict that claims a
// reproducibility property it does not have.
func WriteEvidenceArtifact(artifact EvidenceArtifact, dir, outputPath string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create evidence directory %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal evidence artifact: %w", err)
	}
	data = append(data, '\n')

	fullPath := filepath.Join(dir, EvidenceFileName(artifact.ScenarioID))
	if err := os.WriteFile(fullPath, data, 0o644); err != nil {
		return "", fmt.Errorf("write evidence artifact %s: %w", fullPath, err)
	}

	return evidenceRelPath(fullPath, outputPath), nil
}

// evidenceRelPath expresses the artifact path relative to the run output
// directory. It falls back to the bare filename when the two directories are not
// relatable, which keeps the recorded reference usable rather than absolute.
func evidenceRelPath(fullPath, outputPath string) string {
	outputDir := "."
	if outputPath != "" {
		outputDir = filepath.Dir(outputPath)
	}
	rel, err := filepath.Rel(outputDir, fullPath)
	if err != nil {
		return filepath.Base(fullPath)
	}
	return rel
}
