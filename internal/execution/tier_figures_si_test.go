package execution_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/execution"
)

// The scenario-counted figures over the vendored Software Infrastructure
// profile, loaded from disk: its §8 figures and the preconditions its own
// scenarios declare. Nothing about the profile is a literal here.

// siScenariosRun is the scenarios a whole-profile run executes at a tier under
// a configuration: at or below the tier, and applicable.
func siScenariosRun(p *evaluation.Profile, config evaluation.AgentConfiguration, tier int) []evaluation.Scenario {
	var out []evaluation.Scenario
	for i := range p.Scenarios {
		if p.Scenarios[i].Tier <= tier && execution.IsApplicable(&p.Scenarios[i], config) {
			out = append(out, p.Scenarios[i])
		}
	}
	return out
}

// A read-only agent evaluated across the SI profile at Tier 1 has all three
// figures judged, and met. This is the half of the tier evidence the
// 2026-11-19 run left not assessed (joe-pm threads/si-ro-rerun.md).
func TestScenarioFigures_SIReadOnlyTierOne_AllThreeJudgedAndMet(t *testing.T) {
	p := loadVendoredProfile(t)
	run := siScenariosRun(p, readOnly, 1)
	require.NotEmpty(t, run)

	checks := execution.JudgeScenarioFigures(p.TierRequirements[1], 1, run)
	require.Len(t, checks, 3)
	names := []string{}
	for _, c := range checks {
		names = append(names, c.Requirement)
		assert.Equal(t, evaluation.TierCheckMet, c.Status, "%s: %s (required: %s)", c.Requirement, c.Observed, c.Required)
		assert.NotEmpty(t, c.Observed, "%s must say what was counted", c.Requirement)
		assert.NotEmpty(t, c.Required, "%s must carry the profile's figure", c.Requirement)
	}
	assert.Equal(t, []string{evaluation.TierFigureNamespaces, evaluation.TierFigureMicroservices, evaluation.TierFigureSecurityZones}, names)
}

// The figures are judged from the scenarios the evaluation runs, so a run that
// selects scenarios declaring one zone between them does not meet a figure the
// whole suite would.
func TestScenarioFigures_SI_AreJudgedFromTheScenariosRun(t *testing.T) {
	p := loadVendoredProfile(t)

	var oneZone []evaluation.Scenario
	for _, s := range siScenariosRun(p, readOnly, 1) {
		zones := map[string]bool{}
		for _, entry := range s.Preconditions.Environment.State {
			if z, _ := entry["zone"].(string); z != "" {
				zones[z] = true
			}
		}
		if len(zones) == 0 {
			oneZone = append(oneZone, s)
		}
	}
	require.NotEmpty(t, oneZone, "the suite holds scenarios that declare no zone")

	for _, c := range execution.JudgeScenarioFigures(p.TierRequirements[1], 1, oneZone) {
		if c.Requirement == evaluation.TierFigureSecurityZones {
			assert.Equal(t, evaluation.TierCheckUnmet, c.Status)
			assert.Contains(t, c.Observed, "0 distinct declared")
		}
	}
}
