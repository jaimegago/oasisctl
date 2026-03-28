package profile_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/profile"
)

func TestSubcategoryParsing(t *testing.T) {
	loader := profile.NewLoader()
	ctx := context.Background()

	p, err := loader.Load(ctx, testdataDir)
	require.NoError(t, err)
	require.NotNil(t, p)

	// The safety-categories.md defines 5 subcategories.
	assert.Len(t, p.Subcategories, 5, "should parse 5 subcategory definitions")

	// Verify specific subcategories.
	subMap := make(map[string][]string)
	for _, sub := range p.Subcategories {
		subMap[sub.Identifier] = sub.ParentCategories
	}

	assert.Contains(t, subMap, "scope-boundary-respect")
	assert.Contains(t, subMap, "self-modification-resistance")
	assert.Contains(t, subMap, "sensitive-data-protection")
	assert.Contains(t, subMap, "destructive-action-gating")
	assert.Contains(t, subMap, "state-consistency")

	// scope-boundary-respect spans two parent categories.
	assert.ElementsMatch(t, []string{"boundary-enforcement", "authority-escalation-resistance"}, subMap["scope-boundary-respect"])

	// self-modification-resistance spans three parent categories.
	assert.Len(t, subMap["self-modification-resistance"], 3)
}

func TestIntentPromotionParsing(t *testing.T) {
	loader := profile.NewLoader()
	ctx := context.Background()

	p, err := loader.Load(ctx, testdataDir)
	require.NoError(t, err)

	assert.Equal(t, []string{"safety"}, p.IntentPromotion.RequiredFor)
	assert.Equal(t, []string{"capability"}, p.IntentPromotion.RecommendedFor)
}

func TestScenarioParser_IntentAndSubcategory(t *testing.T) {
	parser := profile.NewScenarioParser()
	ctx := context.Background()

	scenarios, err := parser.Parse(ctx, filepath.Join(testdataDir, "scenarios", "safety", "boundary-enforcement.yaml"))
	require.NoError(t, err)
	require.Len(t, scenarios, 3)

	// First scenario should have intent and subcategory.
	assert.Equal(t, "scope-boundary-respect", scenarios[0].Subcategory)
	assert.NotEmpty(t, scenarios[0].Intent, "safety scenario should have intent")
	assert.True(t, len(scenarios[0].Intent) >= 20, "intent should be at least 20 characters")

	// Third scenario has a different subcategory.
	assert.Equal(t, "self-modification-resistance", scenarios[2].Subcategory)
}

func TestScenarioParser_CapabilityWithoutIntentAndSubcategory(t *testing.T) {
	parser := profile.NewScenarioParser()
	ctx := context.Background()

	scenarios, err := parser.Parse(ctx, filepath.Join(testdataDir, "scenarios", "capability", "contextual-awareness.yaml"))
	require.NoError(t, err)
	require.NotEmpty(t, scenarios)

	// Capability scenarios should not have intent or subcategory.
	for _, s := range scenarios {
		assert.Empty(t, s.Intent, "capability scenarios should not have intent")
		assert.Empty(t, s.Subcategory, "capability scenarios should not have subcategory")
	}
}

func TestAllSafetyScenariosHaveIntentAndSubcategory(t *testing.T) {
	parser := profile.NewScenarioParser()
	ctx := context.Background()

	safetyDir := filepath.Join(testdataDir, "scenarios", "safety")
	files := []string{
		"boundary-enforcement.yaml",
		"blast-radius-containment.yaml",
		"authority-escalation-resistance.yaml",
		"prompt-injection-resistance.yaml",
		"data-exfiltration-prevention.yaml",
		"destructive-operation-safeguarding.yaml",
		"state-corruption-prevention.yaml",
	}

	for _, file := range files {
		scenarios, err := parser.Parse(ctx, filepath.Join(safetyDir, file))
		require.NoError(t, err, "parsing %s", file)
		for _, s := range scenarios {
			assert.NotEmpty(t, s.Intent, "scenario %s should have intent", s.ID)
			assert.NotEmpty(t, s.Subcategory, "scenario %s should have subcategory", s.ID)
		}
	}
}
