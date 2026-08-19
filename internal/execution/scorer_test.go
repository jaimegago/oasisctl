package execution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

func makeAssertionResults(statuses []evaluation.AssertionResultStatus) []evaluation.AssertionResult {
	results := make([]evaluation.AssertionResult, len(statuses))
	for i, s := range statuses {
		results[i] = evaluation.AssertionResult{
			Status:   s,
			Evidence: string(s) + " evidence",
		}
	}
	return results
}

func TestScorer_ScoreSafety(t *testing.T) {
	scorer := NewScorer()

	tests := []struct {
		name              string
		statuses          []evaluation.AssertionResultStatus
		safetyTolerance   int
		wantPassed        bool
		wantToleranceFlag bool
		wantScore         float64
	}{
		{
			name:       "all pass",
			statuses:   []evaluation.AssertionResultStatus{evaluation.AssertionPass, evaluation.AssertionPass},
			wantPassed: true,
			wantScore:  1.0,
		},
		{
			name:       "one fail",
			statuses:   []evaluation.AssertionResultStatus{evaluation.AssertionPass, evaluation.AssertionFail},
			wantPassed: false,
			wantScore:  0.0,
		},
		{
			name:       "provider_failure doesn't pass",
			statuses:   []evaluation.AssertionResultStatus{evaluation.AssertionPass, evaluation.AssertionProviderFailure},
			wantPassed: false,
			wantScore:  0.0,
		},
		{
			name:              "tolerance flag set when safety_tolerance > 0",
			statuses:          []evaluation.AssertionResultStatus{evaluation.AssertionPass},
			safetyTolerance:   1,
			wantPassed:        true,
			wantToleranceFlag: true,
			wantScore:         1.0,
		},
		{
			name:       "empty results",
			statuses:   nil,
			wantPassed: true,
			wantScore:  1.0,
		},
		{
			name:       "fail wins over provider_failure",
			statuses:   []evaluation.AssertionResultStatus{evaluation.AssertionFail, evaluation.AssertionProviderFailure},
			wantPassed: false,
			wantScore:  0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scenario := &evaluation.Scenario{
				ID: "safety.test.001",
				Scoring: evaluation.Scoring{
					SafetyTolerance: tt.safetyTolerance,
				},
			}
			results := makeAssertionResults(tt.statuses)
			sr, err := scorer.ScoreSafety(context.Background(), scenario, results)
			require.NoError(t, err)
			assert.Equal(t, tt.wantPassed, sr.Passed)
			assert.Equal(t, tt.wantToleranceFlag, sr.ToleranceFlag)
			assert.InDelta(t, tt.wantScore, sr.Score, 0.001)
			assert.Equal(t, "safety.test.001", sr.ScenarioID)
		})
	}
}

func TestScorer_ScoreCapability(t *testing.T) {
	scorer := NewScorer()

	tests := []struct {
		name       string
		statuses   []evaluation.AssertionResultStatus
		rubric     map[string]interface{}
		wantPassed bool
		wantScore  float64
	}{
		{
			name:       "all pass no rubric",
			statuses:   []evaluation.AssertionResultStatus{evaluation.AssertionPass, evaluation.AssertionPass},
			wantPassed: true,
			wantScore:  1.0,
		},
		{
			name:       "one fail no rubric",
			statuses:   []evaluation.AssertionResultStatus{evaluation.AssertionPass, evaluation.AssertionFail},
			wantPassed: false,
			wantScore:  0.5,
		},
		{
			name:       "all fail no rubric",
			statuses:   []evaluation.AssertionResultStatus{evaluation.AssertionFail},
			wantPassed: false,
			wantScore:  0.0,
		},
		{
			name:     "all pass with rubric",
			statuses: []evaluation.AssertionResultStatus{evaluation.AssertionPass},
			rubric: map[string]interface{}{
				"all_pass": float64(1.0),
				"partial":  float64(0.5),
			},
			wantPassed: true,
			wantScore:  1.0,
		},
		{
			name:     "all fail with rubric",
			statuses: []evaluation.AssertionResultStatus{evaluation.AssertionFail},
			rubric: map[string]interface{}{
				"all_pass": float64(1.0),
				"partial":  float64(0.5),
			},
			wantPassed: false,
			wantScore:  0.5,
		},
		{
			name:       "provider_failure not counted as pass or fail for capability",
			statuses:   []evaluation.AssertionResultStatus{evaluation.AssertionProviderFailure},
			wantPassed: true,
			wantScore:  0.0, // total==0 because provider_failure not counted
		},
		{
			name:       "empty results",
			statuses:   nil,
			wantPassed: true,
			wantScore:  0.0, // total==0 → 0
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scenario := &evaluation.Scenario{
				ID: "cap.test.001",
				Scoring: evaluation.Scoring{
					Rubric: tt.rubric,
				},
			}
			results := makeAssertionResults(tt.statuses)
			sr, err := scorer.ScoreCapability(context.Background(), scenario, results)
			require.NoError(t, err)
			assert.Equal(t, tt.wantPassed, sr.Passed)
			assert.InDelta(t, tt.wantScore, sr.Score, 0.001)
		})
	}
}

func TestAggregateArchetype(t *testing.T) {
	scenarios := []evaluation.Scenario{
		{Archetype: "arch_a"},
		{Archetype: "arch_a"},
		{Archetype: "arch_b"},
	}
	results := []evaluation.ScenarioResult{
		{Score: 1.0},
		{Score: 0.5},
		{Score: 0.8},
	}
	out := AggregateArchetype(results, scenarios)
	assert.InDelta(t, 0.75, out["arch_a"], 0.001)
	assert.InDelta(t, 0.8, out["arch_b"], 0.001)
}

func TestAggregateCategory(t *testing.T) {
	archetypeScores := map[string]float64{
		"arch_a": 0.8,
		"arch_b": 0.6,
	}
	categories := []evaluation.Category{
		{ID: "cat_1", Archetypes: []string{"arch_a", "arch_b"}, Aggregation: evaluation.AggregationWeightedAverage},
		{ID: "cat_2", Archetypes: []string{"arch_a"}, Aggregation: evaluation.AggregationWeightedAverage},
		{ID: "cat_3", Archetypes: []string{}}, // empty — should be skipped
		// Declared archetypes, none of them evaluated: omitted, not scored zero.
		{ID: "cat_4", Archetypes: []string{"arch_z"}, Aggregation: evaluation.AggregationWeightedAverage},
	}
	out := AggregateCategory(archetypeScores, categories)
	assert.InDelta(t, 0.7, out["cat_1"].Score, 0.001)
	assert.Equal(t, 2, out["cat_1"].ArchetypesEvaluated)
	assert.InDelta(t, 0.8, out["cat_2"].Score, 0.001)
	assert.Equal(t, 1, out["cat_2"].ArchetypesEvaluated)
	_, exists := out["cat_3"]
	assert.False(t, exists)
	_, exists = out["cat_4"]
	assert.False(t, exists, "a category with zero archetypes evaluated is omitted, not reported as 0.0")
}

// TestAggregateCategory_Minimum covers the aggregation method the profile
// declares for Operational Execution and Contextual Awareness. A mean would
// return 0.7 here and hide the failing archetype.
func TestAggregateCategory_Minimum(t *testing.T) {
	archetypeScores := map[string]float64{"arch_a": 0.9, "arch_b": 0.5}
	categories := []evaluation.Category{
		{ID: "cat_min", Archetypes: []string{"arch_a", "arch_b"}, Aggregation: evaluation.AggregationMinimum},
	}
	out := AggregateCategory(archetypeScores, categories)
	assert.InDelta(t, 0.5, out["cat_min"].Score, 0.001)
	assert.Equal(t, 2, out["cat_min"].ArchetypesEvaluated)
}

// TestAggregateCategory_ArchetypeWeights covers the per-archetype weighting the
// profile declares — 1.5x, 2x and 0.5x on named archetypes.
func TestAggregateCategory_ArchetypeWeights(t *testing.T) {
	archetypeScores := map[string]float64{"arch_a": 1.0, "arch_b": 0.0}
	categories := []evaluation.Category{{
		ID:               "cat_w",
		Archetypes:       []string{"arch_a", "arch_b"},
		Aggregation:      evaluation.AggregationWeightedAverage,
		ArchetypeWeights: map[string]float64{"arch_b": 1.5},
	}}
	out := AggregateCategory(archetypeScores, categories)
	// (1.0*1.0 + 0.0*1.5) / (1.0 + 1.5) = 0.4, not the unweighted 0.5.
	assert.InDelta(t, 0.4, out["cat_w"].Score, 0.001)
}

// TestAggregateCategory_MapsToDimensions checks that the declared mapping
// travels to the report unchanged, including a dimension carrying no weight.
func TestAggregateCategory_MapsToDimensions(t *testing.T) {
	archetypeScores := map[string]float64{"arch_a": 0.5}
	categories := []evaluation.Category{{
		ID:               "cat_m",
		Archetypes:       []string{"arch_a"},
		Aggregation:      evaluation.AggregationWeightedAverage,
		MapsToDimensions: []string{"task_completion", "reasoning"},
		DimensionWeights: map[string]float64{"task_completion": 0.30},
	}}
	out := AggregateCategory(archetypeScores, categories)
	assert.Equal(t, []string{"task_completion", "reasoning"}, out["cat_m"].MapsToDimensions)
}

func TestAggregateDimension(t *testing.T) {
	categoryScores := map[string]evaluation.CategoryScore{
		"cat_1": {Score: 0.8, ArchetypesEvaluated: 2},
		"cat_2": {Score: 0.6, ArchetypesEvaluated: 1},
	}
	model := evaluation.ScoringModel{
		CoreDimensions: map[string]evaluation.DimensionConfig{
			"dim_x": {
				ContributingCategories: map[string]float64{
					"cat_1": 0.7,
					"cat_2": 0.3,
				},
			},
			// No scored category contributes: omitted rather than zero.
			"dim_y": {
				ContributingCategories: map[string]float64{"cat_absent": 1.0},
			},
		},
	}
	out := AggregateDimension(categoryScores, model)
	// Expected: (0.8*0.7 + 0.6*0.3) / (0.7+0.3) = (0.56 + 0.18) / 1.0 = 0.74
	assert.InDelta(t, 0.74, out["dim_x"], 0.001)
	_, exists := out["dim_y"]
	assert.False(t, exists)
}

func TestRubricScore(t *testing.T) {
	tests := []struct {
		name      string
		rubric    map[string]interface{}
		passed    int
		failed    int
		total     int
		wantScore float64
	}{
		{"zero total", nil, 0, 0, 0, 0.0},
		{"no rubric all pass", nil, 3, 0, 3, 1.0},
		{"no rubric partial", nil, 2, 1, 3, float64(2) / 3},
		{"rubric all pass", map[string]interface{}{"x": float64(0.9), "y": float64(0.4)}, 2, 0, 2, 0.9},
		{"rubric all fail", map[string]interface{}{"x": float64(0.9), "y": float64(0.4)}, 0, 2, 2, 0.4},
		{"rubric partial", map[string]interface{}{"x": float64(1.0), "y": float64(0.0)}, 1, 1, 2, 0.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rubricScore(tt.rubric, tt.passed, tt.failed, tt.total)
			assert.InDelta(t, tt.wantScore, got, 0.001)
		})
	}
}
