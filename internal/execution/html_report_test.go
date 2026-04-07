package execution

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

func TestRenderHTML_ProducesValidHTML(t *testing.T) {
	verdict := makeVerdict()
	report := buildReport(verdict)

	html, err := RenderHTML(report)
	require.NoError(t, err)
	assert.Contains(t, html, "<!DOCTYPE html>")
	assert.Contains(t, html, "</html>")
}

func TestRenderHTML_ContainsSafetyVerdict(t *testing.T) {
	verdict := makeVerdict()
	verdict.SafetyPassed = true
	report := buildReport(verdict)

	html, err := RenderHTML(report)
	require.NoError(t, err)
	assert.Contains(t, html, "Safety Gate: PASS")
	assert.Contains(t, html, "banner-pass")
}

func TestRenderHTML_SafetyFailed(t *testing.T) {
	verdict := makeVerdict()
	verdict.Safety = evaluation.SafetyVerdictFail
	verdict.SafetyPassed = false
	report := buildReport(verdict)

	html, err := RenderHTML(report)
	require.NoError(t, err)
	assert.Contains(t, html, "Safety Gate: FAIL")
	assert.Contains(t, html, "banner-fail")
}

func TestRenderHTML_ScenarioRowClasses(t *testing.T) {
	verdict := &evaluation.Verdict{
		Safety:       evaluation.SafetyVerdictFail,
		SafetyPassed: false,
		SafetyResults: []evaluation.ScenarioResult{
			{ScenarioID: "s.001", Category: "sec", Passed: true, Status: evaluation.ScenarioPass},
			{ScenarioID: "s.002", Category: "sec", Passed: false, Status: evaluation.ScenarioFail},
			{ScenarioID: "s.003", Category: "sec", Passed: false, Errors: []string{"provision failed"}},
			{ScenarioID: "s.004", Category: "sec", Passed: true, ToleranceFlag: true, Status: evaluation.ScenarioPass},
			{ScenarioID: "s.005", Category: "sec", Status: evaluation.ScenarioProviderFailure},
		},
	}
	report := buildReport(verdict)

	html, err := RenderHTML(report)
	require.NoError(t, err)
	assert.Contains(t, html, "row-pass")
	assert.Contains(t, html, "row-fail")
	assert.Contains(t, html, "row-error")
	assert.Contains(t, html, "row-review")
	assert.Contains(t, html, "row-provider-failure")
}

func TestRenderHTML_Statistics(t *testing.T) {
	verdict := makeVerdict()
	report := buildReport(verdict)

	html, err := RenderHTML(report)
	require.NoError(t, err)
	assert.Contains(t, html, "Total Scenarios")
	assert.Contains(t, html, "Passed")
	assert.Contains(t, html, "Failed")
}

func TestWriteHTML_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.html")

	writer := NewReportWriter()
	verdict := makeVerdict()
	err := writer.Write(context.Background(), verdict, "html", path)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "<!DOCTYPE html>")
	assert.Contains(t, string(data), "OASIS Evaluation Report")
}

func TestWriteHTML_EvaluationNote(t *testing.T) {
	verdict := makeVerdict()
	verdict.EvaluationMode = evaluation.EvaluationMode{SafetyOnly: true}
	report := buildReport(verdict)

	html, err := RenderHTML(report)
	require.NoError(t, err)
	assert.Contains(t, html, "safety-only")
}

func TestComputeStats(t *testing.T) {
	details := []evaluation.ScenarioResult{
		{ScenarioID: "s.001", Passed: true},
		{ScenarioID: "s.002", Passed: false},
		{ScenarioID: "s.003", Passed: false, Errors: []string{"provision failed"}},
		{ScenarioID: "s.004", Status: evaluation.ScenarioProviderFailure},
	}
	s := computeStats(details)
	assert.Equal(t, 4, s.Total)
	assert.Equal(t, 2, s.Provisioned)
	assert.Equal(t, 1, s.ProvisionErrors)
	assert.Equal(t, 1, s.Passed)
	assert.Equal(t, 1, s.Failed)
	assert.Equal(t, 1, s.ProviderFailures)
}

func TestRenderHTML_SubcategoryResults(t *testing.T) {
	verdict := &evaluation.Verdict{
		SafetyPassed: true,
		SafetyResults: []evaluation.ScenarioResult{
			{ScenarioID: "s.001", Category: "be", Subcategory: "scope-boundary", Passed: true},
			{ScenarioID: "s.002", Category: "be", Subcategory: "scope-boundary", Passed: false},
		},
	}
	report := buildReport(verdict)

	html, err := RenderHTML(report)
	require.NoError(t, err)
	assert.Contains(t, html, "scope-boundary")
	assert.Contains(t, html, "1/2 passed")
}

func TestRenderHTML_CapabilitySummary(t *testing.T) {
	verdict := makeVerdict()
	report := buildReport(verdict)

	html, err := RenderHTML(report)
	require.NoError(t, err)
	assert.Contains(t, html, "Capability Summary")
	assert.Contains(t, html, "Tier 2")
	// Score should be rendered as percentage.
	assert.True(t, strings.Contains(html, "80.0%") || strings.Contains(html, "80"),
		"should contain capability scores")
}
