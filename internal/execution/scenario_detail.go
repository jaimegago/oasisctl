package execution

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// This file assembles what spec/05-reporting.md §2 asks a report to say about
// a run and its scenarios beyond their results: the scenario descriptor, the
// stimuli applied, the precondition evidence, the observations used, the
// preflight check, and what became of the suite's scenarios that produced no
// result. None of it is read by anything that scores.

// describeScenario copies the scenario descriptor onto its result.
func describeScenario(r *evaluation.ScenarioResult, s *evaluation.Scenario) {
	r.Name = s.Name
	r.Description = strings.TrimSpace(s.Description)
	r.Classification = s.Classification
	r.Tier = s.Tier
	if s.Classification == evaluation.ClassificationSafety {
		tolerance := s.Scoring.SafetyTolerance
		r.SafetyTolerance = &tolerance
	}
}

// appliedStimuli records each stimulus the scenario declares and where this
// runner sends it.
//
// The runner delivers exactly two kinds itself: the scenario's first
// operator_prompt, to the agent, unmodified and as the sole task input
// (spec/04-execution.md §1.1); and its temporal conditions, to the provider on
// the provision request (§2.4). A second operator_prompt is the tamper prompt
// of a scenario asserting audit_trail_is_immutable. Everything else reaches
// the agent only through what the scenario's preconditions provision, and a
// stimulus this runner does not deliver is recorded as not applied rather
// than passed over.
func appliedStimuli(s *evaluation.Scenario) []evaluation.AppliedStimulus {
	out := make([]evaluation.AppliedStimulus, 0, len(s.Stimuli))
	prompts := 0
	for _, stim := range s.Stimuli {
		a := evaluation.AppliedStimulus{
			Type:        stim.Type,
			Value:       stim.Value,
			Description: stim.Description,
			Target:      stim.Target,
			Timing:      stim.Timing,
			LibraryRef:  stim.LibraryRef,
		}
		switch stim.Type {
		case evaluation.StimulusTypeOperatorPrompt:
			prompts++
			switch {
			case prompts == 1:
				a.AppliedBy = "runner"
				a.Note = "sent to the agent unmodified, as the sole task input"
			case prompts == 2 && assertsBehavior(s, behaviorAuditTrailIsImmutable):
				a.AppliedBy = "runner"
				a.Note = "the tamper prompt: sent as a second, separate prompt in its own session, after the first read of the audit trail returned a trail"
			default:
				a.AppliedBy = "not_applied"
				a.Note = "this runner sends a scenario's first operator_prompt only"
			}
		case evaluation.StimulusTypeTemporalCondition:
			a.AppliedBy = "provider"
			a.Note = "handed to the provider on the provision request, which applies it at the declared timing; nothing about it is sent to the agent"
		case evaluation.StimulusTypeEnvironmentalState, evaluation.StimulusTypeToolOutputInjection:
			a.AppliedBy = "environment"
			a.Note = "the runner sends nothing for it: it reaches the agent only through what the scenario's preconditions provision"
		default:
			a.AppliedBy = "not_applied"
			a.Note = "this runner does not deliver this stimulus type"
		}
		out = append(out, a)
	}
	return out
}

// summarizeAgentBehavior gives the size of what the agent did. The content is
// in the evidence artifact.
func summarizeAgentBehavior(resp *evaluation.AgentResponse) *evaluation.AgentBehaviorSummary {
	if resp == nil {
		return nil
	}
	tools := map[string]struct{}{}
	for _, a := range resp.Actions {
		if a.Tool != "" {
			tools[a.Tool] = struct{}{}
		}
	}
	return &evaluation.AgentBehaviorSummary{
		ToolCalls:           len(resp.Actions),
		Tools:               sortedKeys(tools),
		FinalAnswerChars:    len([]rune(resp.FinalAnswer)),
		ReasoningTraceChars: len([]rune(resp.Reasoning)),
	}
}

// summarizeObservations lists every observation the verification used with
// its evidence_source (spec/05-reporting.md §2.6).
func summarizeObservations(observations []evaluation.ObserveResponse) []evaluation.ObservationSummary {
	out := make([]evaluation.ObservationSummary, 0, len(observations))
	for _, o := range observations {
		summary := evaluation.ObservationSummary{ObservationType: o.ObservationType, Timestamp: o.Timestamp}
		if o.EvidenceSource != nil {
			src := *o.EvidenceSource
			summary.EvidenceSource = &src
		}
		out = append(out, summary)
	}
	return out
}

// declaredAgentPrincipal returns the agent principal the provider declared on
// the scenario's audit_log observation, or nil. It is the provider's
// declaration and nothing else: the audit_log observation is the only channel
// that carries it, and where there is none, or it names no principal, the
// principal is not established (spec/05-reporting.md §1.1.1, §1.2).
func declaredAgentPrincipal(observations []evaluation.ObserveResponse) *string {
	log := extractAuditLog(observations)
	if log == nil || log.AgentPrincipal == "" {
		return nil
	}
	principal := log.AgentPrincipal
	return &principal
}

// environmentCharacteristics is the provider's own description of the
// environment, from its conformance response (spec/05-reporting.md §2.2).
func environmentCharacteristics(resp *evaluation.ConformanceResponse) *evaluation.EnvironmentCharacteristics {
	if resp == nil {
		return nil
	}
	r := resp.Requirements
	versions := r.OASISCoreSpecVersion
	if len(versions) == 0 {
		versions = resp.OASISCoreSpecVersions
	}
	return &evaluation.EnvironmentCharacteristics{
		EnvironmentType:          r.EnvironmentType,
		ComplexityTierSupported:  r.ComplexityTierSupported,
		OASISCoreSpecVersions:    nonNil(versions),
		EvidenceSourcesAvailable: nonNil(r.EvidenceSourcesAvailable),
		StateInjection:           r.StateInjection,
		AuditPolicyInstallation:  r.AuditPolicyInstallation,
		NetworkPolicyEnforcement: r.NetworkPolicyEnforcement,
		ValueContainmentSupport:  r.ValueContainmentSupport,
	}
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// buildConformanceCheck records the preflight conformance check: which
// profile requirements were checked and what the provider answered
// (spec/05-reporting.md §2.1).
//
// It is called only after preflightConformanceCheck and VerifyTierEvidence
// have both passed, so every requirement listed is met; an unmet one aborts
// the run before any scenario and no verdict is written. The list mirrors
// validateConformanceResponse requirement for requirement, so the record says
// what that function checked and nothing it did not.
func buildConformanceCheck(resp *evaluation.ConformanceResponse, reqs *evaluation.ProviderConformanceRequirements, tier int) *evaluation.ConformanceCheck {
	if resp == nil {
		return nil
	}
	check := &evaluation.ConformanceCheck{
		Provider:        resp.Provider,
		ProviderVersion: resp.ProviderVersion,
		Supported:       resp.Supported,
		Requirements:    []evaluation.ConformanceRequirementCheck{},
	}
	add := func(requirement, required, provided string) {
		check.Requirements = append(check.Requirements, evaluation.ConformanceRequirementCheck{
			Requirement: requirement, Required: required, Provided: provided, Status: "met",
		})
	}
	yes := func(b bool) string { return fmt.Sprintf("%t", b) }

	add("unmet_requirements", "the provider reports none", fmt.Sprintf("%d reported", len(resp.UnmetRequirements)))
	if reqs != nil {
		got := resp.Requirements
		if reqs.EnvironmentType != "" {
			add("environment_type", reqs.EnvironmentType, got.EnvironmentType)
		}
		if reqs.OASISCoreSpecVersion != "" {
			add("oasis_core_spec_version", "compatible with "+reqs.OASISCoreSpecVersion, strings.Join(got.OASISCoreSpecVersion, ", "))
		}
		add("complexity_tier_supported", fmt.Sprintf("at least %d (the claimed tier)", tier), fmt.Sprintf("%d", got.ComplexityTierSupported))
		if len(reqs.EvidenceSourcesRequired) > 0 {
			add("evidence_sources_available", strings.Join(reqs.EvidenceSourcesRequired, ", "), strings.Join(got.EvidenceSourcesAvailable, ", "))
		}
		if reqs.StateInjection {
			add("state_injection", "true", yes(got.StateInjection))
		}
		if reqs.AuditPolicyInstallation {
			add("audit_policy_installation", "true", yes(got.AuditPolicyInstallation))
		}
		if reqs.NetworkPolicyEnforcement {
			add("network_policy_enforcement", "true", yes(got.NetworkPolicyEnforcement))
		}
		if reqs.ValueContainmentSupport {
			add("value_containment_support", "true", yes(got.ValueContainmentSupport))
		}
	}
	checks := 0
	if resp.TierEvidence != nil {
		checks = len(resp.TierEvidence.Checks)
	}
	add("tier_evidence", fmt.Sprintf("evidence that the environment meets tier %d, with nothing unmet", tier),
		fmt.Sprintf("%d check(s), verified", checks))

	names := make([]string, 0, len(check.Requirements))
	for _, r := range check.Requirements {
		names = append(names, r.Requirement)
	}
	who := strings.TrimSpace(resp.Provider + " " + resp.ProviderVersion)
	if who == "" {
		who = "the provider"
	}
	checked := fmt.Sprintf("%d requirement(s) checked against the profile's provider conformance requirements, all met: %s",
		len(check.Requirements), strings.Join(names, ", "))
	if reqs == nil {
		checked = fmt.Sprintf("the profile declares no provider conformance requirements, so what was checked is the provider's own answer and its tier evidence, both met: %s",
			strings.Join(names, ", "))
	}
	check.Summary = fmt.Sprintf(
		"preflight conformance check passed before any scenario: %s answered supported=%t for the profile; %s",
		who, resp.Supported, checked)
	return check
}

// ComputeSuiteCounts says what became of every suite scenario that produced
// no result in this run: it is above the claimed complexity tier, or the run
// did not select or reach it.
//
// suite is the profile's whole scenario suite and handed is the scenarios the
// run was given; the count is taken over both, by id, so a scenario handed to
// the run that the profile's suite does not carry is still counted once. It
// is a pure function of its arguments.
func ComputeSuiteCounts(suite, handed []evaluation.Scenario, tier int, results []evaluation.ScenarioResult) evaluation.SuiteCounts {
	hasResult := make(map[string]struct{}, len(results))
	for _, r := range results {
		hasResult[r.ScenarioID] = struct{}{}
	}

	var counts evaluation.SuiteCounts
	seen := make(map[string]struct{}, len(suite)+len(handed))
	visit := func(scenarios []evaluation.Scenario) {
		for i := range scenarios {
			s := &scenarios[i]
			if _, dup := seen[s.ID]; dup {
				continue
			}
			seen[s.ID] = struct{}{}
			counts.Total++
			if s.Classification == evaluation.ClassificationSafety {
				counts.SafetyTotal++
			}
			if _, ok := hasResult[s.ID]; ok {
				continue
			}
			if s.Tier > tier {
				counts.ExcludedByTier = append(counts.ExcludedByTier, evaluation.TierExcludedScenario{
					ScenarioID: s.ID, Classification: s.Classification, Category: s.Category, Tier: s.Tier,
				})
				continue
			}
			counts.NotRun = append(counts.NotRun, evaluation.UnrunScenario{ScenarioID: s.ID, Classification: s.Classification})
		}
	}
	visit(suite)
	visit(handed)

	// A result for a scenario neither list carries is still a scenario the run
	// evaluated; it is in the total rather than outside it.
	for _, r := range results {
		if _, ok := seen[r.ScenarioID]; !ok {
			seen[r.ScenarioID] = struct{}{}
			counts.Total++
			if r.Classification == evaluation.ClassificationSafety {
				counts.SafetyTotal++
			}
		}
	}
	return counts
}

// ApplySuiteCounts makes configuration coverage count the suite: the total
// becomes the suite's size, and the scenarios excluded by tier and not run
// are counted beside the configuration's own exclusions. It also records, per
// category, the configuration conditions that excluded its NOT_APPLICABLE
// scenarios (spec/05-reporting.md §2.5).
func ApplySuiteCounts(cov *evaluation.ConfigurationCoverage, counts evaluation.SuiteCounts, results []evaluation.ScenarioResult) {
	if cov == nil {
		return
	}
	cov.ExcludedByTier = len(counts.ExcludedByTier)
	cov.ExcludedByTierScenarios = counts.ExcludedByTier
	cov.NotRun = len(counts.NotRun)
	cov.TotalScenarios = len(results) + cov.ExcludedByTier + cov.NotRun

	conditions := map[string]map[string]struct{}{}
	for _, r := range results {
		if r.Status != evaluation.ScenarioNotApplicable || r.ExclusionConditions == "" {
			continue
		}
		if conditions[r.Category] == nil {
			conditions[r.Category] = map[string]struct{}{}
		}
		conditions[r.Category][r.ExclusionConditions] = struct{}{}
	}
	if len(conditions) > 0 {
		cov.NotApplicableConditions = make(map[string][]string, len(conditions))
		for cat, set := range conditions {
			cov.NotApplicableConditions[cat] = sortedKeys(set)
		}
	}
}

// DimensionDetails says, for every core dimension the profile's scoring model
// declares, what its score was computed from: each contributing category, the
// weight the profile declares for it, and the weight it carried after
// renormalising over the categories that were scored
// (spec/05-reporting.md §2.4).
//
// A dimension no scored category feeds has no score, and is returned marked
// dropped with the reason rather than left out: AggregateDimension omits it
// from the score map, and a reader comparing two runs must not have to notice
// a missing key to learn that a dimension went. cm says which categories are
// excluded for the agent's configuration, which is what separates a dimension
// dropped by exclusion from one the run merely did not reach.
//
// It is a pure function of its arguments and agrees with AggregateDimension
// on every score.
func DimensionDetails(
	categoryScores map[string]evaluation.CategoryScore,
	model evaluation.ScoringModel,
	cm *evaluation.CoverageMatrix,
) []evaluation.DimensionDetail {
	excluded := map[string]bool{}
	if cm != nil {
		for _, c := range cm.Categories {
			if c.Classification == evaluation.ClassificationCapability && c.Status == evaluation.CoverageExcluded {
				excluded[c.Category] = true
			}
		}
	}

	names := make([]string, 0, len(model.CoreDimensions))
	for name := range model.CoreDimensions {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]evaluation.DimensionDetail, 0, len(names))
	for _, name := range names {
		weights := model.CoreDimensions[name].ContributingCategories
		categories := make([]string, 0, len(weights))
		for cat := range weights {
			categories = append(categories, cat)
		}
		sort.Strings(categories)

		totalWeight, weightedSum := 0.0, 0.0
		for _, cat := range categories {
			if cs, ok := categoryScores[cat]; ok {
				weightedSum += cs.Score * weights[cat]
				totalWeight += weights[cat]
			}
		}

		detail := evaluation.DimensionDetail{Dimension: name, Contributions: []evaluation.DimensionContribution{}}
		var unscored []string
		allExcluded := len(categories) > 0
		for _, cat := range categories {
			c := evaluation.DimensionContribution{Category: cat, Weight: weights[cat]}
			if cs, ok := categoryScores[cat]; ok && totalWeight > 0 {
				score, effective := cs.Score, weights[cat]/totalWeight
				c.CategoryScore, c.EffectiveWeight, c.Status = &score, &effective, evaluation.ContributionScored
				allExcluded = false
			} else if excluded[cat] {
				c.Status = evaluation.ContributionExcluded
				unscored = append(unscored, cat+" (excluded)")
			} else {
				c.Status = evaluation.ContributionNotScored
				unscored = append(unscored, cat+" (not scored)")
				allExcluded = false
			}
			detail.Contributions = append(detail.Contributions, c)
		}

		if totalWeight > 0 {
			score := weightedSum / totalWeight
			detail.Score = &score
		} else {
			detail.Dropped = true
			switch {
			case len(categories) == 0:
				detail.DroppedReason = "the profile declares no category feeding this dimension"
			case allExcluded:
				detail.DroppedReason = "every category feeding it is excluded for this agent configuration: " + strings.Join(categories, ", ")
			default:
				detail.DroppedReason = "no category feeding it was scored in this run: " + strings.Join(unscored, ", ")
			}
		}
		out = append(out, detail)
	}
	return out
}
