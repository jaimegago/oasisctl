package execution

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// supportedClaimInputs is a verdict and report that support a conformance
// claim, for a test to break in one place.
func supportedClaimInputs() (*evaluation.Verdict, *evaluation.Report) {
	whole := evaluation.BuildVerdictAgentIdentity(wholeDeclaration(), []evaluation.ObservedModelRecord{
		{ScenarioID: "s.1", ObservedModel: strp(conformanceModel)},
	})
	evaluator := "Example Lab"
	v := &evaluation.Verdict{Safety: evaluation.SafetyVerdictPass}
	r := &evaluation.Report{}
	r.Metadata.EvaluationMode.Complete = true
	r.Metadata.AgentIdentity = &whole
	r.Metadata.Evaluator = &evaluator
	r.Environment.ScenarioFigures = []evaluation.TierCheck{
		{Requirement: "namespaces", Status: evaluation.TierCheckMet},
		{Requirement: "microservices", Status: evaluation.TierCheckMet},
		{Requirement: "security-zones", Status: evaluation.TierCheckMet},
	}
	return v, r
}

// TestConformanceClaim_ReadsEveryCapabilityResultsRange: a capability result
// is a score from 0.0 to 1.0 (spec/05-reporting.md §1.2). A run carrying one
// outside it wrote a verdict the format forbids, and does not support a claim;
// the reason names the scenario and the value. Run 20261119-234640-ca82d6
// carried -0.25 for C-DA-004 and was reported as supporting one.
func TestConformanceClaim_ReadsEveryCapabilityResultsRange(t *testing.T) {
	inRange := []evaluation.ScenarioResult{
		{ScenarioID: "c.zero", Score: 0},
		{ScenarioID: "c.half", Score: 0.5},
		{ScenarioID: "c.one", Score: 1, Passed: true},
	}

	v, r := supportedClaimInputs()
	v.CapabilityResults = inRange
	claim := conformanceClaim(v, r)
	assert.True(t, claim.Supported, "0, 0.5 and 1 are all in range; reasons: %v", claim.Reasons)

	for name, tt := range map[string]struct {
		score float64
		want  string
	}{
		"below zero":   {-0.25, `the capability result of scenario "c.bad" is -0.25, outside the 0.0 to 1.0 the verdict format allows`},
		"above one":    {1.2, `the capability result of scenario "c.bad" is 1.2, outside the 0.0 to 1.0 the verdict format allows`},
		"not a number": {math.NaN(), `the capability result of scenario "c.bad" is NaN, outside`},
	} {
		t.Run(name, func(t *testing.T) {
			v, r := supportedClaimInputs()
			v.CapabilityResults = append(append([]evaluation.ScenarioResult{}, inRange...), evaluation.ScenarioResult{ScenarioID: "c.bad", Score: tt.score})
			claim := conformanceClaim(v, r)
			assert.False(t, claim.Supported)
			require.Len(t, claim.Reasons, 1, "the range is the only thing wrong with this run")
			assert.Contains(t, claim.Reasons[0], tt.want)
		})
	}

	// Every one is named, in run order.
	v, r = supportedClaimInputs()
	v.CapabilityResults = []evaluation.ScenarioResult{
		{ScenarioID: "c.first", Score: -1},
		{ScenarioID: "c.fine", Score: 0.4},
		{ScenarioID: "c.second", Score: 2},
	}
	claim = conformanceClaim(v, r)
	require.Len(t, claim.Reasons, 2)
	assert.Contains(t, claim.Reasons[0], `"c.first"`)
	assert.Contains(t, claim.Reasons[1], `"c.second"`)

	// A scenario with no result has no range to be outside of: its Score is
	// the zero a float defaults to, or whatever was left there, and it is not
	// read.
	v, r = supportedClaimInputs()
	v.CapabilityResults = []evaluation.ScenarioResult{
		{ScenarioID: "c.na", Status: evaluation.ScenarioNotApplicable, Score: -5, Passed: true},
		{ScenarioID: "c.unevaluable", Status: evaluation.ScenarioUnevaluable, Score: -5},
		{ScenarioID: "c.unassessable", Unassessable: true, Score: -5},
	}
	claim = conformanceClaim(v, r)
	assert.True(t, claim.Supported, "reasons: %v", claim.Reasons)
}

// TestRun_AnOutOfRangeCapabilityResultIsNotVouchedFor: through a whole run.
// The score is written as the scorer produced it — it is not clamped, which
// would read as a judgement the rubric never made — and the run's own
// statement about a conformance claim, and the note every rendering leads
// with, name the scenario.
func TestRun_AnOutOfRangeCapabilityResultIsNotVouchedFor(t *testing.T) {
	scorer := &mockScorer{capabilityResult: &evaluation.ScenarioResult{Passed: false, Score: -0.25}}
	orch := NewOrchestrator(&mockProfileLoader{profile: opsProfile()},
		&mockAgentClient{response: defaultAgentResp()},
		&mockProvider{provisionResp: defaultProvision()},
		&mockAsserter{results: []evaluation.AssertionResult{{Status: evaluation.AssertionPass}}},
		scorer, NewReportWriter(), nil, Config{EvidenceDir: t.TempDir(), Tier: 1, Evaluator: "Example Lab"})

	scenarios := []evaluation.Scenario{
		safetyScenario("s.001", 1),
		capabilityScenarioWithCategory("c.001", 1, "ops"),
	}
	verdict, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "json",
		filepath.Join(t.TempDir(), "verdict.json"))
	require.NoError(t, err)

	assert.Equal(t, -0.25, verdict.CapabilityResults[0].Score, "the score is reported as scored, not clamped")

	claim := verdict.Report.Metadata.ConformanceClaim
	require.NotNil(t, claim)
	assert.False(t, claim.Supported)
	const want = `the capability result of scenario "c.001" is -0.25, outside the 0.0 to 1.0 the verdict format allows`
	assert.Contains(t, strings.Join(claim.Reasons, "\n"), want)
	assert.Contains(t, verdict.Report.Metadata.EvaluationNote, want)
}
