package validation_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/validation"
)

func TestNegativeTestingRatio(t *testing.T) {
	scenarios := []evaluation.Scenario{
		{
			ID:             "safety-001",
			Classification: evaluation.ClassificationSafety,
			Quality:        evaluation.QualityMetadata{CompanionScenario: "capability-001"},
		},
		{
			ID:             "safety-002",
			Classification: evaluation.ClassificationSafety,
			Quality:        evaluation.QualityMetadata{CompanionScenario: "capability-002"},
		},
		{
			ID:             "safety-003",
			Classification: evaluation.ClassificationSafety,
			// No companion scenario.
		},
		{ID: "capability-001", Classification: evaluation.ClassificationCapability},
		{ID: "capability-002", Classification: evaluation.ClassificationCapability},
	}

	ratio := validation.NegativeTestingRatio(scenarios)
	// 2 out of 3 safety scenarios have valid companion references.
	assert.InDelta(t, 2.0/3.0, ratio, 0.001)
}

func TestComputeDifficultyDistribution(t *testing.T) {
	scenarios := []evaluation.Scenario{
		{
			Classification: evaluation.ClassificationSafety,
			Quality: evaluation.QualityMetadata{
				Difficulty: evaluation.Difficulty{
					Plausibility:        "low",
					DetectionComplexity: "medium",
					ContextualEmbedding: "high",
				},
			},
		},
		{
			Classification: evaluation.ClassificationSafety,
			Quality: evaluation.QualityMetadata{
				Difficulty: evaluation.Difficulty{
					Plausibility:        "high",
					DetectionComplexity: "medium",
					ContextualEmbedding: "low",
				},
			},
		},
		// Capability scenarios should be ignored.
		{Classification: evaluation.ClassificationCapability},
	}

	dist := validation.ComputeDifficultyDistribution(scenarios)
	assert.Equal(t, 1, dist.Plausibility["low"])
	assert.Equal(t, 1, dist.Plausibility["high"])
	assert.Equal(t, 2, dist.DetectionComplexity["medium"])
	assert.Equal(t, 0, dist.DetectionComplexity["low"])
}

func TestComputeIntentCoverage(t *testing.T) {
	cfg := evaluation.IntentPromotionConfig{
		RequiredFor:    []string{"safety"},
		RecommendedFor: []string{"capability"},
	}

	scenarios := []evaluation.Scenario{
		{
			ID:             "safety-001",
			Classification: evaluation.ClassificationSafety,
			Intent:         "Verify the agent refuses zone violations for security boundary enforcement",
		},
		{
			ID:             "safety-002",
			Classification: evaluation.ClassificationSafety,
			// Missing intent — required.
		},
		{
			ID:             "capability-001",
			Classification: evaluation.ClassificationCapability,
			// Missing intent — recommended.
		},
		{
			ID:             "capability-002",
			Classification: evaluation.ClassificationCapability,
			Intent:         "Verify the agent can diagnose cascading failures across service dependencies",
		},
	}

	report := validation.ComputeIntentCoverage(scenarios, cfg)
	assert.Equal(t, 4, report.Total)
	assert.Equal(t, 2, report.WithIntent)
	assert.Equal(t, []string{"safety-002"}, report.MissingRequired)
	assert.Equal(t, []string{"capability-001"}, report.MissingRecommended)
	assert.Empty(t, report.DuplicateIntents)
}

func TestComputeIntentCoverage_Duplicates(t *testing.T) {
	cfg := evaluation.IntentPromotionConfig{}
	duplicateIntent := "Verify the agent refuses zone violations for security boundary enforcement"

	scenarios := []evaluation.Scenario{
		{ID: "s-001", Classification: evaluation.ClassificationSafety, Intent: duplicateIntent},
		{ID: "s-002", Classification: evaluation.ClassificationSafety, Intent: duplicateIntent},
	}

	report := validation.ComputeIntentCoverage(scenarios, cfg)
	assert.Len(t, report.DuplicateIntents, 1)
	assert.ElementsMatch(t, []string{"s-001", "s-002"}, report.DuplicateIntents[duplicateIntent])
}

func TestComputeSubcategoryDistribution(t *testing.T) {
	subcategories := []evaluation.SubcategoryDefinition{
		{Identifier: "scope-boundary", ParentCategories: []string{"boundary-enforcement"}},
		{Identifier: "data-protection", ParentCategories: []string{"data-exfiltration"}},
		{Identifier: "unused-subcat", ParentCategories: []string{"some-category"}},
	}

	scenarios := []evaluation.Scenario{
		{ID: "s-001", Category: "boundary-enforcement", Subcategory: "scope-boundary"},
		{ID: "s-002", Category: "boundary-enforcement", Subcategory: "scope-boundary"},
		{ID: "s-003", Category: "data-exfiltration", Subcategory: "data-protection"},
		{ID: "s-004", Category: "boundary-enforcement"}, // no subcategory, but category has subs
	}

	dist := validation.ComputeSubcategoryDistribution(scenarios, subcategories)
	assert.Equal(t, 2, dist.PerSubcategory["scope-boundary"])
	assert.Equal(t, 1, dist.PerSubcategory["data-protection"])
	assert.Equal(t, 0, dist.PerSubcategory["unused-subcat"])
	assert.Contains(t, dist.UnusedSubcategories, "unused-subcat")
	assert.Equal(t, []string{"s-004"}, dist.Unassigned)
}

func TestComputeCoverage(t *testing.T) {
	scenarios := []evaluation.Scenario{
		{Category: "cat-a", Archetype: "arch-1", Classification: evaluation.ClassificationSafety},
		{Category: "cat-a", Archetype: "arch-2", Classification: evaluation.ClassificationSafety},
		{Category: "cat-b", Archetype: "arch-1", Classification: evaluation.ClassificationCapability},
	}

	report := validation.ComputeCoverage(scenarios)
	assert.Len(t, report.ArchetypesByCategory["cat-a"], 2)
	assert.Len(t, report.ArchetypesByCategory["cat-b"], 1)
}
