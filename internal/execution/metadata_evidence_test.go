package execution

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/agent"
	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// TestEvidenceArtifact_NonScoringMetadataEndToEnd drives an adapter-shaped body
// through the HTTP decode and into the artifact on disk, and reads the file
// back: a reported zero cache count and an unreported one must still be
// different values there (joe-pm threads/cost-latency-metadata-wire.md,
// invariant 2). The body is the one the joe adapter test serializes.
func TestEvidenceArtifact_NonScoringMetadataEndToEnd(t *testing.T) {
	body := `{"actions":[],"reasoning":"r","final_answer":"f","token_usage":{"input_tokens":900,"output_tokens":40,"cache_read_tokens":0},"agent_loop_duration_ms":5123}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	resp, err := agent.NewHTTPClient(server.URL, "").Execute(context.Background(), evaluation.AgentRequest{Prompt: "p"})
	require.NoError(t, err)

	dir := t.TempDir()
	artifact := BuildEvidenceArtifact("infra.capability.da.single-signal-diagnosis-001", resp, nil, nil)
	relPath, err := WriteEvidenceArtifact(artifact, dir, filepath.Join(dir, "report.yaml"))
	require.NoError(t, err)

	raw, err := os.ReadFile(filepath.Join(dir, relPath))
	require.NoError(t, err)
	var loose map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &loose))
	require.Contains(t, loose, "non_scoring_metadata")
	assert.JSONEq(t,
		`{"tokens":{"input_tokens":900,"output_tokens":40,"cache_read_tokens":0,"cache_write_tokens":null},"agent_loop_duration_ms":5123}`,
		string(loose["non_scoring_metadata"]))

	// Read back through the artifact type, the metadata re-serializes unchanged.
	var got EvidenceArtifact
	require.NoError(t, json.Unmarshal(raw, &got))
	again, err := json.Marshal(got.NonScoringMetadata)
	require.NoError(t, err)
	assert.JSONEq(t, string(loose["non_scoring_metadata"]), string(again))
}

// An agent that reports no metadata leaves an explicit null, on the
// observed_model precedent, never an absent key or a zero-filled object.
func TestEvidenceArtifact_NoMetadataIsNull(t *testing.T) {
	dir := t.TempDir()
	artifact := BuildEvidenceArtifact("infra.capability.da.single-signal-diagnosis-001", goldenResponse(), nil, nil)
	relPath, err := WriteEvidenceArtifact(artifact, dir, filepath.Join(dir, "report.yaml"))
	require.NoError(t, err)

	raw, err := os.ReadFile(filepath.Join(dir, relPath))
	require.NoError(t, err)
	var loose map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &loose))
	require.Contains(t, loose, "non_scoring_metadata")
	assert.Equal(t, "null", string(loose["non_scoring_metadata"]))
}
