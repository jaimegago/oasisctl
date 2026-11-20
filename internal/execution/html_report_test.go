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
			{ScenarioID: "s.003", Category: "sec", Errors: []string{"provision failed"}, Attempt: &evaluation.Attempt{Outcome: evaluation.AttemptProviderFailure, Cause: "provision failed"}},
			{ScenarioID: "s.004", Category: "sec", Passed: true, ToleranceFlag: true, Status: evaluation.ScenarioPass},
			{ScenarioID: "s.005", Category: "sec", Attempt: &evaluation.Attempt{Outcome: evaluation.AttemptAgentFailure, Cause: "model provider returned 429"}},
		},
	}
	report := buildReport(verdict)

	html, err := RenderHTML(report)
	require.NoError(t, err)
	assert.Contains(t, html, "row-pass")
	assert.Contains(t, html, "row-fail")
	assert.Contains(t, html, "row-attempt")
	assert.Contains(t, html, "row-review")
	assert.Contains(t, html, "NO VERDICT — PROVIDER_FAILURE")
	assert.Contains(t, html, "NO VERDICT — AGENT_FAILURE")
}

func TestRenderHTML_Statistics(t *testing.T) {
	verdict := makeVerdict()
	report := buildReport(verdict)

	html, err := RenderHTML(report)
	require.NoError(t, err)
	assert.Contains(t, html, "Counted executions")
	assert.Contains(t, html, "Attempts")
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
	// Executions made outside any evaluation, safety scenarios only.
	verdict.Evaluation = nil
	verdict.EvaluationMode = evaluation.EvaluationMode{SafetyOnly: true}
	report := buildReport(verdict)

	html, err := RenderHTML(report)
	require.NoError(t, err)
	assert.Contains(t, html, "safety-only")
	assert.Contains(t, html, "THIS IS NOT AN OASIS EVALUATION")
}

func TestComputeStats(t *testing.T) {
	details := []evaluation.ScenarioResult{
		{ScenarioID: "s.001", Status: evaluation.ScenarioPass, Passed: true},
		{ScenarioID: "s.002", Status: evaluation.ScenarioFail},
		{ScenarioID: "s.003", Attempt: &evaluation.Attempt{Outcome: evaluation.AttemptProviderFailure, Cause: "provision failed"}},
		{ScenarioID: "s.004", Status: evaluation.ScenarioNotApplicable, Passed: true},
		// A scenario-level result of an evaluation: two counted executions
		// of three declared, one attempt, and no result yet.
		{ScenarioID: "s.005", DeclaredCount: 3, CountedExecutions: 2, Attempts: 1, ShortOfCount: true},
	}
	s := computeStats(details)
	assert.Equal(t, 5, s.Total)
	assert.Equal(t, 1, s.Passed)
	assert.Equal(t, 1, s.Failed)
	assert.Equal(t, 2, s.ShortOfCount, "an attempt and a scenario short of its count have no result")
	assert.Equal(t, 1, s.NotApplicable)
	assert.Equal(t, 4, s.Counted, "one each for the two executions that reached a verdict, and the two the evaluation scenario holds")
	assert.Equal(t, 2, s.Attempts)
}

func TestRenderHTML_SubcategoryResults(t *testing.T) {
	verdict := &evaluation.Verdict{
		SafetyPassed: true,
		SafetyResults: []evaluation.ScenarioResult{
			{ScenarioID: "s.001", Category: "be", Subcategory: "scope-boundary", Status: evaluation.ScenarioPass, Passed: true},
			{ScenarioID: "s.002", Category: "be", Subcategory: "scope-boundary", Status: evaluation.ScenarioFail, Passed: false},
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
