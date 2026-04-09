package profile

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// Loader implements evaluation.ProfileLoader.
type Loader struct {
	scenarioParser *ScenarioParser
	behaviorParser *BehaviorParser
	stimulusParser *StimulusParser
}

// NewLoader creates a Loader with default parsers.
func NewLoader() *Loader {
	return &Loader{
		scenarioParser: NewScenarioParser(),
		behaviorParser: NewBehaviorParser(),
		stimulusParser: NewStimulusParser(),
	}
}

// Load reads a profile from dir and returns a validated Profile.
func (l *Loader) Load(ctx context.Context, dir string) (*evaluation.Profile, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("open profile directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("profile path %q is not a directory", dir)
	}

	profileResult, err := parseProfileMD(filepath.Join(dir, "profile.md"))
	if err != nil {
		return nil, fmt.Errorf("parse profile.md: %w", err)
	}

	behaviors, err := l.behaviorParser.Parse(filepath.Join(dir, "behavior-definitions.md"))
	if err != nil {
		return nil, fmt.Errorf("parse behavior-definitions.md: %w", err)
	}

	stimuli, err := l.stimulusParser.Parse(filepath.Join(dir, "stimulus-library.md"))
	if err != nil {
		return nil, fmt.Errorf("parse stimulus-library.md: %w", err)
	}

	// Parse subcategories from safety-categories.md (optional).
	safetyCatPath := filepath.Join(dir, "safety-categories.md")
	var subcategories []evaluation.SubcategoryDefinition
	if _, statErr := os.Stat(safetyCatPath); statErr == nil {
		subcategories, err = parseSubcategories(safetyCatPath)
		if err != nil {
			return nil, fmt.Errorf("parse subcategories: %w", err)
		}
	}

	// Parse agent configuration schema from a dedicated YAML file (optional).
	// If not found here, it may come from profile.md (already parsed above).
	var agentConfigSchema *evaluation.AgentConfigurationSchema
	agentConfigPath := filepath.Join(dir, "agent-configuration-schema.yaml")
	if _, statErr := os.Stat(agentConfigPath); statErr == nil {
		schemaData, readErr := os.ReadFile(agentConfigPath)
		if readErr != nil {
			return nil, fmt.Errorf("read agent-configuration-schema.yaml: %w", readErr)
		}
		var schema evaluation.AgentConfigurationSchema
		if yamlErr := yaml.Unmarshal(schemaData, &schema); yamlErr != nil {
			return nil, fmt.Errorf("parse agent-configuration-schema.yaml: %w", yamlErr)
		}
		if len(schema.Dimensions) > 0 {
			agentConfigSchema = &schema
		}
	}
	// Prefer dedicated file; fall back to profile.md embedded block.
	if agentConfigSchema == nil {
		agentConfigSchema = profileResult.AgentConfigurationSchema
	}

	// Parse provider conformance requirements (optional).
	var conformanceReqs *evaluation.ProviderConformanceRequirements
	conformancePath := filepath.Join(dir, "provider-conformance-requirements.yaml")
	if _, statErr := os.Stat(conformancePath); statErr == nil {
		reqData, readErr := os.ReadFile(conformancePath)
		if readErr != nil {
			return nil, fmt.Errorf("read provider-conformance-requirements.yaml: %w", readErr)
		}
		parsed, parseErr := parseConformanceRequirements(reqData)
		if parseErr != nil {
			return nil, fmt.Errorf("parse provider-conformance-requirements.yaml: %w", parseErr)
		}
		conformanceReqs = parsed
	}

	safetyScenarios, err := l.loadScenariosDir(ctx, filepath.Join(dir, "scenarios", "safety"))
	if err != nil {
		return nil, fmt.Errorf("load safety scenarios: %w", err)
	}

	capabilityScenarios, err := l.loadScenariosDir(ctx, filepath.Join(dir, "scenarios", "capability"))
	if err != nil {
		return nil, fmt.Errorf("load capability scenarios: %w", err)
	}

	allScenarios := append(safetyScenarios, capabilityScenarios...)

	profile := &evaluation.Profile{
		Metadata:                        profileResult.Metadata,
		BehaviorDefinitions:             behaviors,
		StimulusLibrary:                 stimuli,
		Subcategories:                   subcategories,
		IntentPromotion:                 profileResult.IntentPromotion,
		AgentConfigurationSchema:        agentConfigSchema,
		ProviderConformanceRequirements: conformanceReqs,
	}

	// Map subcategories to categories.
	mapSubcategoriesToCategories(profile)

	verr := validateProfileIntegrity(profile, allScenarios)
	if verr != nil && verr.HasIssues() {
		return nil, verr
	}

	return profile, nil
}

// mapSubcategoriesToCategories populates each Category's Subcategories field
// based on the parent category mapping in subcategory definitions.
func mapSubcategoriesToCategories(p *evaluation.Profile) {
	if len(p.Subcategories) == 0 {
		return
	}
	// Build lookup: category ID -> index in SafetyCategories.
	safetyIdx := make(map[string]int)
	for i, cat := range p.SafetyCategories {
		safetyIdx[cat.ID] = i
	}
	capIdx := make(map[string]int)
	for i, cat := range p.CapabilityCategories {
		capIdx[cat.ID] = i
	}

	for _, sub := range p.Subcategories {
		for _, parent := range sub.ParentCategories {
			if idx, ok := safetyIdx[parent]; ok {
				p.SafetyCategories[idx].Subcategories = append(p.SafetyCategories[idx].Subcategories, sub.Identifier)
			}
			if idx, ok := capIdx[parent]; ok {
				p.CapabilityCategories[idx].Subcategories = append(p.CapabilityCategories[idx].Subcategories, sub.Identifier)
			}
		}
	}
}

// loadScenariosDir loads all scenario YAML files from a directory.
func (l *Loader) loadScenariosDir(ctx context.Context, dir string) ([]evaluation.Scenario, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read directory %q: %w", dir, err)
	}

	var all []evaluation.Scenario
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		scenarios, err := l.scenarioParser.Parse(ctx, filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		all = append(all, scenarios...)
	}
	return all, nil
}

// validateProfileIntegrity checks cross-reference consistency.
func validateProfileIntegrity(p *evaluation.Profile, scenarios []evaluation.Scenario) *evaluation.ValidationError {
	verr := &evaluation.ValidationError{}

	// Build a set of all scenario IDs for companion reference checks.
	scenarioIDs := make(map[string]struct{}, len(scenarios))
	for _, s := range scenarios {
		scenarioIDs[s.ID] = struct{}{}
	}

	for _, s := range scenarios {
		// Validate behavior references.
		for _, a := range s.Assertions.Must {
			if a.Behavior != "" {
				if _, ok := p.BehaviorDefinitions[a.Behavior]; !ok {
					verr.Add(fmt.Sprintf("scenario %s", s.ID),
						fmt.Sprintf("must assertion references undefined behavior %q", a.Behavior))
				}
			}
		}
		for _, a := range s.Assertions.MustNot {
			if a.Behavior != "" {
				if _, ok := p.BehaviorDefinitions[a.Behavior]; !ok {
					verr.Add(fmt.Sprintf("scenario %s", s.ID),
						fmt.Sprintf("must_not assertion references undefined behavior %q", a.Behavior))
				}
			}
		}

		// Validate library_ref references.
		for _, stim := range s.Stimuli {
			if stim.LibraryRef != "" {
				if _, ok := p.StimulusLibrary[stim.LibraryRef]; !ok {
					verr.Add(fmt.Sprintf("scenario %s", s.ID),
						fmt.Sprintf("stimulus references undefined library_ref %q", stim.LibraryRef))
				}
			}
		}

		// Validate companion_scenario references.
		if s.Quality.CompanionScenario != "" {
			if _, ok := scenarioIDs[s.Quality.CompanionScenario]; !ok {
				verr.Add(fmt.Sprintf("scenario %s", s.ID),
					fmt.Sprintf("companion_scenario %q not found", s.Quality.CompanionScenario))
			}
		}
	}

	if verr.HasIssues() {
		return verr
	}
	return nil
}
