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
