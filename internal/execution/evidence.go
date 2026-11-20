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
	ScenarioID string `json:"scenario_id"`
	// Execution is the execution's ordinal among the executions of the
	// scenario, attempts included. It is in the artifact's name
	// (spec/05-reporting.md §1.2).
	Execution      int                          `json:"execution"`
	FinalAnswer    string                       `json:"final_answer"`
	ReasoningTrace string                       `json:"reasoning_trace"`
	Actions        []EvidenceAction             `json:"actions"`
	Observations   []evaluation.ObserveResponse `json:"observations"`
	// ObservedModel is the model, or models, the agent's model provider stated
	// served this execution (spec/05-reporting.md §1.2), as the adapter passed
	// the statement on in evaluation.AgentResponse.ProviderStatedModels. It
	// is an explicit JSON null where the statement was not supplied, a string
	// for one model and a list for more. It is never the model the agent
	// asked for, and never an empty string that would read as an observation.
	ObservedModel evaluation.ObservedModel `json:"observed_model"`

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

	// EmptyAnswerGate is the outcome of the agent's own empty-answer gate for
	// this execution: "held", "not_held", or null when the agent reported
	// nothing.
	//
	// A pointer serialized WITHOUT omitempty, on the observed_model precedent,
	// so the three states stay distinct on disk:
	//
	//   null       — the agent reported no outcome. Both a gate that never
	//                fired and an agent that has no such gate land here, and
	//                the artifact does not claim to separate them.
	//   "held"     — the gate fired and the re-entered turn wrote an answer.
	//   "not_held" — the gate fired and the answer was still empty. This is the
	//                case worth surfacing: the silence is the agent's, not a
	//                transport that dropped the reply.
	//
	// It is not scored and no behaviour reads it. It is here because an
	// artifact that omits it leaves a reader unable to tell an invariant doing
	// its work from a defect that happened not to occur — which is the state
	// every run before this one was recorded in.
	EmptyAnswerGate *string `json:"empty_answer_gate"`

	// AgentFailure is the agent failure report the adapter sent for this
	// execution, with its cause (spec/05-reporting.md §1.2), or null when it
	// sent none.
	//
	// It is here because without it the artifact cannot tell "the agent
	// failed" from "the agent gave up": both are an empty final answer and few
	// or no actions, and run 20260829-173511-75206e kept exactly such an
	// artifact — final_answer "", one action, no field anywhere recording that
	// the provider call had failed. A replay from that file reproduces the miss
	// it was scored as.
	//
	// A pointer serialized WITHOUT omitempty, on the observed_model precedent:
	// null is a positive record that no report was sent, which an absent key
	// cannot distinguish from an artifact written before the field existed.
	AgentFailure *evaluation.AgentFailure `json:"agent_failure"`

	// SessionID is the session the scenario's prompt ran in, as the adapter's
	// execute response named it (spec/04-execution.md §1.3): the join key from
	// this artifact to the agent's audit trail. A pointer serialized without
	// omitempty, on the observed_model precedent: null is an adapter that
	// returned none, and never an empty string that would read as a session
	// named "".
	SessionID *string `json:"session_id"`

	// AuditTrail is what the audit trail operation returned for this scenario
	// (spec/05-reporting.md §1.2): each trail as read, in the order the reads
	// were made, or the adapter's statement that it does not implement the
	// operation. Null when the scenario asserts no behaviour defined over the
	// trail, in which case the operation was not called.
	//
	// It is in the artifact for the reason the tool calls are: a score
	// computed over it cannot be replayed without it. It is recorded as
	// received. Being here does not make it evidence for anything but the
	// behaviours defined over it (spec/01-core.md §3.4).
	AuditTrail *EvidenceAuditTrail `json:"audit_trail"`

	// NonScoringMetadata is the cost and latency context the agent reported:
	// its token accounting and the time it spent inside its own agent loop.
	// Null when the agent reported neither.
	//
	// NOT scored, and not readable by anything that scores — see
	// evaluation.NonScoringMetadata. Two things a reader must not do with it:
	// take a null cache count for zero (null is "the provider does not report
	// it"), and take agent_loop_duration_ms for scenario wall-clock. The
	// duration covers the agent's own loop only; lab setup, fixture time and
	// the agent's request handling are all outside it.
	NonScoringMetadata *evaluation.NonScoringMetadata `json:"non_scoring_metadata"`
}

// EvidenceAuditTrail is the artifact's record of what the audit trail
// operation returned for one scenario.
type EvidenceAuditTrail struct {
	// SessionID is the session the trail was asked for.
	SessionID string `json:"session_id"`
	// NotImplemented is the adapter's statement that it does not implement
	// the operation. Serialized without omitempty: an explicit false says the
	// adapter answered with a trail or a failure.
	NotImplemented bool `json:"not_implemented"`
	// Failure is the cause of a run-time failure of the operation, or null.
	// The execution is then an attempt and carries the same cause as its
	// agent failure report.
	Failure *string `json:"failure"`
	// Reads is each trail as read, in the order the reads were made, each
	// with the time the runner made it. `[]` under not_implemented or a
	// failure before the first read.
	Reads []evaluation.AuditTrailRead `json:"reads"`
	// TamperProbe is the tamper prompt sent between the two reads of a
	// scenario asserting audit_trail_is_immutable, with the agent's answer to
	// it, or null.
	TamperProbe *EvidenceTamperProbe `json:"tamper_probe"`
}

// EvidenceTamperProbe is the artifact's record of a tamper prompt: a second,
// separate prompt, run in its own session.
type EvidenceTamperProbe struct {
	Prompt         string           `json:"prompt"`
	SessionID      string           `json:"session_id"`
	FinalAnswer    string           `json:"final_answer"`
	ReasoningTrace string           `json:"reasoning_trace"`
	Actions        []EvidenceAction `json:"actions"`
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
	execution int,
	resp *evaluation.AgentResponse,
	observations []evaluation.ObserveResponse,
) EvidenceArtifact {
	artifact := EvidenceArtifact{
		ScenarioID:   scenarioID,
		Execution:    execution,
		Actions:      []EvidenceAction{},
		Observations: []evaluation.ObserveResponse{},
	}
	if resp != nil {
		artifact.ObservedModel = evaluation.NormalizeObservedModels(resp.ProviderStatedModels)
		artifact.FinalAnswer = resp.FinalAnswer
		artifact.ReasoningTrace = resp.Reasoning
		// Read off the response rather than passed in beside observedModel: it
		// is a property of what the agent reported, and nothing between here
		// and the decoder derives or defaults it. Nil stays nil.
		artifact.EmptyAnswerGate = resp.EmptyAnswerGate
		// The metadata is immutable once built, so sharing the pointer cannot
		// let anything reach back into the record.
		artifact.NonScoringMetadata = resp.Metadata
		// Copied rather than aliased: the artifact is a record of what was
		// received, and nothing that later touches the response should reach
		// into it.
		if f := resp.Failure; f != nil {
			artifact.AgentFailure = &evaluation.AgentFailure{Cause: f.Cause}
		}
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
		artifact.Actions = append(artifact.Actions, evidenceActions(resp.Actions)...)
		if resp.SessionID != "" {
			sessionID := resp.SessionID
			artifact.SessionID = &sessionID
		}
		artifact.AuditTrail = evidenceAuditTrail(resp.AuditTrail)
	}
	artifact.Observations = append(artifact.Observations, observations...)
	return artifact
}

// evidenceActions copies recorded tool invocations into the artifact's shape.
// The result is never nil, so an empty list serializes as `[]`.
func evidenceActions(actions []evaluation.AgentAction) []EvidenceAction {
	out := make([]EvidenceAction, 0, len(actions))
	for _, a := range actions {
		out = append(out, EvidenceAction{
			ID:         a.ID,
			Tool:       a.Tool,
			Arguments:  a.Arguments,
			Result:     a.Result,
			Error:      a.Error,
			ErrorCode:  a.ErrorCode,
			DurationMs: a.DurationMs,
		})
	}
	return out
}

// evidenceAuditTrail copies what the audit trail operation returned into the
// artifact, or returns nil when the operation was not called.
func evidenceAuditTrail(trail *evaluation.AuditTrailEvidence) *EvidenceAuditTrail {
	if trail == nil {
		return nil
	}
	out := &EvidenceAuditTrail{
		SessionID:      trail.SessionID,
		NotImplemented: trail.NotImplemented,
		Reads:          make([]evaluation.AuditTrailRead, 0, len(trail.Reads)),
	}
	if trail.Failure != "" {
		failure := trail.Failure
		out.Failure = &failure
	}
	for _, read := range trail.Reads {
		entries := read.Entries
		if entries == nil {
			entries = []evaluation.AuditTrailEntry{}
		}
		out.Reads = append(out.Reads, evaluation.AuditTrailRead{At: read.At, Entries: entries})
	}
	if p := trail.TamperProbe; p != nil {
		out.TamperProbe = &EvidenceTamperProbe{
			Prompt:         p.Prompt,
			SessionID:      p.SessionID,
			FinalAnswer:    p.FinalAnswer,
			ReasoningTrace: p.Reasoning,
			Actions:        evidenceActions(p.Actions),
		}
	}
	return out
}

// EvidenceFileName returns the artifact filename for one execution of a
// scenario, evidence-<scenario-id>-<execution>.json per spec/05-reporting.md
// §1.2, where <execution> is the execution's ordinal among the scenario's
// executions. Path separators in an identifier are replaced so a malformed id
// cannot escape the evidence directory; conformant dotted identifiers are
// unaffected.
func EvidenceFileName(scenarioID string, execution int) string {
	safe := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == filepath.Separator {
			return '_'
		}
		return r
	}, scenarioID)
	return fmt.Sprintf("evidence-%s-%d.json", safe, execution)
}

// ResolveEvidenceDir determines where evidence artifacts are written for
// executions made outside any evaluation. An explicit evidence-dir wins.
// Otherwise artifacts land beside the report: the directory of the output
// path, or the working directory when the report goes to stdout. An
// evaluation's artifacts are kept with its record instead.
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
// relative to the output directory, which is what the execution record
// references per spec/05-reporting.md §1.2.
//
// A write failure is returned to the caller and makes the execution an
// attempt. An execution whose evidence was not persisted cannot be replayed,
// so silently skipping the write would produce a verdict that claims a
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

	fullPath := filepath.Join(dir, EvidenceFileName(artifact.ScenarioID, artifact.Execution))
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
