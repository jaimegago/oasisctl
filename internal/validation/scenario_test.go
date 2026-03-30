package validation_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/validation"
)

func TestValidateScenario_ValidSafetyScenario(t *testing.T) {
	s := evaluation.Scenario{
		ID:             "test.safety.001",
		Name:           "Test scenario",
		Version:        "0.1.0",
		Classification: evaluation.ClassificationSafety,
		Category:       "test-category",
		Archetype:      "S-TEST-001",
		Tier:           1,
		Stimuli: []evaluation.Stimulus{
			{Type: evaluation.StimulusTypeOperatorPrompt, Value: "Do something."},
		},
		Assertions: evaluation.Assertions{
			Must: []evaluation.AssertionItem{{Behavior: "refuse_with_explanation"}},
		},
		Verification: evaluation.Verification{
			NegativeVerification: []string{"no operations performed"},
		},
		Scoring: evaluation.Scoring{
			Type:            evaluation.ScoringTypeBinary,
			SafetyTolerance: 0,
		},
		Observability: []string{"agent reasoning trace"},
	}

	verr := validation.ValidateScenario(s)
	assert.Nil(t, verr, "valid safety scenario should not produce errors")
}

func TestValidateScenario_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name      string
		scenario  evaluation.Scenario
		wantField string
	}{
		{
			name:      "missing ID",
			scenario:  evaluation.Scenario{Name: "x", Version: "1.0.0", Classification: evaluation.ClassificationSafety, Category: "c", Archetype: "a", Tier: 1},
			wantField: "id",
		},
		{
			name:      "missing name",
			scenario:  evaluation.Scenario{ID: "x", Version: "1.0.0", Classification: evaluation.ClassificationSafety, Category: "c", Archetype: "a", Tier: 1},
			wantField: "name",
		},
		{
			name:      "missing tier",
			scenario:  evaluation.Scenario{ID: "x", Name: "x", Version: "1.0.0", Classification: evaluation.ClassificationSafety, Category: "c", Archetype: "a"},
			wantField: "tier",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			verr := validation.ValidateScenario(tc.scenario)
			require.NotNil(t, verr)
			found := false
			for _, issue := range verr.Issues {
				if issue.Field == tc.wantField {
					found = true
					break
				}
			}
			assert.True(t, found, "expected issue for field %q", tc.wantField)
		})
	}
}

func TestValidateScenario_InvalidStimulusType(t *testing.T) {
	s := evaluation.Scenario{
		ID: "x", Name: "x", Version: "1.0.0",
		Classification: evaluation.ClassificationSafety,
		Category:       "c", Archetype: "a", Tier: 1,
		Stimuli:       []evaluation.Stimulus{{Type: "invalid_type"}},
		Assertions:    evaluation.Assertions{Must: []evaluation.AssertionItem{{Behavior: "b"}}},
		Verification:  evaluation.Verification{NegativeVerification: []string{"x"}},
		Scoring:       evaluation.Scoring{Type: evaluation.ScoringTypeBinary},
		Observability: []string{"trace"},
	}

	verr := validation.ValidateScenario(s)
	require.NotNil(t, verr)
	assert.True(t, verr.HasIssues())
}

func TestValidateScenario_IntentRequiredForSafety(t *testing.T) {
	cfg := evaluation.IntentPromotionConfig{
		RequiredFor:    []string{"safety"},
		RecommendedFor: []string{"capability"},
	}

	s := evaluation.Scenario{
		ID: "test.safety.001", Name: "Test", Version: "0.1.0",
		Classification: evaluation.ClassificationSafety,
		Category:       "test-cat", Archetype: "S-TEST-001", Tier: 1,
		Stimuli:       []evaluation.Stimulus{{Type: evaluation.StimulusTypeOperatorPrompt, Value: "x"}},
		Assertions:    evaluation.Assertions{Must: []evaluation.AssertionItem{{Behavior: "refuse_with_explanation"}}},
		Verification:  evaluation.Verification{NegativeVerification: []string{"none"}},
		Scoring:       evaluation.Scoring{Type: evaluation.ScoringTypeBinary},
		Observability: []string{"trace"},
		// Intent is empty — should fail validation.
	}

	verr := validation.ValidateScenario(s, cfg)
	require.NotNil(t, verr)
	found := false
	for _, issue := range verr.Issues {
		if issue.Field == "intent" {
			found = true
			break
		}
	}
	assert.True(t, found, "should report missing intent for safety scenario")
}

func TestValidateScenario_CapabilityWithoutIntentIsNotError(t *testing.T) {
	cfg := evaluation.IntentPromotionConfig{
		RequiredFor:    []string{"safety"},
		RecommendedFor: []string{"capability"},
	}

	s := evaluation.Scenario{
		ID: "test.cap.001", Name: "Test", Version: "0.1.0",
		Classification: evaluation.ClassificationCapability,
		Category:       "test-cat", Archetype: "C-TEST-001", Tier: 1,
		Stimuli:       []evaluation.Stimulus{{Type: evaluation.StimulusTypeOperatorPrompt, Value: "x"}},
		Assertions:    evaluation.Assertions{Must: []evaluation.AssertionItem{{Behavior: "b"}}},
		Verification:  evaluation.Verification{NegativeVerification: []string{"none"}},
		Scoring:       evaluation.Scoring{Type: evaluation.ScoringTypeWeighted},
		Observability: []string{"trace"},
		// Intent is empty — should NOT be an error for capability.
	}

	verr := validation.ValidateScenario(s, cfg)
	assert.Nil(t, verr, "capability scenario without intent should pass with recommendation only")
}

func TestValidateScenario_IntentTooShort(t *testing.T) {
	cfg := evaluation.IntentPromotionConfig{
		RequiredFor: []string{"safety"},
	}

	s := evaluation.Scenario{
		ID: "test.safety.001", Name: "Test", Version: "0.1.0",
		Classification: evaluation.ClassificationSafety,
		Category:       "test-cat", Archetype: "S-TEST-001", Tier: 1,
		Intent:        "Too short",
		Stimuli:       []evaluation.Stimulus{{Type: evaluation.StimulusTypeOperatorPrompt, Value: "x"}},
		Assertions:    evaluation.Assertions{Must: []evaluation.AssertionItem{{Behavior: "b"}}},
		Verification:  evaluation.Verification{NegativeVerification: []string{"none"}},
		Scoring:       evaluation.Scoring{Type: evaluation.ScoringTypeBinary},
		Observability: []string{"trace"},
	}

	verr := validation.ValidateScenario(s, cfg)
	require.NotNil(t, verr)
	found := false
	for _, issue := range verr.Issues {
		if issue.Field == "intent" {
			found = true
			break
		}
	}
	assert.True(t, found, "should report intent too short")
}

func TestValidateScenario_WrongScoringTypeForClassification(t *testing.T) {
	tests := []struct {
		name           string
		classification evaluation.Classification
		scoringType    evaluation.ScoringType
		wantErr        bool
	}{
		{"safety must be binary", evaluation.ClassificationSafety, evaluation.ScoringTypeBinary, false},
		{"safety with weighted fails", evaluation.ClassificationSafety, evaluation.ScoringTypeWeighted, true},
		{"capability must be weighted", evaluation.ClassificationCapability, evaluation.ScoringTypeWeighted, false},
		{"capability with binary fails", evaluation.ClassificationCapability, evaluation.ScoringTypeBinary, true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			s := evaluation.Scenario{
				ID: "x", Name: "x", Version: "1.0.0",
				Classification: tc.classification,
				Category:       "c", Archetype: "a", Tier: 1,
				Stimuli:       []evaluation.Stimulus{{Type: evaluation.StimulusTypeOperatorPrompt, Value: "v"}},
				Assertions:    evaluation.Assertions{Must: []evaluation.AssertionItem{{Behavior: "b"}}},
				Verification:  evaluation.Verification{NegativeVerification: []string{"x"}},
				Scoring:       evaluation.Scoring{Type: tc.scoringType},
				Observability: []string{"trace"},
			}
			verr := validation.ValidateScenario(s)
			if tc.wantErr {
				require.NotNil(t, verr)
				assert.True(t, verr.HasIssues())
			} else {
				assert.Nil(t, verr)
			}
		})
	}
}
