package execution

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

func conformanceWithEvidence(ev *evaluation.TierEvidence) *evaluation.ConformanceResponse {
	return &evaluation.ConformanceResponse{
		Provider:        "mock-provider",
		ProviderVersion: "1.0.0",
		Supported:       true,
		Requirements: evaluation.ConformanceRequirements{
			EnvironmentType:         "kubernetes-cluster",
			ComplexityTierSupported: 3,
		},
		TierEvidence: ev,
	}
}

func TestVerifyTierEvidence(t *testing.T) {
	met := evaluation.TierCheck{Requirement: "nodes", Required: "at least 3 ready nodes", Observed: "3 ready nodes", Status: evaluation.TierCheckMet}
	unmet := evaluation.TierCheck{Requirement: "certificate-management", Required: "a certificate controller running", Observed: "0 of 3 deployments available", Status: evaluation.TierCheckUnmet}
	notAssessed := evaluation.TierCheck{Requirement: "namespaces", Required: "at most 3 namespaces", Observed: "4 namespaces", Status: evaluation.TierCheckNotAssessed}

	evidence := func(tier int, met bool, checks ...evaluation.TierCheck) *evaluation.ConformanceResponse {
		return conformanceWithEvidence(&evaluation.TierEvidence{Tier: tier, Met: met, Checks: checks})
	}

	tests := []struct {
		name     string
		resp     *evaluation.ConformanceResponse
		claimed  int
		wantErr  []string // every substring the error must carry; empty means no error
		wantTier int
	}{
		{name: "met at the claimed tier", resp: evidence(1, true, met), claimed: 1, wantTier: 1},
		{name: "verified against a higher tier than claimed", resp: evidence(2, true, met), claimed: 1, wantTier: 2},
		{name: "a check reported and not assessed does not fail it", resp: evidence(1, true, met, notAssessed), claimed: 1, wantTier: 1},
		{
			name:    "no evidence at all",
			resp:    conformanceWithEvidence(nil),
			claimed: 1,
			wantErr: []string{`provider "mock-provider" returned no tier evidence`, "claimed tier 1"},
		},
		{name: "evidence with no checks", resp: evidence(1, true), claimed: 1, wantErr: []string{"returned no tier evidence"}},
		{name: "nil response", resp: nil, claimed: 1, wantErr: []string{"returned no tier evidence"}},
		{
			name: "a met check that observed nothing is not evidence",
			resp: evidence(1, true, evaluation.TierCheck{
				Requirement: "nodes", Required: "at least 3 ready nodes", Status: evaluation.TierCheckMet,
			}),
			claimed: 1,
			wantErr: []string{`requirement "nodes" met`, "carries no observed value"},
		},
		{
			name:    "a check that names no requirement",
			resp:    evidence(1, true, met, evaluation.TierCheck{Observed: "3", Status: evaluation.TierCheckMet}),
			claimed: 1,
			wantErr: []string{"check 2 names no requirement"},
		},
		{
			name:    "a status outside the vocabulary",
			resp:    evidence(1, true, met, evaluation.TierCheck{Requirement: "ingress", Observed: "1/1", Status: "probably"}),
			claimed: 1,
			wantErr: []string{`check "ingress" has status "probably"`, "met, unmet or not_assessed"},
		},
		{
			name:    "a check with no status",
			resp:    evidence(1, true, evaluation.TierCheck{Requirement: "ingress", Observed: "1/1"}),
			claimed: 1,
			wantErr: []string{`check "ingress" has status ""`},
		},
		{
			name:    "verified against a lower tier than claimed",
			resp:    evidence(1, true, met),
			claimed: 2,
			wantErr: []string{"against tier 1", "claims tier 2"},
		},
		{
			name:    "an unmet requirement is named with what was required and observed",
			resp:    evidence(1, false, met, unmet),
			claimed: 1,
			wantErr: []string{
				"does not meet claimed tier 1",
				"certificate-management: observed 0 of 3 deployments available (required: a certificate controller running)",
			},
		},
		{
			name:    "an unmet check wins over a provider that says met",
			resp:    evidence(1, true, met, unmet),
			claimed: 1,
			wantErr: []string{"does not meet claimed tier 1", "certificate-management"},
		},
		{
			name:    "a provider that says unmet and names nothing",
			resp:    evidence(1, false, met),
			claimed: 1,
			wantErr: []string{"does not meet tier 1", "names no unmet requirement"},
		},
		{
			name:    "evidence in which nothing was assessed",
			resp:    evidence(1, true, notAssessed),
			claimed: 1,
			wantErr: []string{"no requirement is met"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := VerifyTierEvidence(tt.resp, tt.claimed)
			if len(tt.wantErr) == 0 {
				require.NoError(t, err)
				require.NotNil(t, ev)
				assert.Equal(t, tt.wantTier, ev.Tier)
				return
			}
			require.Error(t, err)
			assert.Nil(t, ev)
			for _, want := range tt.wantErr {
				assert.Contains(t, err.Error(), want)
			}
		})
	}

	// The met requirement is not named in an abort: the error is the gap.
	_, err := VerifyTierEvidence(evidence(1, false, met, unmet), 1)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "ready nodes")
}

// The wire shape is the provider's. This is the JSON a petri conformance
// response carries, decoded the way the HTTP client decodes it.
func TestTierEvidence_DecodesTheProviderWireShape(t *testing.T) {
	body := `{
	  "provider": "petri", "provider_version": "0.9.0", "supported": true,
	  "requirements": {"environment_type": "kubernetes-cluster", "complexity_tier_supported": 1},
	  "tier_evidence": {
	    "tier": 1, "met": true, "observed_at": "2026-11-18T10:00:00Z",
	    "checks": [
	      {"requirement": "nodes", "required": "at least 3 ready nodes", "observed": "3 ready nodes", "status": "met"},
	      {"requirement": "namespaces", "required": "at most 3 namespaces", "observed": "4 namespaces", "status": "not_assessed"}
	    ]
	  }
	}`
	var resp evaluation.ConformanceResponse
	require.NoError(t, json.Unmarshal([]byte(body), &resp))

	ev, err := VerifyTierEvidence(&resp, 1)
	require.NoError(t, err)
	require.Len(t, ev.Checks, 2)
	assert.Equal(t, evaluation.TierCheckMet, ev.Checks[0].Status)
	assert.Equal(t, evaluation.TierCheckNotAssessed, ev.Checks[1].Status)
	require.NotNil(t, ev.ObservedAt)

	assert.Equal(t,
		"petri 0.9.0 verified the environment against tier 1 at 2026-11-18T10:00:00Z: nodes: 3 ready nodes (required: at least 3 ready nodes). "+
			"Reported and not assessed: namespaces: 4 namespaces (required: at most 3 namespaces)",
		evaluation.TierEvidenceSummary(resp.Provider, resp.ProviderVersion, ev))
}

// runWithConformance runs one safety and one capability scenario against a
// provider answering the preflight with resp.
func runWithConformance(t *testing.T, resp *evaluation.ConformanceResponse) (*evaluation.Verdict, *mockProvider, *mockReportWriter, error) {
	t.Helper()
	prov := &mockProvider{provisionResp: defaultProvision(), conformanceResp: resp}
	reporter := &mockReportWriter{}
	orch := NewOrchestrator(
		&mockProfileLoader{profile: defaultProfile()},
		&mockAgentClient{response: defaultAgentResp()},
		prov, &mockAsserter{}, &mockScorer{}, reporter, nil,
		Config{EvidenceDir: t.TempDir(), Tier: 1},
	)
	verdict, err := orch.Run(context.Background(), "/profile",
		[]evaluation.Scenario{safetyScenario("s.001", 1), capabilityScenario("c.001", 1)},
		"agent", "provider", "yaml", "")
	return verdict, prov, reporter, err
}

// A provider that returns no tier evidence aborts the run before any scenario:
// nothing is provisioned and no report is written.
func TestOrchestrator_AbortsWithoutTierEvidence(t *testing.T) {
	verdict, prov, reporter, err := runWithConformance(t, conformanceWithEvidence(nil))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "returned no tier evidence")
	assert.Nil(t, verdict)
	assert.Empty(t, prov.provisionRequests, "no scenario may be provisioned")
	assert.False(t, reporter.called, "no verdict file is produced")
}

// An environment that does not meet the claimed tier aborts the same way, and
// the error names the requirement, what was required and what was observed.
func TestOrchestrator_AbortsWhenEnvironmentDoesNotMeetClaimedTier(t *testing.T) {
	verdict, prov, reporter, err := runWithConformance(t, conformanceWithEvidence(&evaluation.TierEvidence{
		Tier: 1,
		Checks: []evaluation.TierCheck{
			{Requirement: "nodes", Required: "at least 3 ready nodes", Observed: "1 ready node", Status: evaluation.TierCheckUnmet},
		},
	}))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not meet claimed tier 1")
	assert.Contains(t, err.Error(), "nodes: observed 1 ready node (required: at least 3 ready nodes)")
	assert.Nil(t, verdict)
	assert.Empty(t, prov.provisionRequests)
	assert.False(t, reporter.called)
}

// A run that started carries the provider's evidence, verbatim and as a
// summary line that is never empty.
func TestOrchestrator_VerdictCarriesTierEvidence(t *testing.T) {
	ev := &evaluation.TierEvidence{Tier: 1, Met: true, Checks: []evaluation.TierCheck{
		{Requirement: "nodes", Required: "at least 3 ready nodes", Observed: "3 ready nodes", Status: evaluation.TierCheckMet},
		{Requirement: "ingress", Required: "an ingress controller running", Observed: "ingress-nginx-controller 1/1", Status: evaluation.TierCheckMet},
	}}
	verdict, _, _, err := runWithConformance(t, conformanceWithEvidence(ev))
	require.NoError(t, err)

	report := buildReport(verdict)
	assert.Equal(t, 1, report.Environment.TierClaimed)
	assert.Equal(t, ev, report.Environment.TierEvidence)
	assert.Equal(t,
		"mock-provider 1.0.0 verified the environment against tier 1: nodes: 3 ready nodes (required: at least 3 ready nodes); "+
			"ingress: ingress-nginx-controller 1/1 (required: an ingress controller running)",
		report.Environment.Evidence)
}

// coverageProfile declares one safety and one capability category, one
// archetype each, and a tier 1 minimum of one archetype per category.
func coverageProfile() *evaluation.Profile {
	p := defaultProfile()
	p.SafetyCategories = []evaluation.Category{{ID: "boundary", Archetypes: []string{"S-1"}}}
	p.CapabilityCategories = []evaluation.Category{{ID: "diagnosis", Archetypes: []string{"C-1"}}}
	p.TierRequirements = map[int]evaluation.TierRequirement{
		1: {
			Tier:       1,
			Safety:     evaluation.CoverageMinimum{Declared: true, Archetypes: 1},
			Capability: evaluation.CoverageMinimum{Declared: true, Archetypes: 1},
		},
	}
	return p
}

func coverageScenarios() []evaluation.Scenario {
	s := safetyScenarioWithCategorySub("s.001", 1, "boundary", "")
	s.Archetype = "S-1"
	c := capabilityScenarioWithCategory("c.001", 1, "diagnosis")
	c.Archetype = "C-1"
	return []evaluation.Scenario{s, c}
}

func runCoverageProfile(t *testing.T, scorer *mockScorer, cfg Config) *evaluation.Verdict {
	t.Helper()
	cfg.EvidenceDir = t.TempDir()
	cfg.Tier = 1
	orch := NewOrchestrator(
		&mockProfileLoader{profile: coverageProfile()},
		&mockAgentClient{response: defaultAgentResp()},
		&mockProvider{provisionResp: defaultProvision()},
		&mockAsserter{}, scorer, &mockReportWriter{}, nil, cfg,
	)
	verdict, err := orch.Run(context.Background(), "/profile", coverageScenarios(), "agent", "provider", "yaml", "")
	require.NoError(t, err)
	require.NotNil(t, verdict)
	return verdict
}

// An unfiltered run that ran both phases and met the tier's minimum is
// complete, and its report carries the populated coverage matrix.
func TestOrchestrator_CompleteWhenBothPhasesRanAndCoverageMet(t *testing.T) {
	verdict := runCoverageProfile(t, &mockScorer{}, Config{})

	assert.True(t, verdict.EvaluationMode.Complete)
	assert.Empty(t, verdict.EvaluationMode.IncompleteReasons)

	cm := buildReport(verdict).CoverageMatrix
	require.NotNil(t, cm)
	assert.True(t, cm.MinimumCoverageMet)
	require.Len(t, cm.Categories, 2)
	assert.Equal(t, []string{"S-1"}, cm.Categories[0].ArchetypesEvaluated)
	assert.Equal(t, []string{"C-1"}, cm.Categories[1].ArchetypesEvaluated)
	assert.Equal(t, evaluation.CoverageMet, cm.Categories[0].Status)
	assert.Equal(t, evaluation.CoverageMet, cm.Categories[1].Status)
}

// The defect: a whole-profile run stopped at the safety gate passed no filter
// and reported complete: true over a capability phase that never started.
func TestOrchestrator_NotCompleteWhenCapabilityPhaseNeverRan(t *testing.T) {
	verdict := runCoverageProfile(t,
		&mockScorer{safetyResult: &evaluation.ScenarioResult{Passed: false}}, Config{})

	require.False(t, verdict.SafetyPassed)
	require.Empty(t, verdict.CapabilityResults)

	assert.False(t, verdict.EvaluationMode.Complete)
	assert.Equal(t, []string{
		"the capability phase did not run: the safety verdict is FAIL",
		"minimum coverage for tier 1 was not met",
	}, verdict.EvaluationMode.IncompleteReasons)

	report := buildReport(verdict)
	require.NotNil(t, report.CoverageMatrix)
	assert.False(t, report.CoverageMatrix.MinimumCoverageMet)
	assert.Equal(t, evaluation.CoverageMet, report.CoverageMatrix.Categories[0].Status,
		"the safety archetype was evaluated, to a FAIL")
	assert.Equal(t, evaluation.CoverageUnmet, report.CoverageMatrix.Categories[1].Status)
	assert.Equal(t, evaluation.SkipNotRun, report.CoverageMatrix.Categories[1].ArchetypesSkipped[0].Reason)
	assert.Contains(t, report.Metadata.EvaluationNote,
		"Evaluation incomplete: the capability phase did not run: the safety verdict is FAIL; minimum coverage for tier 1 was not met. This is not a complete OASIS assessment.")
}

// A safety-only run is incomplete for the reason it always was, and now also
// says that minimum coverage was not met.
func TestOrchestrator_SafetyOnlyIsNotCompleteAndSaysWhy(t *testing.T) {
	verdict := runCoverageProfile(t, &mockScorer{}, Config{SafetyOnly: true})

	assert.False(t, verdict.EvaluationMode.Complete)
	assert.Equal(t, []string{
		"safety-only run: capability scenarios were not executed",
		"minimum coverage for tier 1 was not met",
	}, verdict.EvaluationMode.IncompleteReasons)
	assert.Contains(t, buildReport(verdict).Metadata.EvaluationNote,
		"Evaluation mode: safety-only. Capability scenarios were not executed.")
}

// A profile that states no minimum for the claimed tier cannot have its
// coverage established, and a run against it is not complete.
func TestOrchestrator_NotCompleteWhenProfileStatesNoMinimum(t *testing.T) {
	p := coverageProfile()
	p.TierRequirements = nil
	orch := NewOrchestrator(
		&mockProfileLoader{profile: p},
		&mockAgentClient{response: defaultAgentResp()},
		&mockProvider{provisionResp: defaultProvision()},
		&mockAsserter{}, &mockScorer{}, &mockReportWriter{}, nil,
		Config{EvidenceDir: t.TempDir(), Tier: 1},
	)
	verdict, err := orch.Run(context.Background(), "/profile", coverageScenarios(), "agent", "provider", "yaml", "")
	require.NoError(t, err)

	assert.False(t, verdict.EvaluationMode.Complete)
	require.NotNil(t, verdict.CoverageMatrix)
	assert.False(t, verdict.CoverageMatrix.MinimumCoverageMet)
	require.Len(t, verdict.CoverageMatrix.Unmet, 1)
	assert.Contains(t, verdict.CoverageMatrix.Unmet[0], "states no minimum coverage for tier 1")
}

// reportedCoverage is a matrix with one category in each standing.
func reportedCoverage() *evaluation.CoverageMatrix {
	return &evaluation.CoverageMatrix{
		Tier:  1,
		Unmet: []string{`capability category "diagnosis": 0 archetype(s) evaluated, tier 1 requires 1`},
		Categories: []evaluation.CategoryCoverage{
			{
				Category: "boundary", Classification: evaluation.ClassificationSafety, Status: evaluation.CoverageMet,
				ArchetypesDeclared: 1, ArchetypesApplicable: 1, ArchetypesRequired: 1, ArchetypesEvaluated: []string{"S-1"},
			},
			{
				Category: "diagnosis", Classification: evaluation.ClassificationCapability, Status: evaluation.CoverageUnmet,
				ArchetypesDeclared: 1, ArchetypesApplicable: 1, ArchetypesRequired: 1, ArchetypesEvaluated: []string{},
				ArchetypesSkipped: []evaluation.SkippedArchetype{{Archetype: "C-1", Reason: evaluation.SkipNotRun, Detail: "no scenario of this archetype was executed in this run"}},
			},
			{
				Category: "execution", Classification: evaluation.ClassificationCapability, Status: evaluation.CoverageExcluded,
				ArchetypesDeclared: 1, ArchetypesEvaluated: []string{},
				ArchetypesSkipped: []evaluation.SkippedArchetype{{Archetype: "C-2", Reason: evaluation.SkipNotApplicable, Detail: "requires operational_mode=read_write"}},
			},
		},
	}
}

// The HTML report states the tier evidence and whether minimum coverage was
// met, and tells an excluded category apart from an unmet one.
func TestRenderHTML_TierEvidenceAndCoverage(t *testing.T) {
	verdict := makeVerdict()
	verdict.Environment.Evidence = "petri 0.9.0 verified the environment against tier 2: nodes: 3 ready nodes (required: at least 3 ready nodes)"
	verdict.CoverageMatrix = reportedCoverage()

	html, err := RenderHTML(buildReport(verdict))
	require.NoError(t, err)

	assert.Contains(t, html, "Tier evidence")
	assert.Contains(t, html, "nodes: 3 ready nodes (required: at least 3 ready nodes)")
	assert.Contains(t, html, "Minimum coverage for tier 1: NOT MET")
	assert.Contains(t, html, "<strong>UNMET</strong> — 0 evaluated, 1 required")
	assert.Contains(t, html, "excluded — no archetype applicable to this configuration")
	assert.Contains(t, html, "C-2 (not_applicable: requires operational_mode=read_write)")
}

// A report written before coverage was recorded renders without the section.
func TestRenderHTML_NoCoverageSectionWithoutAMatrix(t *testing.T) {
	html, err := RenderHTML(buildReport(makeVerdict()))
	require.NoError(t, err)
	assert.NotContains(t, html, "<h2>Coverage</h2>")
	assert.NotContains(t, html, "Tier evidence")
}

// The coverage matrix and the tier evidence survive the written report in
// both serialisations, under the keys the report declares.
func TestReportWriter_SerialisesTierEvidenceAndCoverage(t *testing.T) {
	for _, format := range []string{"yaml", "json"} {
		t.Run(format, func(t *testing.T) {
			verdict := makeVerdict()
			verdict.Environment = evaluation.ReportEnvironment{
				Evidence:     "petri 0.9.0 verified the environment against tier 2: nodes: 3 ready nodes (required: at least 3 ready nodes)",
				TierEvidence: metTierEvidence(2),
			}
			verdict.CoverageMatrix = reportedCoverage()

			path := filepath.Join(t.TempDir(), "verdict."+format)
			require.NoError(t, NewReportWriter().Write(context.Background(), verdict, format, path))
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			out := string(data)

			for _, want := range []string{
				"tier_claimed", "tier_evidence", "at least 3 ready nodes", "status",
				"coverage_matrix", "minimum_coverage_met", "archetypes_evaluated", "archetypes_skipped",
				"not_applicable", "excluded",
			} {
				assert.Contains(t, out, want)
			}
		})
	}
}
