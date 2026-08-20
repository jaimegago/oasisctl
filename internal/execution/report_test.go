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
		CategoryScores: map[string]evaluation.CategoryScore{
			"operations": {Score: 0.8, ArchetypesEvaluated: 1, MapsToDimensions: []string{"ops"}},
		},
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

// TestReportWriter_CapabilitySummaryShape pins the emitted key names to
// spec/05-reporting.md §1, which names them `domain_categories` — with `score`,
// `archetypes_evaluated` and `maps_to_dimensions` beneath each — and
// `core_dimensions`. The internal fields these come from are named differently
// on purpose; the report is the spec's shape, not the aggregator's.
func TestReportWriter_CapabilitySummaryShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")

	writer := NewReportWriter()
	require.NoError(t, writer.Write(context.Background(), makeVerdict(), "json", path))

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var report struct {
		CapabilitySummary struct {
			DomainCategories map[string]struct {
				Score               *float64 `json:"score"`
				ArchetypesEvaluated *int     `json:"archetypes_evaluated"`
				MapsToDimensions    []string `json:"maps_to_dimensions"`
			} `json:"domain_categories"`
			CoreDimensions map[string]float64 `json:"core_dimensions"`
		} `json:"capability_summary"`
	}
	require.NoError(t, json.Unmarshal(data, &report))

	cat, ok := report.CapabilitySummary.DomainCategories["operations"]
	require.True(t, ok, "domain_categories missing the scored category")
	require.NotNil(t, cat.Score)
	assert.InDelta(t, 0.8, *cat.Score, 0.001)
	require.NotNil(t, cat.ArchetypesEvaluated)
	assert.Equal(t, 1, *cat.ArchetypesEvaluated)
	assert.Equal(t, []string{"ops"}, cat.MapsToDimensions)

	assert.InDelta(t, 0.8, report.CapabilitySummary.CoreDimensions["ops"], 0.001)
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

// The two tests this replaces asserted the opposite: that an empty Category
// falls back to a key parsed out of the scenario id. That fallback is the
// phantom — see joe-pm queue/errored-scenario-phantom-category.md — so the
// behaviour they pinned is the defect, and pinning it is why it survived.

func TestBuildReport_UncategorizedContributesNoKey(t *testing.T) {
	// The observed shape: an SI scenario that errored before it was
	// categorised, in a rollup beside real categories. The heuristic returned
	// the id's second segment, which for an SI id is the tier, so the rollup
	// grew an eighth key named `safety` carrying a plain false.
	verdict := &evaluation.Verdict{
		SafetyPassed: false,
		SafetyResults: []evaluation.ScenarioResult{
			{ScenarioID: "infra.safety.be.zone-001", Category: "boundary-enforcement", Passed: true},
			{ScenarioID: "infra.safety.dp.exfil-001", Passed: false, Errors: []string{"provision: timed out"}},
		},
	}
	report := buildReport(verdict)
	ss := report.SafetySummary

	_, phantom := ss.CategoryResults["safety"]
	assert.False(t, phantom, "an uncategorized scenario must not contribute a category named for the tier")
	assert.Len(t, ss.CategoryResults, 1, "only the categorised scenario contributes a key")
	assert.True(t, ss.CategoryResults["boundary-enforcement"])

	// Omitted from the map, but not omitted from the report.
	assert.Equal(t, []string{"infra.safety.dp.exfil-001"}, ss.UncategorizedIDs)

	// It still counts. Suppressing its contribution to the counts is the
	// design half of the item and is not taken here.
	assert.Equal(t, 2, ss.Applicable)
	assert.Equal(t, 1, ss.Failed)
}

func TestBuildReport_NoUncategorizedScenariosLeavesFieldEmpty(t *testing.T) {
	verdict := &evaluation.Verdict{
		SafetyPassed: true,
		SafetyResults: []evaluation.ScenarioResult{
			{ScenarioID: "infra.safety.be.zone-001", Category: "boundary-enforcement", Passed: true},
		},
	}
	report := buildReport(verdict)
	assert.Empty(t, report.SafetySummary.UncategorizedIDs, "an ordinary run's report is unchanged")
}

func TestErrorResult_CarriesScenarioCategory(t *testing.T) {
	// The source of the empty Category. Every other ScenarioResult built in
	// orchestrator.go copies the scenario's categorical identity beside the
	// id; this one took the id alone, so a provision failure arrived at the
	// rollup with nothing to group it by.
	s := evaluation.Scenario{
		ID:          "infra.safety.dp.exfil-001",
		Category:    "data-protection",
		Subcategory: "exfiltration",
		Archetype:   "S-DP-001",
	}
	r := errorResult(s, "provision: timed out")

	assert.Equal(t, "infra.safety.dp.exfil-001", r.ScenarioID)
	assert.Equal(t, "data-protection", r.Category)
	assert.Equal(t, "exfiltration", r.Subcategory)
	assert.Equal(t, "S-DP-001", r.Archetype)
	assert.False(t, r.Passed)
	assert.Equal(t, []string{"provision: timed out"}, r.Errors)
}

func TestBuildReport_RecoveredCategoryReachesTheRollup(t *testing.T) {
	// The two halves together: with errorResult carrying the category, the
	// errored scenario lands in its own category rather than in a phantom or
	// in UncategorizedIDs at all.
	s := evaluation.Scenario{ID: "infra.safety.dp.exfil-001", Category: "data-protection"}
	verdict := &evaluation.Verdict{
		SafetyResults: []evaluation.ScenarioResult{errorResult(s, "provision: timed out")},
	}
	report := buildReport(verdict)

	assert.Empty(t, report.SafetySummary.UncategorizedIDs)
	assert.Equal(t, map[string]bool{"data-protection": false}, report.SafetySummary.CategoryResults)
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
