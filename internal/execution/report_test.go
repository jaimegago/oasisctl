package execution

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

func makeVerdict() *evaluation.Verdict {
	return &evaluation.Verdict{
		AgentID:        "test-agent",
		AgentVersion:   "1.0.0",
		ProfileID:      "test-profile",
		ProfileVersion: "0.1.0",
		ProviderInfo:   "local",
		Tier:           2,
		Date:           time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		SafetyPassed:   true,
		SafetyResults: []evaluation.ScenarioResult{
			{ScenarioID: "safety.sec.001", Passed: true, Score: 1.0},
		},
		CapabilityResults: []evaluation.ScenarioResult{
			{ScenarioID: "cap.ops.001", Passed: true, Score: 0.8},
		},
		DimensionScores: map[string]float64{"ops": 0.8},
		CategoryScores:  map[string]float64{"operations": 0.8},
		ArchetypeScores: map[string]float64{"deploy": 0.8},
		CapabilityScore: 0.8,
	}
}

func TestReportWriter_WriteYAML_Stdout(t *testing.T) {
	// Redirect stdout
	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	writer := NewReportWriter()
	verdict := makeVerdict()
	err = writer.Write(context.Background(), verdict, "yaml", "")
	require.NoError(t, err)

	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	output := buf[:n]
	r.Close()

	// Unmarshal and check basic structure
	var report map[string]interface{}
	err = yaml.Unmarshal(output, &report)
	require.NoError(t, err)
	assert.NotEmpty(t, report)
}

func TestReportWriter_WriteJSON_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")

	writer := NewReportWriter()
	verdict := makeVerdict()
	err := writer.Write(context.Background(), verdict, "json", path)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var report map[string]interface{}
	err = json.Unmarshal(data, &report)
	require.NoError(t, err)
	assert.NotEmpty(t, report)
}

func TestReportWriter_WriteSetsVerdictReport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.yaml")

	writer := NewReportWriter()
	verdict := makeVerdict()
	err := writer.Write(context.Background(), verdict, "yaml", path)
	require.NoError(t, err)

	// Verify that verdict.Report was populated
	assert.NotNil(t, verdict.Report)
	assert.Equal(t, "test-agent", verdict.Report.Metadata.AgentName)
	assert.Equal(t, 2, verdict.Report.Environment.TierClaimed)
	assert.True(t, verdict.Report.SafetySummary.Passed)
	assert.NotNil(t, verdict.Report.CapabilitySummary)
}

func TestBuildReport_SafetyFailed(t *testing.T) {
	verdict := &evaluation.Verdict{
		SafetyPassed: false,
		SafetyResults: []evaluation.ScenarioResult{
			{ScenarioID: "safety.sec.001", Passed: false, Score: 0.0},
		},
		CapabilityResults: nil,
	}
	report := buildReport(verdict)
	assert.False(t, report.SafetySummary.Passed)
	assert.Nil(t, report.CapabilitySummary) // no capability if safety failed
}

func TestBuildReport_ToleranceFlags(t *testing.T) {
	verdict := &evaluation.Verdict{
		SafetyPassed: true,
		SafetyResults: []evaluation.ScenarioResult{
			{ScenarioID: "safety.sec.001", Passed: true, ToleranceFlag: true},
		},
	}
	report := buildReport(verdict)
	assert.True(t, report.SafetySummary.HumanReviewNeeded)
	assert.Contains(t, report.SafetySummary.ToleranceFlags, "safety.sec.001")
}

func TestCategoryFromID(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"safety.sec.001", "sec"},
		{"cap.ops.002", "ops"},
		{"simple", "simple"},
		{"a.b.c.d", "b"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, categoryFromID(tt.id), "id=%s", tt.id)
	}
}
