package validation

import (
	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// DifficultyDistribution holds counts of difficulty ratings across three axes.
type DifficultyDistribution struct {
	Plausibility        map[string]int
	DetectionComplexity map[string]int
	ContextualEmbedding map[string]int
}

// ComputeDifficultyDistribution calculates the difficulty distribution for safety scenarios.
func ComputeDifficultyDistribution(scenarios []evaluation.Scenario) DifficultyDistribution {
	dist := DifficultyDistribution{
		Plausibility:        make(map[string]int),
		DetectionComplexity: make(map[string]int),
		ContextualEmbedding: make(map[string]int),
	}
	for _, s := range scenarios {
		if s.Classification != evaluation.ClassificationSafety {
			continue
		}
		if s.Quality.Difficulty.Plausibility != "" {
			dist.Plausibility[s.Quality.Difficulty.Plausibility]++
		}
		if s.Quality.Difficulty.DetectionComplexity != "" {
			dist.DetectionComplexity[s.Quality.Difficulty.DetectionComplexity]++
		}
		if s.Quality.Difficulty.ContextualEmbedding != "" {
			dist.ContextualEmbedding[s.Quality.Difficulty.ContextualEmbedding]++
		}
	}
	return dist
}

// CoverageReport describes scenario coverage by category and archetype.
type CoverageReport struct {
	// ArchetypesByCategory maps category -> set of archetypes covered.
	ArchetypesByCategory map[string][]string
	// UncoveredCategories lists categories with zero scenarios.
	UncoveredCategories []string
}

// ComputeCoverage determines which categories and archetypes have scenarios.
func ComputeCoverage(scenarios []evaluation.Scenario) CoverageReport {
	archetypesByCategory := make(map[string]map[string]struct{})
	for _, s := range scenarios {
		if _, ok := archetypesByCategory[s.Category]; !ok {
			archetypesByCategory[s.Category] = make(map[string]struct{})
		}
		archetypesByCategory[s.Category][s.Archetype] = struct{}{}
	}

	report := CoverageReport{
		ArchetypesByCategory: make(map[string][]string),
	}
	for cat, archetypes := range archetypesByCategory {
		list := make([]string, 0, len(archetypes))
		for a := range archetypes {
			list = append(list, a)
		}
		report.ArchetypesByCategory[cat] = list
	}

	return report
}

// NegativeTestingRatio returns the fraction of safety archetypes that have a companion capability scenario.
func NegativeTestingRatio(scenarios []evaluation.Scenario) float64 {
	companionIDs := make(map[string]struct{})
	for _, s := range scenarios {
		if s.Classification == evaluation.ClassificationCapability {
			companionIDs[s.ID] = struct{}{}
		}
	}

	var safety, withCompanion int
	for _, s := range scenarios {
		if s.Classification != evaluation.ClassificationSafety {
			continue
		}
		safety++
		if s.Quality.CompanionScenario != "" {
			if _, ok := companionIDs[s.Quality.CompanionScenario]; ok {
				withCompanion++
			}
		}
	}

	if safety == 0 {
		return 0
	}
	return float64(withCompanion) / float64(safety)
}
