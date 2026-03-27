// Package validation implements profile and scenario validation.
package validation

import (
	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// ValidateScenario checks a single scenario for structural correctness.
func ValidateScenario(s evaluation.Scenario) *evaluation.ValidationError {
	verr := &evaluation.ValidationError{}

	if s.ID == "" {
		verr.Add("id", "required")
	}
	if s.Name == "" {
		verr.Add("name", "required")
	}
	if s.Version == "" {
		verr.Add("version", "required")
	}
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

	if len(s.Assertions.Must) == 0 && len(s.Assertions.MustNot) == 0 {
		verr.Add("assertions", "at least one must or must_not assertion is required")
	}

	if err := s.Scoring.Type.Validate(); err != nil {
		verr.Add("scoring.type", err.Error())
	}

	// Binary scoring for safety, weighted for capability.
	if s.Classification == evaluation.ClassificationSafety && s.Scoring.Type != evaluation.ScoringTypeBinary {
		verr.Add("scoring.type", "safety scenarios must use binary scoring")
	}
	if s.Classification == evaluation.ClassificationCapability && s.Scoring.Type != evaluation.ScoringTypeWeighted {
		verr.Add("scoring.type", "capability scenarios must use weighted scoring")
	}

	hasVerification := len(s.Verification.StateAssertions) > 0 ||
		len(s.Verification.APIAudit) > 0 ||
		len(s.Verification.NegativeVerification) > 0 ||
		s.Verification.StateDiff != nil
	if !hasVerification {
		verr.Add("verification", "at least one verification method is required")
	}

	if len(s.Observability) == 0 {
		verr.Add("observability_requirements", "at least one observability requirement is required")
	}

	if verr.HasIssues() {
		return verr
	}
	return nil
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
