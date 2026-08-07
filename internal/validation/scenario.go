// Package validation implements profile and scenario validation.
package validation

import (
	"fmt"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/scoring"
)

// ValidateScenario checks a single scenario for structural correctness.
// If intentConfig is non-nil, intent presence is validated against the promotion rules.
func ValidateScenario(s evaluation.Scenario, intentConfig ...evaluation.IntentPromotionConfig) *evaluation.ValidationError {
	verr := &evaluation.ValidationError{}

	if s.ID == "" {
		verr.Add("id", "required")
	}
	if s.Name == "" {
		verr.Add("name", "required")
	}
	// version is optional per spec/02-scenarios.md §1.1: a scenario that omits it
	// inherits the version of its parent profile.
	if err := s.Classification.Validate(); err != nil {
		verr.Add("classification", err.Error())
	}
	if s.Category == "" {
		verr.Add("category", "required")
	}
	if s.Archetype == "" {
		verr.Add("archetype", "required")
	}
	if s.Tier == 0 {
		verr.Add("tier", "required and must be 1, 2, or 3")
	}

	for i, stim := range s.Stimuli {
		if err := stim.Type.Validate(); err != nil {
			verr.Add("stimuli", "invalid type at index "+itoa(i)+": "+err.Error())
		}
	}

	validateConcern(s, verr)
	validateScoringForm(s, verr)

	// value_containment is a verification method per spec/02-scenarios.md §1.6,
	// listed alongside state_assertions, api_audit, negative_verification and
	// state_diff.
	hasVerification := len(s.Verification.StateAssertions) > 0 ||
		len(s.Verification.APIAudit) > 0 ||
		len(s.Verification.NegativeVerification) > 0 ||
		len(s.Verification.ValueContainment) > 0 ||
		s.Verification.StateDiff != nil
	if !hasVerification {
		verr.Add("verification", "at least one verification method is required")
	}

	if len(s.Observability) == 0 {
		verr.Add("observability_requirements", "at least one observability requirement is required")
	}

	// Intent validation against promotion config.
	if len(intentConfig) > 0 {
		validateIntent(s, intentConfig[0], verr)
	}

	if verr.HasIssues() {
		return verr
	}
	return nil
}

// validateConcern enforces spec/02-scenarios.md §1.5: every scenario must declare
// at least one verifiable concern, but a must/must_not entry is only one of the
// three shapes that satisfies it. A value-containment entry IS the assertion for
// a scenario whose threat is captured entirely by containment, and a Form B
// scoring binding IS the evaluation for a capability scenario whose bands the
// archetype decision table decides.
func validateConcern(s evaluation.Scenario, verr *evaluation.ValidationError) {
	if len(s.Assertions.Must) > 0 || len(s.Assertions.MustNot) > 0 {
		return
	}
	if len(s.Verification.ValueContainment) > 0 || s.Scoring.IsFormB() {
		return
	}
	verr.Add("assertions",
		"a scenario must declare at least one concern: a must/must_not assertion, a verification.value_containment entry, or a Form B scoring binding")
}

// validateScoringForm enforces spec/02-scenarios.md §1.7. Safety scenarios are
// always binary. Capability scenarios use exactly one of Form A (weighted rubric)
// and Form B (archetype_template binding); Form B omits scoring.type entirely, so
// the enum check must not run against it.
func validateScoringForm(s evaluation.Scenario, verr *evaluation.ValidationError) {
	if s.Classification == evaluation.ClassificationCapability && s.Scoring.IsFormB() {
		if s.Scoring.IsFormA() {
			verr.Add("scoring",
				"declares both Form A (type/rubric/dimensions) and Form B (archetype_template); the two forms are mutually exclusive")
		}
		// A Form B scenario must reference a template that exists and must bind
		// the roles that template declares. An unregistered reference is
		// non-conformant (spec/02-scenarios.md §1.7) and is caught here rather
		// than degrading into a runtime skip that would score every agent alike.
		if _, err := scoring.Bind(&s); err != nil {
			verr.Add("scoring.archetype_template", err.Error())
		}
		return
	}

	if err := s.Scoring.Type.Validate(); err != nil {
		verr.Add("scoring.type", err.Error())
	}

	// Binary scoring for safety, weighted for capability.
	if s.Classification == evaluation.ClassificationSafety && s.Scoring.Type != evaluation.ScoringTypeBinary {
		verr.Add("scoring.type", "safety scenarios must use binary scoring")
	}
	if s.Classification == evaluation.ClassificationCapability && s.Scoring.Type != evaluation.ScoringTypeWeighted {
		verr.Add("scoring.type", "capability scenarios must use weighted scoring, or a Form B archetype_template binding")
	}
}

// validateIntent checks intent presence and length based on promotion rules.
func validateIntent(s evaluation.Scenario, cfg evaluation.IntentPromotionConfig, verr *evaluation.ValidationError) {
	classification := string(s.Classification)

	// Check if intent is required for this classification.
	for _, req := range cfg.RequiredFor {
		if req == classification && s.Intent == "" {
			verr.Add("intent", fmt.Sprintf("required for %s scenarios", classification))
			return
		}
	}

	// Minimum length check when intent is present.
	if s.Intent != "" && len(s.Intent) < 20 {
		verr.Add("intent", "must be at least 20 characters when present")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	result := []byte{}
	for i > 0 {
		result = append([]byte{byte('0' + i%10)}, result...)
		i /= 10
	}
	return string(result)
}
