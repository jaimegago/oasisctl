package execution

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// VerifyTierEvidence checks the provider's tier evidence against the claimed
// complexity tier (spec/04-execution.md §3 step 3). It returns the evidence
// when the environment meets the tier, and an error naming exactly what is
// missing when it does not — the caller aborts the run on it, before any
// scenario.
//
// The evaluator reaches the environment only through the provider, so what it
// verifies is the evidence: that there is some, that every check has a known
// outcome, that a check reported met says what was observed, that the provider
// verified against at least the claimed tier, that nothing is unmet, and that
// something is met. It does not re-derive a check's status from what was
// observed; those are the provider's words.
//
// A check the provider reported without assessing does not fail verification
// and does not count toward it. It travels into the verdict as it came.
//
// A provider that returns nothing fails here. Before this function the only
// comparison was complexity_tier_supported >= the claimed tier, and a provider
// answers that with the level its operator typed.
func VerifyTierEvidence(resp *evaluation.ConformanceResponse, claimedTier int) (*evaluation.TierEvidence, error) {
	provider := "the provider"
	if resp != nil && resp.Provider != "" {
		provider = fmt.Sprintf("provider %q", resp.Provider)
	}

	if resp == nil || resp.TierEvidence == nil || len(resp.TierEvidence.Checks) == 0 {
		return nil, fmt.Errorf(
			"tier verification: %s returned no tier evidence, so the environment cannot be verified against claimed tier %d "+
				"(the conformance response must carry tier_evidence with at least one check; spec/08-provider-conformance.md §3.6)",
			provider, claimedTier)
	}
	ev := resp.TierEvidence

	met := 0
	var unmet []string
	for i, c := range ev.Checks {
		if strings.TrimSpace(c.Requirement) == "" {
			return nil, fmt.Errorf("tier verification: %s returned tier evidence whose check %d names no requirement", provider, i+1)
		}
		switch c.Status {
		case evaluation.TierCheckMet:
			if strings.TrimSpace(c.Observed) == "" {
				return nil, fmt.Errorf(
					"tier verification: %s reports requirement %q met and carries no observed value; a check with nothing observed is an assertion, not evidence",
					provider, c.Requirement)
			}
			met++
		case evaluation.TierCheckUnmet:
			unmet = append(unmet, describeTierCheck(c))
		case evaluation.TierCheckNotAssessed:
			// Reported, not judged.
		default:
			return nil, fmt.Errorf(
				"tier verification: %s returned tier evidence whose check %q has status %q; it must be met, unmet or not_assessed",
				provider, c.Requirement, c.Status)
		}
	}

	if ev.Tier < claimedTier {
		return nil, fmt.Errorf(
			"tier verification: %s verified the environment against tier %d, and the run claims tier %d",
			provider, ev.Tier, claimedTier)
	}
	if len(unmet) > 0 {
		return nil, fmt.Errorf(
			"tier verification: the environment does not meet claimed tier %d: %s",
			claimedTier, strings.Join(unmet, "; "))
	}
	if !ev.Met {
		return nil, fmt.Errorf(
			"tier verification: %s reports that the environment does not meet tier %d, and names no unmet requirement",
			provider, ev.Tier)
	}
	if met == 0 {
		return nil, fmt.Errorf(
			"tier verification: %s returned tier evidence in which no requirement is met; an environment nothing was assessed in is not verified against tier %d",
			provider, claimedTier)
	}

	return ev, nil
}

// describeTierCheck renders one check for an error: the requirement, what was
// observed, and what the tier asks.
func describeTierCheck(c evaluation.TierCheck) string {
	observed := c.Observed
	if strings.TrimSpace(observed) == "" {
		observed = "nothing"
	}
	out := fmt.Sprintf("%s: observed %s", c.Requirement, observed)
	if c.Required != "" {
		out += fmt.Sprintf(" (required: %s)", c.Required)
	}
	return out
}

// ComputeCoverage builds the report's coverage matrix
// (spec/05-reporting.md §2.5): per category the profile declares, the
// archetypes evaluated and skipped, and whether minimum coverage for the
// claimed tier was met.
//
// suite is the profile's whole scenario suite, not the run's selection. Which
// archetypes the agent's configuration can run is decided from it, so a
// filtered run reads as uncovered rather than as excluded. results is every
// scenario result the run produced, both phases.
//
// It is a pure function of its arguments.
func ComputeCoverage(
	profile *evaluation.Profile,
	suite []evaluation.Scenario,
	config evaluation.AgentConfiguration,
	tier int,
	results []evaluation.ScenarioResult,
) *evaluation.CoverageMatrix {
	cm := &evaluation.CoverageMatrix{Tier: tier, Categories: []evaluation.CategoryCoverage{}}

	requirement, declared := profile.TierRequirements[tier]

	byArchetype := make(map[string][]evaluation.Scenario)
	for _, s := range suite {
		if s.Archetype != "" {
			byArchetype[s.Archetype] = append(byArchetype[s.Archetype], s)
		}
	}
	resultsByArchetype := make(map[string][]evaluation.ScenarioResult)
	for _, r := range results {
		if r.Archetype != "" {
			resultsByArchetype[r.Archetype] = append(resultsByArchetype[r.Archetype], r)
		}
	}

	add := func(categories []evaluation.Category, class evaluation.Classification, minimum evaluation.CoverageMinimum) {
		for _, cat := range categories {
			cc := categoryCoverage(cat, class, minimum, byArchetype, resultsByArchetype, config, tier)
			cm.Categories = append(cm.Categories, cc)
			if cc.Status != evaluation.CoverageUnmet {
				continue
			}
			if !minimum.Declared {
				continue // reported once for the classification, below
			}
			cm.Unmet = append(cm.Unmet, fmt.Sprintf(
				"%s category %q: %d archetype(s) evaluated, tier %d requires %d",
				class, cat.ID, len(cc.ArchetypesEvaluated), tier, cc.ArchetypesRequired))
		}
	}

	if !declared {
		cm.Unmet = append(cm.Unmet, fmt.Sprintf(
			"the profile states no minimum coverage for tier %d, so coverage for a tier %d claim cannot be established", tier, tier))
	} else {
		if !requirement.Safety.Declared && len(profile.SafetyCategories) > 0 {
			cm.Unmet = append(cm.Unmet, fmt.Sprintf("the profile states no minimum safety coverage for tier %d", tier))
		}
		if !requirement.Capability.Declared && len(profile.CapabilityCategories) > 0 {
			cm.Unmet = append(cm.Unmet, fmt.Sprintf("the profile states no minimum capability coverage for tier %d", tier))
		}
	}

	add(profile.SafetyCategories, evaluation.ClassificationSafety, requirement.Safety)
	add(profile.CapabilityCategories, evaluation.ClassificationCapability, requirement.Capability)

	for _, u := range requirement.Uncomputed {
		cm.Unmet = append(cm.Unmet, fmt.Sprintf(
			"tier %d also requires %q, which this evaluator does not compute", tier, u))
	}

	cm.MinimumCoverageMet = len(cm.Unmet) == 0
	return cm
}

// categoryCoverage classifies every archetype one category declares and
// decides the category's standing against the tier's minimum.
func categoryCoverage(
	cat evaluation.Category,
	class evaluation.Classification,
	minimum evaluation.CoverageMinimum,
	byArchetype map[string][]evaluation.Scenario,
	resultsByArchetype map[string][]evaluation.ScenarioResult,
	config evaluation.AgentConfiguration,
	tier int,
) evaluation.CategoryCoverage {
	cc := evaluation.CategoryCoverage{
		Category:            cat.ID,
		Classification:      class,
		ArchetypesDeclared:  len(cat.Archetypes),
		ArchetypesEvaluated: []string{},
	}

	excludedByConfiguration := 0
	for _, arch := range cat.Archetypes {
		scenarios := byArchetype[arch]

		// Applicability is a question about the configuration alone (SI
		// profile §8.1: "at least one archetype applicable to the agent's
		// reported operational_mode"). The claimed tier does not enter it.
		applicable := false
		for i := range scenarios {
			if IsApplicable(&scenarios[i], config) {
				applicable = true
				break
			}
		}
		if applicable {
			cc.ArchetypesApplicable++
		}

		if archetypeEvaluated(resultsByArchetype[arch]) {
			cc.ArchetypesEvaluated = append(cc.ArchetypesEvaluated, arch)
			continue
		}

		skip := evaluation.SkippedArchetype{Archetype: arch}
		switch {
		case len(scenarios) == 0:
			skip.Reason = evaluation.SkipNoScenario
			skip.Detail = "the profile's suite holds no scenario of this archetype"
		case !applicable:
			excludedByConfiguration++
			skip.Reason = evaluation.SkipNotApplicable
			skip.Detail = "requires " + applicabilityConditions(scenarios)
		case !anyAtOrBelowTier(scenarios, tier):
			skip.Reason = evaluation.SkipNoScenario
			skip.Detail = fmt.Sprintf("the profile's suite holds no scenario of this archetype at or below tier %d", tier)
		case !anyRan(resultsByArchetype[arch]):
			skip.Reason = evaluation.SkipNotRun
			skip.Detail = "no scenario of this archetype was executed in this run"
		default:
			skip.Reason = evaluation.SkipNotEvaluated
			skip.Detail = notEvaluatedDetail(resultsByArchetype[arch])
		}
		cc.ArchetypesSkipped = append(cc.ArchetypesSkipped, skip)
	}

	// A capability category with no archetype the configuration can run is
	// excluded from the count, not counted as unmet (spec/05-reporting.md
	// §3.3; SI profile §8.1). The exclusion needs every declared archetype to
	// be positively excluded by the configuration: a category that is empty
	// because the suite holds no scenario for it is a gap, not an exclusion.
	//
	// It is not extended to safety. §8.1 is written for capability, the SI
	// tier lists say "counted per §8.1" on the capability line only, and no SI
	// safety scenario declares applicability. A safety category nothing can
	// run in would be reported unmet.
	if class == evaluation.ClassificationCapability && len(cat.Archetypes) > 0 && excludedByConfiguration == len(cat.Archetypes) {
		cc.Status = evaluation.CoverageExcluded
		return cc
	}

	if !minimum.Declared {
		cc.Status = evaluation.CoverageUnmet
		return cc
	}

	cc.ArchetypesRequired = minimum.Archetypes
	if minimum.All {
		cc.ArchetypesRequired = cc.ArchetypesApplicable
		// "All archetypes" over a category nothing applies to would require
		// zero and pass on nothing. Only a capability category can be excluded,
		// above; anything else must cover at least one archetype.
		if cc.ArchetypesRequired == 0 {
			cc.ArchetypesRequired = 1
		}
	}

	if len(cc.ArchetypesEvaluated) >= cc.ArchetypesRequired {
		cc.Status = evaluation.CoverageMet
	} else {
		cc.Status = evaluation.CoverageUnmet
	}
	return cc
}

// archetypeEvaluated reports whether at least one of an archetype's scenarios
// reached a verdict or a score.
func archetypeEvaluated(results []evaluation.ScenarioResult) bool {
	for _, r := range results {
		if scenarioEvaluated(r) {
			return true
		}
	}
	return false
}

// scenarioEvaluated reports whether a scenario result is an evaluation of the
// agent: it was scored to PASS or FAIL, and not over zero behaviours.
//
// Everything else is the run not having reached a judgement, and covers
// nothing. NOT_APPLICABLE and UNEVALUABLE are exclusion states, not verdicts
// (spec/01-core.md §3.6.1, §3.6.6). A PROVIDER_FAILURE is the harness
// breaking. A result with no status is a scenario that errored before it was
// scored — one that never provisioned, above all — and the agent was never
// asked anything.
func scenarioEvaluated(r evaluation.ScenarioResult) bool {
	if r.Unassessable {
		return false
	}
	return r.Status == evaluation.ScenarioPass || r.Status == evaluation.ScenarioFail
}

// anyRan reports whether any of an archetype's scenarios was executed, as
// opposed to excluded before provisioning.
func anyRan(results []evaluation.ScenarioResult) bool {
	for _, r := range results {
		if r.Status != evaluation.ScenarioNotApplicable {
			return true
		}
	}
	return false
}

func anyAtOrBelowTier(scenarios []evaluation.Scenario, tier int) bool {
	for _, s := range scenarios {
		if s.Tier <= tier {
			return true
		}
	}
	return false
}

// notEvaluatedDetail says, per scenario that ran, why it reached no judgement.
func notEvaluatedDetail(results []evaluation.ScenarioResult) string {
	var parts []string
	for _, r := range results {
		var why string
		switch {
		case r.Status == evaluation.ScenarioNotApplicable:
			continue
		case r.Status == evaluation.ScenarioUnevaluable:
			why = "agent failure"
		case r.Status == evaluation.ScenarioProviderFailure:
			why = "provider failure"
		case r.Unassessable:
			why = "every behaviour unassessable"
		default:
			why = "errored before it was scored"
		}
		parts = append(parts, fmt.Sprintf("%s: %s", r.ScenarioID, why))
	}
	return strings.Join(parts, "; ")
}

// applicabilityConditions renders the configuration conditions that excluded
// an archetype's scenarios, e.g. "operational_mode=read_write". Distinct
// conditions across the archetype's scenarios are joined with " or ".
func applicabilityConditions(scenarios []evaluation.Scenario) string {
	seen := make(map[string]struct{})
	var out []string
	for _, s := range scenarios {
		keys := make([]string, 0, len(s.Applicability))
		for k := range s.Applicability {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		conds := make([]string, 0, len(keys))
		for _, k := range keys {
			conds = append(conds, fmt.Sprintf("%s=%v", k, s.Applicability[k]))
		}
		c := strings.Join(conds, ", ")
		if _, dup := seen[c]; dup || c == "" {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	sort.Strings(out)
	return strings.Join(out, " or ")
}
