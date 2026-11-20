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

// supportedClaimInputs is an assembled evaluation and its report that support
// a conformance claim, for a test to break in one place.
func supportedClaimInputs() (*evaluation.Verdict, *evaluation.Report) {
	whole := evaluation.BuildVerdictAgentIdentity(wholeDeclaration())
	evaluator := "Example Lab"
	v := &evaluation.Verdict{
		Safety: evaluation.SafetyVerdictPass,
		Evaluation: &evaluation.AssembledEvaluation{
			Complete: true,
			Opening: evaluation.EvaluationOpening{
				Evaluation:      "claimed-evaluation",
				ExecutionCounts: evaluation.ExecutionCounts{Safety: 3, Capability: 4},
				Identity: evaluation.EvaluationIdentity{
					Agent: "agent-under-test", AgentVersion: "v1.2.3",
					AgentConfiguration:   evaluation.AgentConfiguration{"operational_mode": "read_only"},
					DomainProfile:        "test-profile",
					DomainProfileVersion: "0.1.0",
					Environment:          evaluation.IdentityEnvironment{Tier: 1},
				},
			},
		},
	}
	r := &evaluation.Report{}
	r.Metadata.EvaluationMode.Complete = true
	r.Metadata.AgentIdentity = &whole
	r.Metadata.Evaluator = &evaluator
	r.SafetySummary.Applicable = 21
	r.Environment.ScenarioFigures = []evaluation.TierCheck{
		{Requirement: "namespaces", Status: evaluation.TierCheckMet},
		{Requirement: "microservices", Status: evaluation.TierCheckMet},
		{Requirement: "security-zones", Status: evaluation.TierCheckMet},
	}
	return v, r
}

// capabilityRecord is a capability scenario's record with the given counted
// execution scores, and one attempt, which has no score to be out of range.
func capabilityRecord(id string, scores ...float64) evaluation.ScenarioRecord {
	executions := []evaluation.ExecutionRecord{{Execution: 1, Outcome: evaluation.AttemptAgentFailure, Cause: "x"}}
	for i, s := range scores {
		executions = append(executions, evaluation.ExecutionRecord{Execution: i + 2, Counted: true, Verdict: evaluation.ScoreVerdict(s)})
	}
	return evaluation.BuildScenarioRecord(id, evaluation.ClassificationCapability, len(scores), executions)
}

// TestConformanceClaim_ReadsEveryCapabilityScoresRange: a capability score is
// from 0.0 to 1.0 (spec/05-reporting.md §1.3). An evaluation carrying one
// outside it wrote a verdict the format forbids, and does not support a
// claim; the reason names the scenario, the execution and the value. Run
// 20261119-234640-ca82d6 carried -0.25 for C-DA-004 and was reported as
// supporting one.
func TestConformanceClaim_ReadsEveryCapabilityScoresRange(t *testing.T) {
	inRange := []evaluation.ScenarioRecord{capabilityRecord("c.fine", 0, 0.5, 1)}

	v, r := supportedClaimInputs()
	v.Evaluation.CapabilityRecords = inRange
	claim := conformanceClaim(v, r)
	assert.True(t, claim.Supported, "0, 0.5 and 1 are all in range; reasons: %v", claim.Reasons)

	for name, tt := range map[string]struct {
		score float64
		want  string
	}{
		"below zero":   {-0.25, `the score of execution 3 of scenario "c.bad" is -0.25, outside the 0.0 to 1.0 the verdict format allows`},
		"above one":    {1.2, `the score of execution 3 of scenario "c.bad" is 1.2, outside the 0.0 to 1.0 the verdict format allows`},
		"not a number": {math.NaN(), `the score of execution 3 of scenario "c.bad" is NaN, outside`},
	} {
		t.Run(name, func(t *testing.T) {
			v, r := supportedClaimInputs()
			v.Evaluation.CapabilityRecords = append(append([]evaluation.ScenarioRecord{}, inRange...), capabilityRecord("c.bad", 0.5, tt.score, 0.5))
			claim := conformanceClaim(v, r)
			assert.False(t, claim.Supported)
			require.Len(t, claim.Reasons, 1, "the range is the only thing wrong with this evaluation")
			assert.Contains(t, claim.Reasons[0], tt.want)
			assert.Nil(t, claim.Claim, "an unsupported claim is not stated")
		})
	}

	// Every one is named, in order.
	v, r = supportedClaimInputs()
	v.Evaluation.CapabilityRecords = []evaluation.ScenarioRecord{
		capabilityRecord("c.first", -1, 0.4, 0.4),
		capabilityRecord("c.fine", 0.4, 0.4, 0.4),
		capabilityRecord("c.second", 0.4, 0.4, 2),
	}
	claim = conformanceClaim(v, r)
	require.Len(t, claim.Reasons, 2)
	assert.Contains(t, claim.Reasons[0], `"c.first"`)
	assert.Contains(t, claim.Reasons[1], `"c.second"`)
}

// TestConformanceClaim_StatesWhatSection3_2Asks: a supported claim names one
// evaluation and states both execution counts, the date as a range, the
// configuration it is indexed by and the applicable safety scenario count.
func TestConformanceClaim_StatesWhatSection3_2Asks(t *testing.T) {
	v, r := supportedClaimInputs()
	claim := conformanceClaim(v, r)
	require.True(t, claim.Supported, "reasons: %v", claim.Reasons)
	require.NotNil(t, claim.Claim)
	c := claim.Claim
	assert.Equal(t, "claimed-evaluation", c.Evaluation)
	assert.Equal(t, evaluation.SafetyVerdictPass, c.Verdict)
	assert.Equal(t, 21, c.ApplicableSafetyScenarios)
	assert.Equal(t, 3, c.SafetyCount)
	assert.Equal(t, 4, c.CapabilityCount)
	assert.Equal(t, evaluation.AgentConfiguration{"operational_mode": "read_only"}, c.AgentConfiguration)
	assert.Contains(t, c.Text, "each given 3 counted execution(s)")
	assert.Contains(t, c.Text, "capability count 4")

	// On a safety FAIL no capability execution was made, and the capability
	// count the claim states is the one the evaluation declared.
	v, r = supportedClaimInputs()
	v.Safety = evaluation.SafetyVerdictFail
	claim = conformanceClaim(v, r)
	require.True(t, claim.Supported, "a FAIL is a verdict a claim can state")
	assert.Equal(t, 4, claim.Claim.CapabilityCount)
	assert.Contains(t, claim.Claim.Text, "No capability execution was made behind the FAIL")

	// Executions made outside any evaluation support no claim, whatever they
	// produced.
	v, r = supportedClaimInputs()
	v.Evaluation = nil
	claim = conformanceClaim(v, r)
	assert.False(t, claim.Supported)
	assert.Contains(t, claim.Reasons[0], "outside any evaluation")
}

// TestRun_AnOutOfRangeCapabilityScoreIsNotVouchedFor: through a whole
// evaluation. The score is written as the scorer produced it — it is not
// clamped, which would read as a judgement the rubric never made — and the
// evaluation's own statement about a conformance claim, and the note every
// rendering leads with, name the scenario.
func TestRun_AnOutOfRangeCapabilityScoreIsNotVouchedFor(t *testing.T) {
	h := newEvalHarness(t)
	h.scorer.capability = func(id string, n int) float64 {
		if id == "c.one" && n == 2 {
			return -0.25
		}
		return 0.5
	}
	out := filepath.Join(t.TempDir(), "verdict.json")
	verdict, _, err := h.orchestrator().Run(context.Background(), "/profile", h.suite, "http://lab-one", "json", out)
	require.NoError(t, err)

	rec := verdict.Evaluation.CapabilityRecords[0]
	require.Equal(t, "c.one", rec.ScenarioID)
	assert.Equal(t, -0.25, rec.Executions[1].Verdict.Score(), "the score is reported as scored, not clamped")
	assert.Equal(t, -0.25, rec.Range.Min)

	claim := verdict.Report.Metadata.ConformanceClaim
	require.NotNil(t, claim)
	assert.False(t, claim.Supported)
	const want = `the score of execution 2 of scenario "c.one" is -0.25, outside the 0.0 to 1.0 the verdict format allows`
	assert.Contains(t, strings.Join(claim.Reasons, "\n"), want)
	assert.Contains(t, verdict.Report.Metadata.EvaluationNote, want)
}
