package profile_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/profile"
)

// The tier minimums are read from the vendored profile's own §8. A loader that
// stops reading them leaves every coverage claim unestablished, so this fails
// against one.
func TestLoader_ReadsTierMinimumCoverageFromVendoredProfile(t *testing.T) {
	p, err := profile.NewLoader().Load(context.Background(), testdataDir)
	require.NoError(t, err)

	require.Contains(t, p.TierRequirements, 1)
	require.Contains(t, p.TierRequirements, 2)
	require.Contains(t, p.TierRequirements, 3)

	// Tier 1: >=1 archetype per category, both classifications, nothing else.
	t1 := p.TierRequirements[1]
	assert.Equal(t, evaluation.CoverageMinimum{Declared: true, Archetypes: 1}, t1.Safety)
	assert.Equal(t, evaluation.CoverageMinimum{Declared: true, Archetypes: 1}, t1.Capability)
	assert.Empty(t, t1.Uncomputed)

	// Tier 2: >=2, plus a requirement that is not an archetype count.
	t2 := p.TierRequirements[2]
	assert.Equal(t, evaluation.CoverageMinimum{Declared: true, Archetypes: 2}, t2.Safety)
	assert.Equal(t, evaluation.CoverageMinimum{Declared: true, Archetypes: 2}, t2.Capability)
	assert.Len(t, t2.Uncomputed, 1)

	// Tier 3: every archetype.
	t3 := p.TierRequirements[3]
	assert.Equal(t, evaluation.CoverageMinimum{Declared: true, All: true}, t3.Safety)
	assert.Equal(t, evaluation.CoverageMinimum{Declared: true, All: true}, t3.Capability)
	assert.Len(t, t3.Uncomputed, 1)
}

// The profile's suite travels on the loaded profile, so coverage can be
// computed against all of it whatever a run selects.
func TestLoader_CarriesTheScenarioSuite(t *testing.T) {
	p, err := profile.NewLoader().Load(context.Background(), testdataDir)
	require.NoError(t, err)

	require.NotEmpty(t, p.Scenarios)
	classes := map[evaluation.Classification]int{}
	for _, s := range p.Scenarios {
		classes[s.Classification]++
	}
	assert.Positive(t, classes[evaluation.ClassificationSafety])
	assert.Positive(t, classes[evaluation.ClassificationCapability])
}

// The parser reads prose, so what it does with prose it does not recognise is
// the contract: carried verbatim, never dropped, never guessed at.
func TestLoader_TierMinimumFailsClosedOnUnrecognisedProse(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"scenarios/safety", "scenarios/capability"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, sub), 0o755))
	}
	for _, name := range []string{"behavior-definitions.md", "stimulus-library.md"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("# empty\n"), 0o644))
	}
	md := "# Test Profile\n\n" +
		"## 8. Complexity tier requirements\n\n" +
		"### Tier 1 — Minimal\n\n" +
		"**Environment:**\n\n" +
		"- All 9 safety categories: >=7 archetypes per category\n\n" + // an environment bullet is never a minimum
		"**Minimum coverage:**\n\n" +
		"- All 2 safety categories: >=1 archetype per category\n" +
		"- Capability is covered at the evaluator's discretion\n\n" +
		"### Tier 2 — Integrated\n\n" +
		"No minimum is stated here.\n\n" +
		"## 9. Something else\n\n" +
		"- All 2 capability categories: all archetypes\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "profile.md"), []byte(md), 0o644))

	p, err := profile.NewLoader().Load(context.Background(), dir)
	require.NoError(t, err)

	t1 := p.TierRequirements[1]
	assert.Equal(t, evaluation.CoverageMinimum{Declared: true, Archetypes: 1}, t1.Safety)
	assert.False(t, t1.Capability.Declared, "an unrecognised line must not become a capability minimum")
	assert.Equal(t, []string{"Capability is covered at the evaluator's discretion"}, t1.Uncomputed)

	assert.NotContains(t, p.TierRequirements, 2, "a tier that states no minimum has no entry")
}
