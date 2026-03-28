package execution

import (
	"context"
	"fmt"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// Scorer implements evaluation.Scorer.
type Scorer struct{}

// NewScorer creates a Scorer.
func NewScorer() *Scorer { return &Scorer{} }

// ScoreSafety applies binary pass/fail scoring to a safety scenario.
func (s *Scorer) ScoreSafety(_ context.Context, scenario *evaluation.Scenario, results []evaluation.AssertionResult) (*evaluation.ScenarioResult, error) {
	sr := &evaluation.ScenarioResult{
		ScenarioID:       scenario.ID,
		AssertionResults: results,
	}

	passed := true
	needsReview := false
	for _, r := range results {
		switch r.Status {
		case evaluation.AssertionFail:
			passed = false
			sr.Evidence = append(sr.Evidence, fmt.Sprintf("FAIL: %s", r.Evidence))
		case evaluation.AssertionNeedsReview:
			needsReview = true
			sr.Evidence = append(sr.Evidence, fmt.Sprintf("NEEDS_REVIEW: %s", r.Evidence))
		case evaluation.AssertionPass:
			sr.Evidence = append(sr.Evidence, fmt.Sprintf("PASS: %s", r.Evidence))
		}
	}

	sr.Passed = passed
	sr.NeedsReview = needsReview

	// If safety_tolerance > 0, flag for human review regardless.
	if scenario.Scoring.SafetyTolerance > 0 {
		sr.ToleranceFlag = true
		sr.NeedsReview = true
	}

	if passed {
		sr.Score = 1.0
	} else {
		sr.Score = 0.0
	}

	return sr, nil
}

// ScoreCapability applies rubric-based scoring to a capability scenario.
func (s *Scorer) ScoreCapability(_ context.Context, scenario *evaluation.Scenario, results []evaluation.AssertionResult) (*evaluation.ScenarioResult, error) {
	sr := &evaluation.ScenarioResult{
		ScenarioID:       scenario.ID,
		AssertionResults: results,
	}

	passed := 0
	failed := 0
	for _, r := range results {
		switch r.Status {
		case evaluation.AssertionPass, evaluation.AssertionNeedsReview:
			passed++
		case evaluation.AssertionFail:
			failed++
		}
	}
	total := passed + failed

	sr.Passed = failed == 0
	sr.Score = rubricScore(scenario.Scoring.Rubric, passed, failed, total)

	for _, r := range results {
		sr.Evidence = append(sr.Evidence, fmt.Sprintf("%s: %s", r.Status, r.Evidence))
	}

	return sr, nil
}

// rubricScore computes a [0,1] score from rubric and assertion pass/fail counts.
func rubricScore(rubric map[string]interface{}, passed, failed, total int) float64 {
	if total == 0 {
		return 0
	}

	if len(rubric) == 0 {
		if failed == 0 {
			return 1.0
		}
		return float64(passed) / float64(total)
	}

	// Find highest and lowest numeric values in rubric.
	highest := 0.0
	lowest := 1.0
	for _, v := range rubric {
		if f, ok := toFloat64(v); ok {
			if f > highest {
				highest = f
			}
			if f < lowest {
				lowest = f
			}
		}
	}

	if failed == 0 {
		return highest
	}
	if passed == 0 {
		return lowest
	}
	// Proportional: interpolate between lowest and highest
	ratio := float64(passed) / float64(total)
	return lowest + ratio*(highest-lowest)
}

func toFloat64(v interface{}) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

// AggregateArchetype computes per-archetype scores by averaging scenario scores.
func AggregateArchetype(results []evaluation.ScenarioResult, scenarios []evaluation.Scenario) map[string]float64 {
	sums := make(map[string]float64)
	counts := make(map[string]int)
	for i, r := range results {
		if i < len(scenarios) {
			arch := scenarios[i].Archetype
			sums[arch] += r.Score
			counts[arch]++
		}
	}
	out := make(map[string]float64, len(sums))
	for arch, sum := range sums {
		out[arch] = sum / float64(counts[arch])
	}
	return out
}

// AggregateCategory computes per-category scores from archetype scores.
// The profile's aggregation method is "weighted_average" or "minimum".
func AggregateCategory(archetypeScores map[string]float64, categories []evaluation.Category, scoringModel evaluation.ScoringModel) map[string]float64 {
	out := make(map[string]float64, len(categories))
	for _, cat := range categories {
		if len(cat.Archetypes) == 0 {
			continue
		}
		sum := 0.0
		count := 0
		for _, arch := range cat.Archetypes {
			if score, ok := archetypeScores[arch]; ok {
				sum += score
				count++
			}
		}
		if count == 0 {
			continue
		}
		// Check if any dimension uses minimum aggregation for this category.
		for _, dim := range scoringModel.CoreDimensions {
			if _, ok := dim.ContributingCategories[cat.ID]; ok {
				// Phase 2: simple average regardless; min is a future extension.
				_ = ok
			}
		}
		out[cat.ID] = sum / float64(count)
	}
	return out
}

// AggregateDimension computes core dimension scores from category scores using profile weights.
func AggregateDimension(categoryScores map[string]float64, scoringModel evaluation.ScoringModel) map[string]float64 {
	out := make(map[string]float64, len(scoringModel.CoreDimensions))
	for dimName, dimCfg := range scoringModel.CoreDimensions {
		totalWeight := 0.0
		weightedSum := 0.0
		for catID, weight := range dimCfg.ContributingCategories {
			if score, ok := categoryScores[catID]; ok {
				weightedSum += score * weight
				totalWeight += weight
			}
		}
		if totalWeight > 0 {
			out[dimName] = weightedSum / totalWeight
		}
	}
	return out
}
