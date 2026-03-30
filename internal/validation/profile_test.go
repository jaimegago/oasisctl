package validation_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/validation"
)

func makeProfile() *evaluation.Profile {
	return &evaluation.Profile{
		Metadata: evaluation.ProfileMetadata{
			Name:    "test-profile",
			Version: "0.1.0",
		},
		BehaviorDefinitions: map[string]evaluation.BehaviorDefinition{
			"refuse_with_explanation": {Identifier: "refuse_with_explanation"},
		},
		StimulusLibrary: map[string]evaluation.Stimulus{},
		Subcategories: []evaluation.SubcategoryDefinition{
			{
				Identifier:       "scope-boundary",
				ParentCategories: []string{"boundary-enforcement", "authority-escalation"},
			},
			{
				Identifier:       "data-protection",
				ParentCategories: []string{"data-exfiltration"},
			},
		},
	}
}

func TestValidateProfile_ValidSubcategoryReference(t *testing.T) {
	p := makeProfile()
	scenarios := []evaluation.Scenario{
		{
			ID:          "s-001",
			Category:    "boundary-enforcement",
			Subcategory: "scope-boundary",
			Assertions:  evaluation.Assertions{Must: []evaluation.AssertionItem{{Behavior: "refuse_with_explanation"}}},
		},
	}

	verr := validation.ValidateProfile(p, scenarios)
	assert.Nil(t, verr, "valid subcategory reference should pass")
}

func TestValidateProfile_InvalidSubcategoryReference(t *testing.T) {
	p := makeProfile()
	scenarios := []evaluation.Scenario{
		{
			ID:          "s-001",
			Category:    "boundary-enforcement",
			Subcategory: "nonexistent-subcat",
			Assertions:  evaluation.Assertions{Must: []evaluation.AssertionItem{{Behavior: "refuse_with_explanation"}}},
		},
	}

	verr := validation.ValidateProfile(p, scenarios)
	require.NotNil(t, verr)
	found := false
	for _, issue := range verr.Issues {
		if issue.Message == `subcategory "nonexistent-subcat" not defined in profile` {
			found = true
			break
		}
	}
	assert.True(t, found, "should report undefined subcategory")
}

func TestValidateProfile_SubcategoryParentMismatch(t *testing.T) {
	p := makeProfile()
	scenarios := []evaluation.Scenario{
		{
			ID:          "s-001",
			Category:    "data-exfiltration",
			Subcategory: "scope-boundary", // scope-boundary's parents are boundary-enforcement, authority-escalation
			Assertions:  evaluation.Assertions{Must: []evaluation.AssertionItem{{Behavior: "refuse_with_explanation"}}},
		},
	}

	verr := validation.ValidateProfile(p, scenarios)
	require.NotNil(t, verr)
	found := false
	for _, issue := range verr.Issues {
		if assert.ObjectsAreEqual(`category "data-exfiltration" is not a parent of subcategory "scope-boundary"`, issue.Message) {
			found = true
			break
		}
	}
	assert.True(t, found, "should report parent category mismatch")
}

func TestValidateProfile_DuplicateIntents(t *testing.T) {
	p := makeProfile()
	duplicateIntent := "Verify the agent refuses zone violations for security boundary enforcement"

	scenarios := []evaluation.Scenario{
		{
			ID:         "s-001",
			Category:   "boundary-enforcement",
			Intent:     duplicateIntent,
			Assertions: evaluation.Assertions{Must: []evaluation.AssertionItem{{Behavior: "refuse_with_explanation"}}},
		},
		{
			ID:         "s-002",
			Category:   "boundary-enforcement",
			Intent:     duplicateIntent,
			Assertions: evaluation.Assertions{Must: []evaluation.AssertionItem{{Behavior: "refuse_with_explanation"}}},
		},
	}

	verr := validation.ValidateProfile(p, scenarios)
	require.NotNil(t, verr)
	found := false
	for _, issue := range verr.Issues {
		if issue.Field == "intent_uniqueness" {
			found = true
			break
		}
	}
	assert.True(t, found, "should report duplicate intents")
}
