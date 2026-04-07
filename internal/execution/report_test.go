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
		Safety:         evaluation.SafetyVerdictPass,
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

	_ = w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	output := buf[:n]
	_ = r.Close()

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

func TestBuildReport_SubcategoryResults(t *testing.T) {
	verdict := &evaluation.Verdict{
		SafetyPassed: true,
		SafetyResults: []evaluation.ScenarioResult{
			{ScenarioID: "s.001", Category: "boundary-enforcement", Subcategory: "scope-boundary", Passed: true},
			{ScenarioID: "s.002", Category: "boundary-enforcement", Subcategory: "scope-boundary", Passed: true},
			{ScenarioID: "s.003", Category: "data-exfiltration", Subcategory: "data-protection", Passed: false},
		},
	}
	report := buildReport(verdict)

	require.NotNil(t, report.SafetySummary.SubcategoryResults)
	assert.Len(t, report.SafetySummary.SubcategoryResults, 2)

	sb := report.SafetySummary.SubcategoryResults["scope-boundary"]
	assert.Equal(t, 2, sb.Total)
	assert.Equal(t, 2, sb.Passed)
	assert.Equal(t, 0, sb.Failed)

	dp := report.SafetySummary.SubcategoryResults["data-protection"]
	assert.Equal(t, 1, dp.Total)
	assert.Equal(t, 0, dp.Passed)
	assert.Equal(t, 1, dp.Failed)
}

func TestBuildReport_UsesScenarioResultCategory(t *testing.T) {
	verdict := &evaluation.Verdict{
		SafetyPassed: true,
		SafetyResults: []evaluation.ScenarioResult{
			{ScenarioID: "infra.safety.be.zone-001", Category: "boundary-enforcement", Passed: true},
			{ScenarioID: "infra.safety.be.zone-002", Category: "boundary-enforcement", Passed: false},
		},
	}
	report := buildReport(verdict)

	// Should use Category field, not ID-based heuristic.
	_, ok := report.SafetySummary.CategoryResults["boundary-enforcement"]
	assert.True(t, ok, "should group by ScenarioResult.Category")
	assert.False(t, report.SafetySummary.CategoryResults["boundary-enforcement"])
}

func TestBuildReport_FallsBackToIDHeuristic(t *testing.T) {
	// When Category is empty, should fall back to ID parsing.
	verdict := &evaluation.Verdict{
		SafetyPassed: true,
		SafetyResults: []evaluation.ScenarioResult{
			{ScenarioID: "safety.sec.001", Passed: true},
		},
	}
	report := buildReport(verdict)
	_, ok := report.SafetySummary.CategoryResults["sec"]
	assert.True(t, ok, "should fall back to categoryFromID when Category is empty")
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

func TestReportWriter_YAMLFieldNames(t *testing.T) {
	verdict := makeVerdict()
	verdict.SafetyResults = []evaluation.ScenarioResult{
		{
			ScenarioID:    "safety.sec.001",
			Category:      "security",
			Passed:        true,
			ToleranceFlag: true,
			Score:         1.0,
			AssertionResults: []evaluation.AssertionResult{
				{
					Assertion: evaluation.AssertionItem{Behavior: "refuse_with_explanation"},
					Status:    evaluation.AssertionPass,
					Evidence:  "ok",
				},
			},
		},
	}

	report := buildReport(verdict)
	data, err := yaml.Marshal(report)
	require.NoError(t, err)
	output := string(data)

	// Verify snake_case field names from struct tags are used.
	assert.Contains(t, output, "scenario_id:")
	assert.Contains(t, output, "tolerance_flag:")
	assert.Contains(t, output, "safety_summary:")
	assert.Contains(t, output, "scenario_details:")
	assert.Contains(t, output, "agent_name:")
	assert.Contains(t, output, "assertion_results:")

	// Verify old Go-default lowercase field names are NOT used.
	assert.NotContains(t, output, "scenarioid:")
	assert.NotContains(t, output, "toleranceflag:")
}

func TestBuildReport_ProviderFailureExcludedFromSafetySummary(t *testing.T) {
	verdict := &evaluation.Verdict{
		Safety:       evaluation.SafetyVerdictProviderFailure,
		SafetyPassed: false,
		SafetyResults: []evaluation.ScenarioResult{
			{ScenarioID: "s.001", Category: "boundary-enforcement", Status: evaluation.ScenarioPass, Passed: true},
			{ScenarioID: "s.002", Category: "boundary-enforcement", Status: evaluation.ScenarioProviderFailure, Passed: false, Evidence: []string{"infra failure"}},
			{ScenarioID: "s.003", Status: evaluation.ScenarioNotApplicable, Passed: true},
		},
	}
	report := buildReport(verdict)

	ss := report.SafetySummary
	assert.False(t, ss.Passed)
	assert.Equal(t, evaluation.SafetyVerdictProviderFailure, ss.Safety)
	assert.Equal(t, 1, ss.Applicable)       // only s.001 counts
	assert.Equal(t, 1, ss.NotApplicable)    // s.003
	assert.Equal(t, 1, ss.ProviderFailures) // s.002
	// PROVIDER_FAILURE should not appear in category results.
	assert.True(t, ss.CategoryResults["boundary-enforcement"])
	assert.Contains(t, ss.ProviderFailureIDs, "s.002")
}

func TestComputeStats_ProviderFailure(t *testing.T) {
	details := []evaluation.ScenarioResult{
		{ScenarioID: "s.001", Status: evaluation.ScenarioPass, Passed: true},
		{ScenarioID: "s.002", Status: evaluation.ScenarioProviderFailure, Passed: false},
		{ScenarioID: "s.003", Status: evaluation.ScenarioNotApplicable},
		{ScenarioID: "s.004", Status: evaluation.ScenarioFail, Passed: false},
	}
	stats := computeStats(details)
	assert.Equal(t, 4, stats.Total)
	assert.Equal(t, 1, stats.Passed)
	assert.Equal(t, 1, stats.Failed)
	assert.Equal(t, 1, stats.ProviderFailures)
	assert.Equal(t, 1, stats.NotApplicable)
	assert.Equal(t, 0, stats.ProvisionErrors)
}

func TestReportWriter_JSONFieldNames(t *testing.T) {
	verdict := makeVerdict()
	verdict.SafetyResults = []evaluation.ScenarioResult{
		{
			ScenarioID:    "safety.sec.001",
			ToleranceFlag: true,
		},
	}

	report := buildReport(verdict)
	data, err := json.MarshalIndent(report, "", "  ")
	require.NoError(t, err)
	output := string(data)

	assert.Contains(t, output, "\"scenario_id\"")
	assert.Contains(t, output, "\"tolerance_flag\"")
	assert.Contains(t, output, "\"safety_summary\"")
	assert.Contains(t, output, "\"scenario_details\"")
	assert.NotContains(t, output, "\"ScenarioID\"")
}
