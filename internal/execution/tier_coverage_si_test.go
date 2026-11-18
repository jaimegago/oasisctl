package execution_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/execution"
)

// These tests compute coverage over the vendored Software Infrastructure
// profile, loaded from disk: its categories, its tier minimums, its scenario
// suite and that suite's applicability blocks. Nothing about the profile is a
// literal here, on the reasoning loadVendoredProfile gives.

var readOnly = evaluation.AgentConfiguration{evaluation.OperationalModeDimension: evaluation.OperationalModeReadOnly}

const (
	operationalExecution = "operational-execution"
	diagnosticAccuracy   = "diagnostic-accuracy"
)

// runResults builds the results a run would produce over the profile's suite
// under config: a scenario the configuration excludes is NOT_APPLICABLE, every
// other one is evaluated to PASS. drop removes scenarios from the run
// entirely, as a filter would.
func runResults(p *evaluation.Profile, config evaluation.AgentConfiguration, drop func(evaluation.Scenario) bool) []evaluation.ScenarioResult {
	var out []evaluation.ScenarioResult
	for i := range p.Scenarios {
		s := p.Scenarios[i]
		if drop != nil && drop(s) {
			continue
		}
		r := evaluation.ScenarioResult{ScenarioID: s.ID, Category: s.Category, Archetype: s.Archetype, Passed: true}
		if execution.IsApplicable(&s, config) {
			r.Status = evaluation.ScenarioPass
		} else {
			r.Status = evaluation.ScenarioNotApplicable
		}
		out = append(out, r)
	}
	return out
}

func categoryByID(t *testing.T, cm *evaluation.CoverageMatrix, id string) evaluation.CategoryCoverage {
	t.Helper()
	for _, c := range cm.Categories {
		if c.Category == id {
			return c
		}
	}
	require.Failf(t, "category missing from coverage matrix", "%q", id)
	return evaluation.CategoryCoverage{}
}

// A read_only agent evaluated across everything open to it meets Tier 1's
// minimum, and Operational Execution is excluded rather than unmet (SI profile
// §8.1; spec/05-reporting.md §3.3).
func TestCoverage_SIReadOnly_OperationalExecutionExcludedAndMinimumMet(t *testing.T) {
	p := loadVendoredProfile(t)

	cm := execution.ComputeCoverage(p, p.Scenarios, readOnly, 1, runResults(p, readOnly, nil))

	assert.Equal(t, 1, cm.Tier)
	assert.True(t, cm.MinimumCoverageMet, "unmet: %v", cm.Unmet)
	assert.Empty(t, cm.Unmet)
	require.Len(t, cm.Categories, len(p.SafetyCategories)+len(p.CapabilityCategories),
		"the matrix lists every category the profile declares")

	oe := categoryByID(t, cm, operationalExecution)
	assert.Equal(t, evaluation.CoverageExcluded, oe.Status)
	assert.Equal(t, evaluation.ClassificationCapability, oe.Classification)
	assert.Zero(t, oe.ArchetypesApplicable)
	assert.Zero(t, oe.ArchetypesRequired, "an excluded category requires nothing")
	assert.Empty(t, oe.ArchetypesEvaluated)
	require.Len(t, oe.ArchetypesSkipped, oe.ArchetypesDeclared, "every declared archetype is accounted for")
	for _, sk := range oe.ArchetypesSkipped {
		assert.Equal(t, evaluation.SkipNotApplicable, sk.Reason, sk.Archetype)
		assert.Contains(t, sk.Detail, "operational_mode=read_write", sk.Archetype)
	}

	// Every safety category, and every capability category the configuration
	// leaves open, is met. Operational Execution is pinned by name above; any
	// other category the vendored profile closes to read_only is excluded on
	// the same ground — no applicable archetype — and on no other.
	for _, c := range cm.Categories {
		if c.Status == evaluation.CoverageExcluded {
			assert.Equal(t, evaluation.ClassificationCapability, c.Classification, "%s: only a capability category can be excluded", c.Category)
			assert.Zero(t, c.ArchetypesApplicable, "%s: excluded with an applicable archetype", c.Category)
			continue
		}
		assert.Equal(t, evaluation.CoverageMet, c.Status, c.Category)
		assert.Equal(t, 1, c.ArchetypesRequired, c.Category)
		assert.GreaterOrEqual(t, len(c.ArchetypesEvaluated), 1, c.Category)
		assert.Positive(t, c.ArchetypesApplicable, c.Category)
		assert.Equal(t, c.ArchetypesDeclared, len(c.ArchetypesEvaluated)+len(c.ArchetypesSkipped),
			"%s: evaluated and skipped must account for every declared archetype", c.Category)
	}
	assert.Equal(t, evaluation.CoverageMet, categoryByID(t, cm, diagnosticAccuracy).Status,
		"a category read_only can run is never excluded")
	for _, cat := range p.SafetyCategories {
		c := categoryByID(t, cm, cat.ID)
		assert.Equal(t, evaluation.ClassificationSafety, c.Classification)
		assert.Equal(t, c.ArchetypesDeclared, len(c.ArchetypesEvaluated), "%s: no safety archetype is excluded under read_only", cat.ID)
	}
}

// The same agent with one open category left unevaluated does not meet the
// minimum: that category is unmet, and the excluded one stays excluded.
func TestCoverage_SIReadOnly_MinimumUnmetWhenAnOpenCategoryIsNotEvaluated(t *testing.T) {
	p := loadVendoredProfile(t)

	results := runResults(p, readOnly, func(s evaluation.Scenario) bool { return s.Category == diagnosticAccuracy })
	cm := execution.ComputeCoverage(p, p.Scenarios, readOnly, 1, results)

	assert.False(t, cm.MinimumCoverageMet)
	require.Len(t, cm.Unmet, 1)
	assert.Contains(t, cm.Unmet[0], diagnosticAccuracy)
	assert.Contains(t, cm.Unmet[0], "tier 1 requires 1")

	da := categoryByID(t, cm, diagnosticAccuracy)
	assert.Equal(t, evaluation.CoverageUnmet, da.Status)
	assert.Equal(t, 1, da.ArchetypesRequired)
	assert.Empty(t, da.ArchetypesEvaluated)
	assert.Equal(t, da.ArchetypesDeclared, da.ArchetypesApplicable, "the category is open to read_only; it was not run")
	for _, sk := range da.ArchetypesSkipped {
		assert.Equal(t, evaluation.SkipNotRun, sk.Reason, sk.Archetype)
	}

	assert.Equal(t, evaluation.CoverageExcluded, categoryByID(t, cm, operationalExecution).Status)
}

// A run stopped at the safety gate evaluated no capability archetype. Every
// category open to the configuration is unmet; the excluded one is still
// excluded, because the configuration decides that and the run does not.
func TestCoverage_SIReadOnly_CapabilityPhaseNeverRan(t *testing.T) {
	p := loadVendoredProfile(t)

	safetyOnly := runResults(p, readOnly, func(s evaluation.Scenario) bool {
		return s.Classification == evaluation.ClassificationCapability
	})
	cm := execution.ComputeCoverage(p, p.Scenarios, readOnly, 1, safetyOnly)

	assert.False(t, cm.MinimumCoverageMet)
	for _, cat := range p.SafetyCategories {
		assert.Equal(t, evaluation.CoverageMet, categoryByID(t, cm, cat.ID).Status, cat.ID)
	}
	// A capability category is unmet when the configuration leaves it open
	// and excluded when it does not; which ones are open is the profile's.
	open := 0
	for _, cat := range p.CapabilityCategories {
		c := categoryByID(t, cm, cat.ID)
		if c.ArchetypesApplicable == 0 {
			assert.Equal(t, evaluation.CoverageExcluded, c.Status, cat.ID)
			continue
		}
		open++
		assert.Equal(t, evaluation.CoverageUnmet, c.Status, cat.ID)
	}
	assert.Equal(t, evaluation.CoverageExcluded, categoryByID(t, cm, operationalExecution).Status)
	assert.Equal(t, evaluation.CoverageUnmet, categoryByID(t, cm, diagnosticAccuracy).Status)
	assert.Len(t, cm.Unmet, open)
}

// Under read_write nothing is excluded: Operational Execution counts, and is
// met when it is evaluated and unmet when it is not.
func TestCoverage_SIReadWrite_OperationalExecutionCounts(t *testing.T) {
	p := loadVendoredProfile(t)
	readWrite := evaluation.AgentConfiguration{evaluation.OperationalModeDimension: "read_write"}

	cm := execution.ComputeCoverage(p, p.Scenarios, readWrite, 1, runResults(p, readWrite, nil))
	assert.True(t, cm.MinimumCoverageMet, "unmet: %v", cm.Unmet)
	oe := categoryByID(t, cm, operationalExecution)
	assert.Equal(t, evaluation.CoverageMet, oe.Status)
	assert.Equal(t, oe.ArchetypesDeclared, len(oe.ArchetypesEvaluated))

	withoutOE := runResults(p, readWrite, func(s evaluation.Scenario) bool { return s.Category == operationalExecution })
	cm = execution.ComputeCoverage(p, p.Scenarios, readWrite, 1, withoutOE)
	assert.False(t, cm.MinimumCoverageMet)
	assert.Equal(t, evaluation.CoverageUnmet, categoryByID(t, cm, operationalExecution).Status)
}

// A scenario that ran and reached no judgement covers nothing: one that
// errored before it was scored, a provider failure, an agent failure, or one
// whose every behaviour was unassessable.
func TestCoverage_SI_ScenarioThatReachedNoJudgementCoversNothing(t *testing.T) {
	p := loadVendoredProfile(t)

	for name, void := range map[string]func(*evaluation.ScenarioResult){
		"errored before scoring": func(r *evaluation.ScenarioResult) {
			r.Status, r.Passed, r.Errors = "", false, []string{"provision: 502"}
		},
		"provider failure": func(r *evaluation.ScenarioResult) { r.Status, r.Passed = evaluation.ScenarioProviderFailure, false },
		"agent failure":    func(r *evaluation.ScenarioResult) { r.Status, r.Passed = evaluation.ScenarioUnevaluable, false },
		"unassessable": func(r *evaluation.ScenarioResult) {
			r.Status, r.Passed, r.Unassessable = evaluation.ScenarioFail, false, true
		},
	} {
		t.Run(name, func(t *testing.T) {
			results := runResults(p, readOnly, nil)
			for i := range results {
				if results[i].Category == diagnosticAccuracy {
					void(&results[i])
				}
			}
			cm := execution.ComputeCoverage(p, p.Scenarios, readOnly, 1, results)

			assert.False(t, cm.MinimumCoverageMet)
			da := categoryByID(t, cm, diagnosticAccuracy)
			assert.Equal(t, evaluation.CoverageUnmet, da.Status)
			assert.Empty(t, da.ArchetypesEvaluated)
			for _, sk := range da.ArchetypesSkipped {
				assert.Equal(t, evaluation.SkipNotEvaluated, sk.Reason, sk.Archetype)
				assert.NotEmpty(t, sk.Detail, sk.Archetype)
			}
		})
	}
}

// Tier 2 states a requirement that is not an archetype count. The evaluator
// cannot compute it, so a tier 2 claim is never reported as having met minimum
// coverage, and the requirement is named.
func TestCoverage_SI_TierWithAnUncomputedRequirementIsNeverMet(t *testing.T) {
	p := loadVendoredProfile(t)
	readWrite := evaluation.AgentConfiguration{evaluation.OperationalModeDimension: "read_write"}
	require.NotEmpty(t, p.TierRequirements[2].Uncomputed)

	cm := execution.ComputeCoverage(p, p.Scenarios, readWrite, 2, runResults(p, readWrite, nil))

	assert.False(t, cm.MinimumCoverageMet)
	var named bool
	for _, u := range cm.Unmet {
		if strings.Contains(u, p.TierRequirements[2].Uncomputed[0]) && strings.Contains(u, "does not compute") {
			named = true
		}
	}
	assert.True(t, named, "the uncomputed requirement must be named: %v", cm.Unmet)
}

// A filtered run must read as uncovered, not as excluded: applicability is
// decided from the profile's suite, not from what the run was handed.
func TestCoverage_SI_ExclusionIsDecidedFromTheProfileSuite(t *testing.T) {
	p := loadVendoredProfile(t)

	// The run was handed only the safety scenarios. Against that subset no
	// capability archetype has a scenario at all.
	var safety []evaluation.Scenario
	for _, s := range p.Scenarios {
		if s.Classification == evaluation.ClassificationSafety {
			safety = append(safety, s)
		}
	}
	cm := execution.ComputeCoverage(p, safety, readOnly, 1, nil)

	oe := categoryByID(t, cm, operationalExecution)
	assert.Equal(t, evaluation.CoverageUnmet, oe.Status,
		"a category with no scenario in hand is a gap, never a configuration exclusion")
	for _, sk := range oe.ArchetypesSkipped {
		assert.Equal(t, evaluation.SkipNoScenario, sk.Reason, sk.Archetype)
	}
}
