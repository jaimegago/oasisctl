package validation

import (
	"fmt"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// ValidateProfile checks a loaded profile for completeness and internal consistency.
func ValidateProfile(p *evaluation.Profile, scenarios []evaluation.Scenario) *evaluation.ValidationError {
	verr := &evaluation.ValidationError{}

	// Required components.
	if p.Metadata.Name == "" {
		verr.Add("metadata.name", "required")
	}
	if p.Metadata.Version == "" {
		verr.Add("metadata.version", "required")
	}
	if len(p.BehaviorDefinitions) == 0 {
		verr.Add("behavior_definitions", "profile must define at least one behavior")
	}

	// Build scenario ID set.
	scenarioIDs := make(map[string]struct{}, len(scenarios))
	for _, s := range scenarios {
		scenarioIDs[s.ID] = struct{}{}
	}

	// Build subcategory lookup: identifier -> SubcategoryDefinition.
	subcatMap := make(map[string]evaluation.SubcategoryDefinition)
	for _, sub := range p.Subcategories {
		subcatMap[sub.Identifier] = sub
	}

	// Validate each scenario in the context of the profile.
	for _, s := range scenarios {
		scenErr := validateScenarioAgainstProfile(s, p)
		if scenErr != nil && scenErr.HasIssues() {
			for _, issue := range scenErr.Issues {
				verr.Add(fmt.Sprintf("scenario %s: %s", s.ID, issue.Field), issue.Message)
			}
		}

		// Subcategory reference validation.
		if s.Subcategory != "" {
			sub, ok := subcatMap[s.Subcategory]
			if !ok {
				verr.Add(fmt.Sprintf("scenario %s", s.ID),
					fmt.Sprintf("subcategory %q not defined in profile", s.Subcategory))
			} else {
				// Subcategory parent validation: scenario's category must be in subcategory's parents.
				if !stringSliceContains(sub.ParentCategories, s.Category) {
					verr.Add(fmt.Sprintf("scenario %s", s.ID),
						fmt.Sprintf("category %q is not a parent of subcategory %q", s.Category, s.Subcategory))
				}
			}
		}
	}

	// Intent uniqueness: no two scenarios should have identical non-empty intent values.
	intentToScenarios := make(map[string][]string)
	for _, s := range scenarios {
		if s.Intent != "" {
			intentToScenarios[s.Intent] = append(intentToScenarios[s.Intent], s.ID)
		}
	}
	for intent, ids := range intentToScenarios {
		if len(ids) > 1 {
			verr.Add("intent_uniqueness",
				fmt.Sprintf("duplicate intent %q found in scenarios: %v", truncate(intent, 60), ids))
		}
	}

	// Coverage: build set of archetypes represented.
	archetypeCategories := make(map[string]map[string]struct{})
	for _, s := range scenarios {
		if _, ok := archetypeCategories[s.Category]; !ok {
			archetypeCategories[s.Category] = make(map[string]struct{})
		}
		archetypeCategories[s.Category][s.Archetype] = struct{}{}
	}

	// Companion scenario validation.
	for _, s := range scenarios {
		if s.Quality.CompanionScenario != "" {
			if _, ok := scenarioIDs[s.Quality.CompanionScenario]; !ok {
				verr.Add(fmt.Sprintf("scenario %s", s.ID),
					fmt.Sprintf("companion_scenario %q not found in scenario set", s.Quality.CompanionScenario))
			}
		}
	}

	if verr.HasIssues() {
		return verr
	}
	return nil
}

func stringSliceContains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func validateScenarioAgainstProfile(s evaluation.Scenario, p *evaluation.Profile) *evaluation.ValidationError {
	verr := &evaluation.ValidationError{}

	for _, a := range s.Assertions.Must {
		if a.Behavior != "" {
			if _, ok := p.BehaviorDefinitions[a.Behavior]; !ok {
				verr.Add("assertions.must", fmt.Sprintf("undefined behavior %q", a.Behavior))
			}
		}
	}
	for _, a := range s.Assertions.MustNot {
		if a.Behavior != "" {
			if _, ok := p.BehaviorDefinitions[a.Behavior]; !ok {
				verr.Add("assertions.must_not", fmt.Sprintf("undefined behavior %q", a.Behavior))
			}
		}
	}
	for _, stim := range s.Stimuli {
		if stim.LibraryRef != "" {
			if _, ok := p.StimulusLibrary[stim.LibraryRef]; !ok {
				verr.Add("stimuli.library_ref", fmt.Sprintf("undefined library_ref %q", stim.LibraryRef))
			}
		}
	}

	if verr.HasIssues() {
		return verr
	}
	return nil
}
