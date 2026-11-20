package execution

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// buildStandardVerdict assembles the verdict in the format of
// spec/05-reporting.md §1 from the assembled evaluation and the report built
// from it. It reads counts from the report's summaries rather than
// recounting, so the verdict and the report published beside it cannot
// disagree. It is called only for an evaluation: executions made outside one
// have no verdict to emit.
func buildStandardVerdict(v *evaluation.Verdict, r *evaluation.Report) *evaluation.StandardVerdict {
	ev := v.Evaluation
	opening := ev.Opening
	id := opening.Identity
	ss := r.SafetySummary

	safetyExcludedByTier := 0
	for _, s := range opening.ExcludedByTier {
		if s.Classification == evaluation.ClassificationSafety {
			safetyExcludedByTier++
		}
	}

	details := evaluation.VerdictSafetyDetails{
		ExecutionsPerScenario: opening.ExecutionCounts.Safety,
		TotalScenarios:        ss.Applicable + ss.NotApplicable + safetyExcludedByTier,
		Applicable:            ss.Applicable,
		NotApplicable:         ss.NotApplicable,
		Passed:                ss.PassedCount,
		Failed:                ss.Failed,
		ShortOfCount:          ss.ShortOfCount,
		CountedExecutions:     ss.CountedExecutions,
		Attempts:              ss.Attempts,
		ToleranceAdjusted:     ss.ToleranceAdjusted,
		Failures:              []evaluation.VerdictFailure{},
		VerdictRecord:         ev.SafetyVerdictRecord,
		VerdictRecordHash:     ev.SafetyVerdictHash,
		Containment:           ss.Containment,
		HumanReview:           ss.HumanReview,
		ExcludedByTier:        safetyExcludedByTier,
	}
	if details.HumanReview == nil {
		details.HumanReview = []evaluation.HumanReviewEntry{}
	}
	for _, sr := range v.SafetyResults {
		if sr.Status == evaluation.ScenarioFail {
			details.Failures = append(details.Failures, evaluation.VerdictFailure{
				ScenarioID: sr.ScenarioID, Description: failureDescription(sr),
			})
		}
	}
	if len(ss.SubcategoryResults) > 0 {
		details.SubcategoryResults = map[string]map[string]evaluation.VerdictSubcategoryResult{}
		for name, sub := range ss.SubcategoryResults {
			if details.SubcategoryResults[sub.Category] == nil {
				details.SubcategoryResults[sub.Category] = map[string]evaluation.VerdictSubcategoryResult{}
			}
			details.SubcategoryResults[sub.Category][name] = evaluation.VerdictSubcategoryResult{
				Total: sub.Total, Passed: sub.Passed, Failed: sub.Failed, ShortOfCount: sub.ShortOfCount,
			}
		}
	}

	out := &evaluation.StandardVerdict{
		Evaluation:    opening.Evaluation,
		Complete:      ev.Complete,
		Safety:        v.Safety,
		SafetyDetails: details,
		Scenarios:     []evaluation.ScenarioRecord{},
	}
	out.Scenarios = append(out.Scenarios, ev.SafetyRecords...)
	out.Scenarios = append(out.Scenarios, ev.CapabilityRecords...)

	// `capability` is present only if safety = PASS.
	if v.Safety == evaluation.SafetyVerdictPass {
		out.Capability = buildVerdictCapability(v, r)
	}

	labs := []evaluation.VerdictLab{}
	for _, lab := range ev.Labs {
		entry := evaluation.VerdictLab{Lab: lab.Lab, CreatedAt: lab.CreatedAt, TierEvidence: lab.TierEvidenceSummary}
		if lab.ConformanceCheck != nil {
			entry.ConformanceCheck = lab.ConformanceCheck.Summary
		}
		labs = append(labs, entry)
	}

	agentConfiguration := id.AgentConfiguration
	if agentConfiguration == nil {
		agentConfiguration = evaluation.AgentConfiguration{}
	}
	out.Metadata = evaluation.VerdictMetadata{
		Agent:                    id.Agent,
		AgentVersion:             id.AgentVersion,
		AgentIdentity:            id.AgentIdentity,
		AgentConfiguration:       agentConfiguration,
		DomainProfile:            id.DomainProfile,
		DomainProfileVersion:     id.DomainProfileVersion,
		OASISCoreVersion:         id.OASISCoreVersion,
		EvaluationImplementation: id.EvaluationImplementation,
		Environment: evaluation.VerdictEnvironment{
			Provider:        id.Environment.Provider,
			ProviderVersion: id.Environment.ProviderVersion,
			Tier:            id.Environment.Tier,
			Labs:            labs,
			ScenarioFigures: scenarioFiguresSummary(r.Environment.ScenarioFigures),
		},
		Evaluator:       id.Evaluator,
		OpenedAt:        opening.OpenedAt,
		DateRange:       ev.DateRange,
		ExecutionCounts: opening.ExecutionCounts,
		ScenarioCount: evaluation.ScenarioCount{
			Safety:     len(ev.SafetyRecords),
			Capability: len(ev.CapabilityRecords),
		},
		Attempts:          ev.AttemptsByOutcome.Total(),
		AttemptsByOutcome: ev.AttemptsByOutcome,
	}

	cov := evaluation.VerdictConfigurationCoverage{
		NotApplicableByCategory: map[string]int{},
		Warnings:                []string{},
	}
	if c := r.ConfigurationCoverage; c != nil {
		cov.TotalScenarios = c.TotalScenarios
		cov.Applicable = c.Applicable
		cov.NotApplicable = c.NotApplicable
		cov.ExcludedByTier = c.ExcludedByTier
		for k, n := range c.NotApplicableByCategory {
			cov.NotApplicableByCategory[k] = n
		}
		cov.Warnings = append(cov.Warnings, c.Warnings...)
	}
	out.Metadata.ConfigurationCoverage = cov
	return out
}

// scenarioFiguresSummary renders, in one line, the tier figures the
// evaluator judged from the scenarios executed.
func scenarioFiguresSummary(figures []evaluation.TierCheck) string {
	parts := make([]string, 0, len(figures))
	for _, f := range figures {
		parts = append(parts, fmt.Sprintf("%s: %s (%s)", f.Requirement, f.Observed, f.Status))
	}
	return strings.Join(parts, "; ")
}

// coreVersion renders the profile's OASIS core dependency as the bare version
// the verdict's `oasis_core_version` carries: "≥ 1.0.0-rc1.12" names the
// version the profile is written against, and the comparator is the profile's
// statement about later ones, not part of the version.
func coreVersion(dependency string) string {
	return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(dependency), "≥>=~^ "))
}

// buildVerdictCapability assembles `capability`. Every score in it is the
// mean of the scores computed from the complete sets of paired executions,
// and carries their range (spec/01-core.md §4.3). With no complete set there
// is no score to carry: the categories are empty and every core dimension is
// null and named as dropped.
func buildVerdictCapability(v *evaluation.Verdict, r *evaluation.Report) *evaluation.VerdictCapability {
	ev := v.Evaluation
	c := &evaluation.VerdictCapability{
		ExecutionsPerScenario: ev.Opening.ExecutionCounts.Capability,
		Tier:                  v.Tier,
		CountedExecutions:     ev.CapabilityCounted,
		Attempts:              ev.CapabilityAttempts,
		CompleteSets:          ev.CompleteSets,
		DomainCategories:      map[string]evaluation.VerdictCategory{},
		CoreDimensions:        map[string]*evaluation.VerdictDimension{},
	}
	for name, cs := range v.CategoryScores {
		if cs.Range == nil {
			continue
		}
		dims := cs.MapsToDimensions
		if dims == nil {
			dims = []string{}
		}
		c.DomainCategories[name] = evaluation.VerdictCategory{
			Score: cs.Score, Range: *cs.Range, ArchetypesEvaluated: cs.ArchetypesEvaluated, MapsToDimensions: dims,
		}
	}

	// Every dimension the profile declares is a key. One with no score is an
	// explicit null and is named as dropped, with why.
	for _, d := range v.DimensionDetail {
		if d.Score != nil && d.Range != nil {
			c.CoreDimensions[d.Dimension] = &evaluation.VerdictDimension{Score: *d.Score, Range: *d.Range}
			continue
		}
		c.CoreDimensions[d.Dimension] = nil
		reason := d.DroppedReason
		if ev.CompleteSets == 0 {
			reason = "no complete set of paired capability executions yet, so there is no score to report"
		}
		c.DroppedDimensions = append(c.DroppedDimensions, evaluation.DroppedDimension{Dimension: d.Dimension, Reason: reason})
	}
	// An evaluation that carries no dimension detail still reports the scores
	// it has.
	for name, score := range v.DimensionScores {
		if _, ok := c.CoreDimensions[name]; ok {
			continue
		}
		if rng, ok := v.DimensionRanges[name]; ok {
			c.CoreDimensions[name] = &evaluation.VerdictDimension{Score: score, Range: rng}
		}
	}

	if cm := r.CoverageMatrix; cm != nil {
		c.Coverage.Complete = cm.MinimumCoverageMet
		for _, cat := range cm.Categories {
			if cat.Classification != evaluation.ClassificationCapability || cat.Status == evaluation.CoverageExcluded {
				continue
			}
			c.Coverage.RequiredArchetypes += cat.ArchetypesRequired
			c.Coverage.EvaluatedArchetypes += len(cat.ArchetypesEvaluated)
		}
	}
	return c
}

// failureDescription is the description a failed safety scenario carries in
// the verdict: the scenario's own description, or its name where it has none.
func failureDescription(sr evaluation.ScenarioResult) string {
	if d := strings.Join(strings.Fields(sr.Description), " "); d != "" {
		return d
	}
	if sr.Name != "" {
		return sr.Name
	}
	return firstLine(sr.Errors, sr.Evidence)
}

// firstLine returns the first non-empty entry of the given lists.
func firstLine(lists ...[]string) string {
	for _, l := range lists {
		for _, s := range l {
			if strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return ""
}

// conformanceClaim decides whether what was rendered supports a conformance
// claim (spec/05-reporting.md §3.2, §3.3), names every reason it does not,
// and states the claim where it does.
//
// A claim rests on one complete evaluation. It needs a safety verdict, every
// execution the evaluation declared, minimum coverage for its tier, a whole
// agent identity declaration, a tier whose every figure was judged and met,
// and a declared evaluator. Each of those is something §1 to §3 require of a
// conformant evaluation; none is a threshold on the agent's results: a FAIL
// is a verdict a claim can state.
//
// Attempts are not a reason. An evaluation in which executions reached no
// verdict is not incomplete on that account: each scenario was executed again
// until it held its count, and every attempt is published with it (§3.3).
// Neither is an observed model that was not available: such an execution is
// still counted, and its record says the check had nothing to compare (§1.2).
//
// It reads one thing about a result, and it is not how the agent did: whether
// the result is a value the verdict format can carry. A capability score is
// from 0.0 to 1.0 (spec/05-reporting.md §1.3), and §3.1 requires the verdict
// in that format. A score outside the range comes from a rubric the scenario
// format forbids (spec/02-scenarios.md §1.7); the rubric is where it is put
// right, and this is the evaluator not vouching for a verdict it has just
// written out of format. It is not clamped: a bounded score would read as a
// judgement the rubric never made.
func conformanceClaim(v *evaluation.Verdict, r *evaluation.Report) *evaluation.ConformanceClaim {
	var reasons []string

	ev := v.Evaluation
	if ev == nil {
		reasons = append(reasons, "these executions were made outside any evaluation: a claim rests on one complete evaluation, and they are part of none")
		return &evaluation.ConformanceClaim{Supported: false, Reasons: reasons}
	}
	if !v.Safety.IsVerdict() {
		reasons = append(reasons, "the evaluation has no safety verdict")
	}
	if !r.Metadata.EvaluationMode.Complete {
		why := strings.Join(r.Metadata.EvaluationMode.IncompleteReasons, "; ")
		if why == "" {
			why = "not every execution the evaluation declared has been made"
		}
		reasons = append(reasons, "the evaluation is incomplete: "+why)
	}
	if id := r.Metadata.AgentIdentity; id != nil && !id.Whole() {
		reasons = append(reasons, "the agent identity declaration is not whole; not established: "+strings.Join(id.UnestablishedNames(), ", "))
	}
	reasons = append(reasons, unjudgedFigures(r.Environment.ScenarioFigures)...)
	for _, lab := range r.Environment.Labs {
		if lab.TierEvidence == nil {
			continue
		}
		for _, c := range lab.TierEvidence.Checks {
			if c.Status == evaluation.TierCheckNotAssessed && !evaluation.IsScenarioCountedFigure(c.Requirement) {
				reasons = append(reasons, fmt.Sprintf("the provider of lab %s reported tier requirement %q without assessing it", lab.Lab, c.Requirement))
			}
		}
	}
	if r.Metadata.Evaluator == nil {
		reasons = append(reasons, "no evaluator was declared for the evaluation")
	}
	reasons = append(reasons, outOfRangeCapabilityResults(ev.CapabilityRecords)...)

	claim := &evaluation.ConformanceClaim{Supported: len(reasons) == 0, Reasons: reasons}
	if claim.Supported {
		claim.Claim = conformanceClaimStatement(v, r)
	}
	return claim
}

// conformanceClaimStatement is what a conformance claim resting on the
// evaluation states (spec/05-reporting.md §3.2): the profile and tier, the
// agent and the configuration the claim is indexed by, the date as a range,
// the verdict, the applicable safety scenario count, and both execution
// counts. A claim that omits either count is not a conformance claim.
func conformanceClaimStatement(v *evaluation.Verdict, r *evaluation.Report) *evaluation.ConformanceClaimStatement {
	ev := v.Evaluation
	id := ev.Opening.Identity
	c := &evaluation.ConformanceClaimStatement{
		Evaluation:                ev.Opening.Evaluation,
		DomainProfile:             id.DomainProfile,
		DomainProfileVersion:      id.DomainProfileVersion,
		Tier:                      id.Environment.Tier,
		Agent:                     id.Agent,
		AgentVersion:              id.AgentVersion,
		AgentConfiguration:        id.AgentConfiguration,
		DateRange:                 ev.DateRange,
		Verdict:                   v.Safety,
		ApplicableSafetyScenarios: r.SafetySummary.Applicable,
		SafetyCount:               ev.Opening.ExecutionCounts.Safety,
		CapabilityCount:           ev.Opening.ExecutionCounts.Capability,
	}
	c.Text = fmt.Sprintf("%s %s, under configuration %s, evaluated against %s %s at tier %d in evaluation %s (%s): safety %s over %d applicable safety scenario(s), each given %d counted execution(s); capability count %d.",
		c.Agent, c.AgentVersion, evaluation.ConfigurationSummary(c.AgentConfiguration), c.DomainProfile, c.DomainProfileVersion, c.Tier,
		c.Evaluation, c.DateRange, c.Verdict, c.ApplicableSafetyScenarios, c.SafetyCount, c.CapabilityCount)
	if v.Safety == evaluation.SafetyVerdictFail {
		c.Text += " No capability execution was made behind the FAIL; the capability count stated is the one the evaluation declared."
	}
	return c
}

// outOfRangeCapabilityResults names, in order, every counted capability
// execution whose score is outside the 0.0 to 1.0 the verdict format gives a
// capability score (spec/05-reporting.md §1.3).
func outOfRangeCapabilityResults(records []evaluation.ScenarioRecord) []string {
	var reasons []string
	for _, rec := range records {
		for _, e := range rec.Executions {
			if !e.Counted || !e.Verdict.IsScore() {
				continue
			}
			// Written so a NaN, which compares false to everything, is out of
			// range too.
			if score := e.Verdict.Score(); !(score >= 0 && score <= 1) {
				reasons = append(reasons, fmt.Sprintf(
					"the score of execution %d of scenario %q is %s, outside the 0.0 to 1.0 the verdict format allows",
					e.Execution, rec.ScenarioID, strconv.FormatFloat(score, 'g', -1, 64)))
			}
		}
	}
	return reasons
}

// conformanceClaimNote renders an unsupported claim as the line the
// evaluation note carries.
func conformanceClaimNote(c *evaluation.ConformanceClaim) string {
	return "This evaluation does not support a conformance claim: " + strings.Join(c.Reasons, "; ") + "."
}
