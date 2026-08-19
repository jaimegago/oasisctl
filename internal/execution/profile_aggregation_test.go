package execution_test

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/execution"
	"github.com/jaimegago/oasisctl/internal/profile"
)

// vendoredProfileDir is the Software Infrastructure profile as the oasis-spec
// submodule pins it. Every assertion in this file reads that directory.
const vendoredProfileDir = "../../testdata/oasis-spec/profiles/software-infrastructure"

// loadVendoredProfile loads the real profile through the real loader. It is the
// point of this file: nothing here constructs a Profile, a Category or a
// ScoringModel as a literal.
//
// The defect these tests exist to prevent survived six months of green CI
// precisely because every test of the aggregation built its own categories by
// hand. The aggregation functions were correct on the inputs the tests gave
// them, and production never gave them any input at all — the loader returned a
// Profile literal that set seven fields and left SafetyCategories,
// CapabilityCategories and ScoringModel zero. Nothing errored; the aggregation
// ranged over nil, returned empty maps, and the report rendered complete with no
// capability scores in it.
//
// So a test that hands AggregateCategory a literal category cannot fail for the
// reason that matters. Only a load from disk can.
func loadVendoredProfile(t *testing.T) *evaluation.Profile {
	t.Helper()
	p, err := profile.NewLoader().Load(context.Background(), vendoredProfileDir)
	require.NoError(t, err)
	require.NotNil(t, p)
	return p
}

// TestLoadedProfilePopulatesScoringFields is the enforcement test. It fails
// against a loader that does not read the profile's category data, whatever
// else that loader gets right.
func TestLoadedProfilePopulatesScoringFields(t *testing.T) {
	p := loadVendoredProfile(t)

	require.NotEmpty(t, p.SafetyCategories, "loaded profile declares no safety categories")
	require.NotEmpty(t, p.CapabilityCategories, "loaded profile declares no capability categories")
	require.NotEmpty(t, p.ScoringModel.CoreDimensions, "loaded profile declares no core dimensions")

	// Every capability category carries the four things the aggregation and the
	// report need. A category missing any of them would aggregate silently.
	for _, cat := range p.CapabilityCategories {
		assert.NotEmpty(t, cat.ID, "capability category with no identifier")
		assert.NotEmpty(t, cat.Name, "capability category %q has no name", cat.ID)
		assert.NotEmpty(t, cat.Archetypes, "capability category %q declares no archetypes", cat.ID)
		assert.NotEmpty(t, cat.MapsToDimensions, "capability category %q maps to no core dimension", cat.ID)
		assert.Contains(t,
			[]evaluation.AggregationMethod{evaluation.AggregationWeightedAverage, evaluation.AggregationMinimum},
			cat.Aggregation, "capability category %q declares no aggregation method", cat.ID)
	}

	for _, cat := range p.SafetyCategories {
		assert.NotEmpty(t, cat.ID, "safety category with no identifier")
		assert.NotEmpty(t, cat.Archetypes, "safety category %q declares no archetypes", cat.ID)
	}

	// The four core dimensions are fixed by spec/05-reporting.md §1, not chosen
	// by the profile. A profile that renamed one would break every published
	// verdict's comparability.
	for _, dim := range []string{"task_completion", "reliability", "reasoning", "auditability"} {
		cfg, ok := p.ScoringModel.CoreDimensions[dim]
		require.True(t, ok, "core dimension %q missing from the loaded scoring model", dim)
		assert.NotEmpty(t, cfg.ContributingCategories,
			"core dimension %q has no contributing category, so it can never be scored", dim)
	}
}

// TestLoadedProfileProducesNonEmptyAggregation runs the loaded profile through
// the aggregation the orchestrator runs, and asserts it produces numbers.
//
// The archetype scores are synthetic — they stand in for a run's results, which
// no unit test can produce — but the categories, the weights, the aggregation
// methods and the dimension mappings all come off disk.
func TestLoadedProfileProducesNonEmptyAggregation(t *testing.T) {
	p := loadVendoredProfile(t)

	// Score every declared archetype, so every category is fully covered.
	archetypeScores := map[string]float64{}
	for _, cat := range p.CapabilityCategories {
		for _, arch := range cat.Archetypes {
			archetypeScores[arch] = 0.5
		}
	}
	require.NotEmpty(t, archetypeScores)

	categoryScores := execution.AggregateCategory(archetypeScores, p.CapabilityCategories)
	require.NotEmpty(t, categoryScores, "aggregation produced no category scores from a real profile")
	assert.Len(t, categoryScores, len(p.CapabilityCategories))

	dimensionScores := execution.AggregateDimension(categoryScores, p.ScoringModel)
	require.NotEmpty(t, dimensionScores, "aggregation produced no core dimension scores from a real profile")

	for id, cs := range categoryScores {
		assert.InDelta(t, 0.5, cs.Score, 0.001, "category %q", id)
		assert.NotZero(t, cs.ArchetypesEvaluated, "category %q reports no archetypes evaluated", id)
		assert.NotEmpty(t, cs.MapsToDimensions, "category %q reports no dimension mapping", id)
	}
	for dim, score := range dimensionScores {
		assert.InDelta(t, 0.5, score, 0.001, "dimension %q", dim)
	}
}

// TestLoadedProfileAggregatesByDeclaredMethod is the half that a uniform score
// cannot catch. Every category above scored 0.5 on every archetype, which a
// mean and a minimum agree on. Here they disagree.
func TestLoadedProfileAggregatesByDeclaredMethod(t *testing.T) {
	p := loadVendoredProfile(t)

	var sawMinimum, sawWeighted bool
	for _, cat := range p.CapabilityCategories {
		require.GreaterOrEqual(t, len(cat.Archetypes), 2, "category %q", cat.ID)

		// One archetype at 0.0, the rest at 1.0. A minimum returns 0.0; any
		// weighted average returns something above it.
		archetypeScores := map[string]float64{}
		for i, arch := range cat.Archetypes {
			if i == 0 {
				archetypeScores[arch] = 0.0
				continue
			}
			archetypeScores[arch] = 1.0
		}

		out := execution.AggregateCategory(archetypeScores, []evaluation.Category{cat})
		got, ok := out[cat.ID]
		require.True(t, ok, "category %q produced no score", cat.ID)

		switch cat.Aggregation {
		case evaluation.AggregationMinimum:
			sawMinimum = true
			assert.Zero(t, got.Score,
				"category %q declares minimum aggregation but did not return the lowest archetype score", cat.ID)
		case evaluation.AggregationWeightedAverage:
			sawWeighted = true
			assert.Greater(t, got.Score, 0.0,
				"category %q declares weighted average but returned the minimum", cat.ID)
			assert.Less(t, got.Score, 1.0, "category %q", cat.ID)
		}
	}

	assert.True(t, sawMinimum, "no category in the loaded profile aggregates by minimum")
	assert.True(t, sawWeighted, "no category in the loaded profile aggregates by weighted average")
}

// TestLoadedProfileHonoursArchetypeWeights checks that a declared per-archetype
// weight changes the number. A category whose weights were parsed but ignored
// would pass every assertion above.
func TestLoadedProfileHonoursArchetypeWeights(t *testing.T) {
	p := loadVendoredProfile(t)

	var checked int
	for _, cat := range p.CapabilityCategories {
		if cat.Aggregation != evaluation.AggregationWeightedAverage || len(cat.ArchetypeWeights) == 0 {
			continue
		}

		// Score the weighted archetypes 0.0 and the unweighted ones 1.0. The
		// result equals the unweighted mean only if the weights were ignored.
		archetypeScores := map[string]float64{}
		unweighted := 0
		for _, arch := range cat.Archetypes {
			if _, weighted := cat.ArchetypeWeights[arch]; weighted {
				archetypeScores[arch] = 0.0
				continue
			}
			archetypeScores[arch] = 1.0
			unweighted++
		}

		out := execution.AggregateCategory(archetypeScores, []evaluation.Category{cat})
		mean := float64(unweighted) / float64(len(cat.Archetypes))
		assert.Greater(t, math.Abs(out[cat.ID].Score-mean), 0.001,
			"category %q declares archetype weights but scored as an unweighted mean", cat.ID)
		checked++
	}

	assert.NotZero(t, checked, "no category in the loaded profile declares archetype weights")
}

// TestLoadedProfileCategoriesCoverScenarioCategories ties the category block to
// the scenario corpus beside it. A category identifier that no scenario uses,
// or a scenario category the block never declares, means archetype scores land
// in a category that will never be aggregated.
func TestLoadedProfileCategoriesCoverScenarioCategories(t *testing.T) {
	p := loadVendoredProfile(t)

	declared := map[string]struct{}{}
	for _, cat := range append(append([]evaluation.Category{}, p.SafetyCategories...), p.CapabilityCategories...) {
		declared[cat.ID] = struct{}{}
	}

	parser := profile.NewScenarioParser()
	var seen int
	for _, sub := range []string{"safety", "capability"} {
		dir := filepath.Join(vendoredProfileDir, "scenarios", sub)
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
				continue
			}
			scenarios, err := parser.Parse(context.Background(), filepath.Join(dir, e.Name()))
			require.NoError(t, err)
			for _, s := range scenarios {
				seen++
				_, ok := declared[s.Category]
				assert.True(t, ok,
					"scenario %s declares category %q, which the profile's category data does not",
					s.ID, s.Category)
			}
		}
	}
	require.NotZero(t, seen)
}
