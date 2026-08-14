package execution

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// goldenResponse is adapter-shaped: per-action id, tool, arguments, result as a
// compact-JSON string, error fields and duration. The three-way result
// distinction is represented deliberately — a real body, a JSON null, and an
// absent result.
func goldenResponse() *evaluation.AgentResponse {
	return &evaluation.AgentResponse{
		FinalAnswer: "The SMTP_PORT key is missing from the smtp-config ConfigMap.",
		Reasoning:   "Read the deployment, then the ConfigMap.",
		Actions: []evaluation.AgentAction{
			{
				ID:         "call_1",
				Tool:       "kubectl_get",
				Arguments:  map[string]interface{}{"kind": "configmap", "name": "smtp-config"},
				Result:     `{"data":{"SMTP_HOST":"smtp.internal"}}`,
				DurationMs: 12,
			},
			{
				ID:        "call_2",
				Tool:      "kubectl_logs",
				Arguments: map[string]interface{}{"pod": "notification-service-abc"},
				Result:    "null",
				Error:     "container not ready",
				ErrorCode: "not_ready",
			},
			{
				ID:        "call_3",
				Tool:      "kubectl_describe",
				Arguments: map[string]interface{}{"kind": "deployment"},
				Result:    "",
			},
		},
	}
}

// twoObservations is a multi-observation fixture. Observation order in the
// artifact must be stable even though the orchestrator derives the set from a Go
// map.
func twoObservations() []evaluation.ObserveResponse {
	ts := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	return []evaluation.ObserveResponse{
		{
			EnvironmentID:   "env-1",
			Timestamp:       ts,
			ObservationType: "audit_log",
			Data:            map[string]interface{}{"entries": []interface{}{}},
			EvidenceSource:  &evaluation.EvidenceSource{Type: "audit_log_file", Status: "available"},
		},
		{
			EnvironmentID:   "env-1",
			Timestamp:       ts,
			ObservationType: "resource_state",
			Data:            map[string]interface{}{"kind": "ConfigMap", "name": "smtp-config"},
			EvidenceSource:  &evaluation.EvidenceSource{Type: "kube_api", Status: "available"},
		},
	}
}

// TestEvidenceArtifact_Golden pins the artifact's content and location.
func TestEvidenceArtifact_Golden(t *testing.T) {
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "report.yaml")

	artifact := BuildEvidenceArtifact(
		"infra.capability.da.single-signal-diagnosis-001",
		goldenResponse(),
		twoObservations(),
		nil,
	)
	relPath, err := WriteEvidenceArtifact(artifact, dir, outputPath)
	require.NoError(t, err)

	t.Run("location", func(t *testing.T) {
		assert.Equal(t, "evidence-infra.capability.da.single-signal-diagnosis-001.json", relPath,
			"the recorded reference is relative to the run output directory")
		assert.FileExists(t, filepath.Join(dir, relPath))
	})

	raw, err := os.ReadFile(filepath.Join(dir, relPath))
	require.NoError(t, err)

	t.Run("observed model is an explicit null, not an empty string", func(t *testing.T) {
		var loose map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &loose))
		require.Contains(t, loose, "observed_model")
		assert.JSONEq(t, "null", string(loose["observed_model"]))
	})

	var got EvidenceArtifact
	require.NoError(t, json.Unmarshal(raw, &got))

	t.Run("content", func(t *testing.T) {
		assert.Equal(t, "infra.capability.da.single-signal-diagnosis-001", got.ScenarioID)
		assert.Equal(t, "The SMTP_PORT key is missing from the smtp-config ConfigMap.", got.FinalAnswer)
		assert.Equal(t, "Read the deployment, then the ConfigMap.", got.ReasoningTrace)
		assert.Nil(t, got.ObservedModel)

		require.Len(t, got.Actions, 3)
		assert.Equal(t, "call_1", got.Actions[0].ID)
		assert.Equal(t, "kubectl_get", got.Actions[0].Tool)
		assert.Equal(t, map[string]interface{}{"kind": "configmap", "name": "smtp-config"}, got.Actions[0].Arguments)
		assert.Equal(t, 12, got.Actions[0].DurationMs)

		// Tool response bodies survive in full, and the three-way distinction
		// echo exclusion depends on is preserved byte for byte.
		assert.Equal(t, `{"data":{"SMTP_HOST":"smtp.internal"}}`, got.Actions[0].Result)
		assert.Equal(t, "null", got.Actions[1].Result)
		assert.Equal(t, "", got.Actions[2].Result)
		assert.Equal(t, "container not ready", got.Actions[1].Error)
		assert.Equal(t, "not_ready", got.Actions[1].ErrorCode)

		require.Len(t, got.Observations, 2)
		assert.Equal(t, "audit_log", got.Observations[0].ObservationType)
		assert.Equal(t, "resource_state", got.Observations[1].ObservationType)
		require.NotNil(t, got.Observations[0].EvidenceSource)
		assert.Equal(t, "available", got.Observations[0].EvidenceSource.Status)
		assert.Equal(t, "audit_log_file", got.Observations[0].EvidenceSource.Type)
	})

	t.Run("bytes are stable across rewrites", func(t *testing.T) {
		for i := 0; i < 5; i++ {
			again := BuildEvidenceArtifact(
				"infra.capability.da.single-signal-diagnosis-001",
				goldenResponse(),
				twoObservations(),
				nil,
			)
			_, err := WriteEvidenceArtifact(again, dir, outputPath)
			require.NoError(t, err)

			rewritten, err := os.ReadFile(filepath.Join(dir, relPath))
			require.NoError(t, err)
			require.Equal(t, string(raw), string(rewritten),
				"a two-observation artifact must be byte-identical on every write")
		}
	})
}

// TestEvidenceArtifact_ObservedModelPopulated is the golden test's other half:
// the same artifact built for an agent that DOES report a model. The null case
// above proves an absent model is recorded as an explicit JSON null; this proves
// a reported one is recorded verbatim as a JSON string, so both states of the
// field are pinned and neither can silently become the other.
func TestEvidenceArtifact_ObservedModelPopulated(t *testing.T) {
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "report.yaml")

	model := "claude-sonnet-4-20250514"
	artifact := BuildEvidenceArtifact(
		"infra.capability.da.single-signal-diagnosis-001",
		goldenResponse(),
		twoObservations(),
		&model,
	)
	relPath, err := WriteEvidenceArtifact(artifact, dir, outputPath)
	require.NoError(t, err)

	raw, err := os.ReadFile(filepath.Join(dir, relPath))
	require.NoError(t, err)

	var loose map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &loose))
	require.Contains(t, loose, "observed_model")
	assert.JSONEq(t, `"claude-sonnet-4-20250514"`, string(loose["observed_model"]))

	var got EvidenceArtifact
	require.NoError(t, json.Unmarshal(raw, &got))
	require.NotNil(t, got.ObservedModel)
	assert.Equal(t, "claude-sonnet-4-20250514", *got.ObservedModel)

	// Nothing else about the artifact moves when the model is present.
	assert.Equal(t, "The SMTP_PORT key is missing from the smtp-config ConfigMap.", got.FinalAnswer)
	require.Len(t, got.Actions, 3)
	require.Len(t, got.Observations, 2)
}

// TestCollectObservations_DeterministicOrder covers the ordering hazard directly:
// the observation type set is derived from a Go map, whose iteration order is
// randomized, so a scenario needing two observation types would otherwise
// produce a different artifact on different runs.
func TestCollectObservations_DeterministicOrder(t *testing.T) {
	verification := evaluation.Verification{
		StateAssertions: []evaluation.StateAssertion{{Description: "state"}},
		APIAudit:        []evaluation.APIAuditItem{{Required: "get"}},
		StateDiff:       map[string]interface{}{"before": "x"},
	}

	var first []string
	for run := 0; run < 20; run++ {
		types := observationTypesFromVerification(verification)
		ordered := sortedObservationTypes(types)
		if first == nil {
			first = ordered
			continue
		}
		require.Equal(t, first, ordered, "observation collection order must not vary between runs")
	}
	assert.Equal(t, []string{"audit_log", "resource_state", "state_diff"}, first)
}

// TestResolveEvidenceDir covers the default-location rule.
func TestResolveEvidenceDir(t *testing.T) {
	tests := []struct {
		name        string
		evidenceDir string
		outputPath  string
		want        string
	}{
		{
			name:       "stdout report writes evidence into the working directory",
			outputPath: "",
			want:       ".",
		},
		{
			name:       "file report writes evidence beside the report",
			outputPath: filepath.Join("out", "run", "report.json"),
			want:       filepath.Join("out", "run"),
		},
		{
			name:       "bare filename report writes evidence into the working directory",
			outputPath: "report.json",
			want:       ".",
		},
		{
			name:        "an explicit evidence directory wins",
			evidenceDir: filepath.Join("artifacts", "evidence"),
			outputPath:  filepath.Join("out", "report.json"),
			want:        filepath.Join("artifacts", "evidence"),
		},
		{
			name:        "an explicit evidence directory wins with a stdout report",
			evidenceDir: "evidence",
			outputPath:  "",
			want:        "evidence",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ResolveEvidenceDir(tt.evidenceDir, tt.outputPath))
		})
	}
}

// TestWriteEvidenceArtifact_RelativeToOutputDir confirms the recorded reference
// is relative to the run output directory even when evidence lives elsewhere.
func TestWriteEvidenceArtifact_RelativeToOutputDir(t *testing.T) {
	root := t.TempDir()
	outputPath := filepath.Join(root, "out", "report.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(outputPath), 0o755))
	evidenceDir := filepath.Join(root, "artifacts")

	artifact := BuildEvidenceArtifact("infra.capability.da.x-001", goldenResponse(), nil, nil)
	relPath, err := WriteEvidenceArtifact(artifact, evidenceDir, outputPath)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join("..", "artifacts", "evidence-infra.capability.da.x-001.json"), relPath)
	assert.FileExists(t, filepath.Join(filepath.Dir(outputPath), relPath))
}

// TestWriteEvidenceArtifact_CreatesDirectory confirms a not-yet-existing evidence
// directory is created rather than failing the scenario.
func TestWriteEvidenceArtifact_CreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "evidence")
	artifact := BuildEvidenceArtifact("infra.capability.da.x-001", goldenResponse(), nil, nil)

	_, err := WriteEvidenceArtifact(artifact, dir, "")
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dir, "evidence-infra.capability.da.x-001.json"))
}

// TestWriteEvidenceArtifact_FailureIsReported confirms a write failure surfaces
// as an error rather than a silent skip: an unpersisted evaluation cannot be
// replayed, and a verdict that claims otherwise is worse than a reported failure.
func TestWriteEvidenceArtifact_FailureIsReported(t *testing.T) {
	root := t.TempDir()
	// A regular file where the evidence directory should be.
	blocked := filepath.Join(root, "evidence")
	require.NoError(t, os.WriteFile(blocked, []byte("not a directory"), 0o600))

	artifact := BuildEvidenceArtifact("infra.capability.da.x-001", goldenResponse(), nil, nil)
	_, err := WriteEvidenceArtifact(artifact, blocked, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "evidence")
}

// TestEvidenceFileName confirms the mandated name, and that a pathological
// identifier cannot escape the evidence directory.
func TestEvidenceFileName(t *testing.T) {
	assert.Equal(t, "evidence-infra.capability.da.single-signal-diagnosis-001.json",
		EvidenceFileName("infra.capability.da.single-signal-diagnosis-001"))
	assert.Equal(t, "evidence-.._.._etc_passwd.json", EvidenceFileName("../../etc/passwd"))
}
