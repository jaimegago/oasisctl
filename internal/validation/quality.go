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

// IntentCoverageReport describes intent field coverage across scenarios.
type IntentCoverageReport struct {
	Total              int
	WithIntent         int
	MissingRequired    []string            // scenario IDs missing intent where required
	MissingRecommended []string            // scenario IDs missing intent where recommended
	DuplicateIntents   map[string][]string // intent text -> list of scenario IDs
}

// ComputeIntentCoverage analyzes intent field coverage across scenarios.
func ComputeIntentCoverage(scenarios []evaluation.Scenario, config evaluation.IntentPromotionConfig) IntentCoverageReport {
	report := IntentCoverageReport{
		Total:            len(scenarios),
		DuplicateIntents: make(map[string][]string),
	}

	requiredSet := make(map[string]bool)
	for _, r := range config.RequiredFor {
		requiredSet[r] = true
	}
	recommendedSet := make(map[string]bool)
	for _, r := range config.RecommendedFor {
		recommendedSet[r] = true
	}

	intentToIDs := make(map[string][]string)
	for _, s := range scenarios {
		if s.Intent != "" {
			report.WithIntent++
			intentToIDs[s.Intent] = append(intentToIDs[s.Intent], s.ID)
		} else {
			classification := string(s.Classification)
			if requiredSet[classification] {
				report.MissingRequired = append(report.MissingRequired, s.ID)
			} else if recommendedSet[classification] {
				report.MissingRecommended = append(report.MissingRecommended, s.ID)
			}
		}
	}

	for intent, ids := range intentToIDs {
		if len(ids) > 1 {
			report.DuplicateIntents[intent] = ids
		}
	}

	return report
}

// SubcategoryDistribution describes how scenarios map to subcategories.
type SubcategoryDistribution struct {
	PerSubcategory      map[string]int // subcategory identifier -> scenario count
	UnusedSubcategories []string       // defined but zero scenarios
	Unassigned          []string       // scenario IDs without subcategory where one exists for their category
}

// ComputeSubcategoryDistribution analyzes subcategory assignment across scenarios.
func ComputeSubcategoryDistribution(scenarios []evaluation.Scenario, subcategories []evaluation.SubcategoryDefinition) SubcategoryDistribution {
	dist := SubcategoryDistribution{
		PerSubcategory: make(map[string]int),
	}

	// Initialize all defined subcategories.
	for _, sub := range subcategories {
		dist.PerSubcategory[sub.Identifier] = 0
	}

	// Build set of categories that have subcategories.
	categoriesWithSubs := make(map[string]bool)
	for _, sub := range subcategories {
		for _, parent := range sub.ParentCategories {
			categoriesWithSubs[parent] = true
		}
	}

	for _, s := range scenarios {
		if s.Subcategory != "" {
			dist.PerSubcategory[s.Subcategory]++
		} else if categoriesWithSubs[s.Category] {
			dist.Unassigned = append(dist.Unassigned, s.ID)
		}
	}

	for id, count := range dist.PerSubcategory {
		if count == 0 {
			dist.UnusedSubcategories = append(dist.UnusedSubcategories, id)
		}
	}

	return dist
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
