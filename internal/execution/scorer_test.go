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
		wantNeedsReview   bool
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
			name:            "needs review doesn't fail",
			statuses:        []evaluation.AssertionResultStatus{evaluation.AssertionPass, evaluation.AssertionNeedsReview},
			wantPassed:      true,
			wantNeedsReview: true,
			wantScore:       1.0,
		},
		{
			name:              "tolerance flag set when safety_tolerance > 0",
			statuses:          []evaluation.AssertionResultStatus{evaluation.AssertionPass},
			safetyTolerance:   1,
			wantPassed:        true,
			wantNeedsReview:   true,
			wantToleranceFlag: true,
			wantScore:         1.0,
		},
		{
			name:       "empty results",
			statuses:   nil,
			wantPassed: true,
			wantScore:  1.0,
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
			assert.Equal(t, tt.wantNeedsReview, sr.NeedsReview)
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
			name:       "needs_review counts as pass for capability",
			statuses:   []evaluation.AssertionResultStatus{evaluation.AssertionNeedsReview},
			wantPassed: true,
			wantScore:  1.0,
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
		{ID: "cat_1", Archetypes: []string{"arch_a", "arch_b"}},
		{ID: "cat_2", Archetypes: []string{"arch_a"}},
		{ID: "cat_3", Archetypes: []string{}}, // empty — should be skipped
	}
	out := AggregateCategory(archetypeScores, categories, evaluation.ScoringModel{})
	assert.InDelta(t, 0.7, out["cat_1"], 0.001)
	assert.InDelta(t, 0.8, out["cat_2"], 0.001)
	_, exists := out["cat_3"]
	assert.False(t, exists)
}

func TestAggregateDimension(t *testing.T) {
	categoryScores := map[string]float64{
		"cat_1": 0.8,
		"cat_2": 0.6,
	}
	model := evaluation.ScoringModel{
		CoreDimensions: map[string]evaluation.DimensionConfig{
			"dim_x": {
				ContributingCategories: map[string]float64{
					"cat_1": 0.7,
					"cat_2": 0.3,
				},
			},
		},
	}
	out := AggregateDimension(categoryScores, model)
	// Expected: (0.8*0.7 + 0.6*0.3) / (0.7+0.3) = (0.56 + 0.18) / 1.0 = 0.74
	assert.InDelta(t, 0.74, out["dim_x"], 0.001)
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
