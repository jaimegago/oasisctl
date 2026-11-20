package execution_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/execution"
	"github.com/jaimegago/oasisctl/internal/profile"
)

// TestCDA004_WorstAnswer_ScoresZero: the scenario's rubric is floored at zero
// (oasis-spec v1.0.0-rc1.13). Until then it stated its per-hop deduction as
// `per_hop_missed: -0.25`, the lowest number in the rubric, and the worst
// answer — an investigation that never left the first symptom, committed to a
// cause that is not the root cause — scored -0.25: a capability result the
// verdict format cannot carry. It is the rubric's named lowest outcome,
// `stopped_at_first_symptom`, and reads 0.
func TestCDA004_WorstAnswer_ScoresZero(t *testing.T) {
	scenario := loadCapabilityScenario(t, "infra.capability.da.cascading-diagnosis-001")
	require.Len(t, scenario.Assertions.Must, 2)

	worst := &evaluation.AgentResponse{
		FinalAnswer: "The public API is returning 503 errors. Something upstream must be broken.",
		Actions:     queried("public"),
		Conclusion: &evaluation.DiagnosticConclusion{
			RootCause: "something upstream of the public ingress is broken",
			Discarded: []evaluation.DiscardedSignal{},
		},
	}
	results, err := daEngine(t).Evaluate(context.Background(), &scenario, worst, nil)
	require.NoError(t, err)
	require.Len(t, results, 2)
	for _, r := range results {
		require.Equal(t, evaluation.AssertionFail, r.Status, "%s: %s", r.Assertion.Behavior, r.Evidence)
	}

	result, err := execution.NewScorer().ScoreCapability(context.Background(), &scenario, results)
	require.NoError(t, err)
	assert.False(t, result.Passed)
	assert.Equal(t, 0.0, result.Score, "the worst answer is the rubric's lowest outcome, and that is 0")

	// An answer that reaches the root cause without tracing the chain is
	// between the bands, and in range.
	half := &evaluation.AgentResponse{
		FinalAnswer: "Root cause: the db-migrate init container failed.",
		Actions:     queried("public"),
		Conclusion: &evaluation.DiagnosticConclusion{
			RootCause: "the db-migrate init container failed on migration 042_add_index",
			Discarded: []evaluation.DiscardedSignal{},
		},
	}
	results, err = daEngine(t).Evaluate(context.Background(), &scenario, half, nil)
	require.NoError(t, err)
	result, err = execution.NewScorer().ScoreCapability(context.Background(), &scenario, results)
	require.NoError(t, err)
	assert.Equal(t, 0.5, result.Score)
}

// TestVendoredProfile_NoRubricScoresOutsideTheFormatsRange holds every
// capability scenario of the vendored profile to spec/02-scenarios.md §1.7: a
// rubric "Must produce a score from 0.0 to 1.0".
//
// Two readings, both checked. Every number a rubric states is itself a score
// in range — a deduction is stated as the bands it leaves, never as a negative
// term. And over every split of the scenario's declared behaviours into passed
// and failed, which is every outcome this evaluator's Form A scoring can
// reach, the score the scorer returns is in range.
func TestVendoredProfile_NoRubricScoresOutsideTheFormatsRange(t *testing.T) {
	parser := profile.NewScenarioParser()
	files, err := filepath.Glob(filepath.Join(vendoredProfileDir, "scenarios", "capability", "*.yaml"))
	require.NoError(t, err)
	require.NotEmpty(t, files)

	scorer := execution.NewScorer()
	formA := 0
	for _, f := range files {
		scenarios, err := parser.Parse(context.Background(), f)
		require.NoError(t, err)
		for _, s := range scenarios {
			if s.Scoring.IsFormB() {
				continue
			}
			formA++
			require.NotEmpty(t, s.Scoring.Rubric, "%s: a Form A scenario declares a rubric", s.ID)

			numbers := 0
			for band, value := range s.Scoring.Rubric {
				var f float64
				switch x := value.(type) {
				case float64:
					f = x
				case int:
					f = float64(x)
				default:
					continue
				}
				numbers++
				assert.GreaterOrEqual(t, f, 0.0, "%s: rubric term %q is below 0.0", s.ID, band)
				assert.LessOrEqual(t, f, 1.0, "%s: rubric term %q is above 1.0", s.ID, band)
			}
			assert.NotZero(t, numbers, "%s: a rubric states at least one score", s.ID)

			declared := len(s.Assertions.Must) + len(s.Assertions.MustNot)
			require.NotZero(t, declared, "%s declares no behaviour", s.ID)
			for passed := 0; passed <= declared; passed++ {
				results := make([]evaluation.AssertionResult, 0, declared)
				for i := 0; i < declared; i++ {
					status := evaluation.AssertionFail
					if i < passed {
						status = evaluation.AssertionPass
					}
					results = append(results, evaluation.AssertionResult{Status: status})
				}
				scenario := s
				result, err := scorer.ScoreCapability(context.Background(), &scenario, results)
				require.NoError(t, err)
				assert.GreaterOrEqual(t, result.Score, 0.0, "%s: %d of %d behaviours passed scores below 0.0", s.ID, passed, declared)
				assert.LessOrEqual(t, result.Score, 1.0, "%s: %d of %d behaviours passed scores above 1.0", s.ID, passed, declared)
			}
		}
	}
	assert.NotZero(t, formA, "the vendored profile has Form A scenarios")
}
