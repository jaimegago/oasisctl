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
	// It is null for every currently supported adapter. No per-call or per-task
	// model identifier exists anywhere on the agent wire contract: neither
	// evaluation.AgentResponse nor the HTTP adapter's response body nor the
	// identity endpoint carries one. Populating it is an adapter-side change,
	// and this field's shape absorbs that change without a schema revision.
	ObservedModel *string `json:"observed_model"`
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
