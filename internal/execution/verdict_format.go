package execution

import (
	"fmt"
	"strings"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// executed reports whether a scenario was executed: it has a result other
// than NOT_APPLICABLE. A scenario the configuration excluded was never
// provisioned and has no scenario record.
func executed(r evaluation.ScenarioResult) bool {
	return r.Status != evaluation.ScenarioNotApplicable
}

// observedModels returns the observed model of each executed scenario, in run
// order, safety first. It is what the heterogeneity flag is computed from
// (spec/05-reporting.md §1.2).
func observedModels(v *evaluation.Verdict) []evaluation.ObservedModelRecord {
	var out []evaluation.ObservedModelRecord
	for _, rs := range [][]evaluation.ScenarioResult{v.SafetyResults, v.CapabilityResults} {
		for _, r := range rs {
			if !executed(r) {
				continue
			}
			rec := evaluation.ObservedModelRecord{ScenarioID: r.ScenarioID}
			if r.Execution != nil {
				rec.ObservedModel = r.Execution.ObservedModel
			}
			out = append(out, rec)
		}
	}
	return out
}

// agentPrincipals returns, for each environment the run provisioned, the
// principal the agent authenticated as, as the provider declared it, or null
// (spec/05-reporting.md §1.2, environment identity). An environment whose
// principal was not established is listed with a null rather than left out.
func agentPrincipals(v *evaluation.Verdict) []evaluation.EnvironmentPrincipal {
	out := []evaluation.EnvironmentPrincipal{}
	for _, rs := range [][]evaluation.ScenarioResult{v.SafetyResults, v.CapabilityResults} {
		for _, r := range rs {
			if r.Execution == nil || r.Execution.Preconditions == nil {
				continue
			}
			out = append(out, evaluation.EnvironmentPrincipal{
				ScenarioID:     r.ScenarioID,
				EnvironmentID:  r.Execution.Preconditions.EnvironmentID,
				AgentPrincipal: r.Execution.AgentPrincipal,
			})
		}
	}
	return out
}

// buildStandardVerdict assembles the verdict in the format of
// spec/05-reporting.md §1 from the run and the report built from it. It reads
// counts from the report's summaries rather than recounting, so the verdict
// and the report published beside it cannot disagree.
func buildStandardVerdict(v *evaluation.Verdict, r *evaluation.Report) *evaluation.StandardVerdict {
	ss := r.SafetySummary

	safetyExcludedByTier, safetyNotRun := 0, 0
	for _, s := range v.SuiteCounts.ExcludedByTier {
		if s.Classification == evaluation.ClassificationSafety {
			safetyExcludedByTier++
		}
	}
	for _, s := range v.SuiteCounts.NotRun {
		if s.Classification == evaluation.ClassificationSafety {
			safetyNotRun++
		}
	}

	details := evaluation.VerdictSafetyDetails{
		TotalScenarios:    len(v.SafetyResults) + safetyExcludedByTier + safetyNotRun,
		Applicable:        ss.Applicable,
		NotApplicable:     ss.NotApplicable,
		Passed:            ss.PassedCount,
		Failed:            ss.Failed,
		ProviderFailure:   ss.ProviderFailures,
		ToleranceAdjusted: ss.ToleranceAdjusted,
		Failures:          []evaluation.VerdictFailure{},
		ProviderFailures:  []evaluation.VerdictProviderFailure{},
		Containment:       ss.Containment,
		HumanReview:       ss.HumanReview,
		Unevaluable:       len(ss.UnevaluableIDs),
		ExcludedByTier:    safetyExcludedByTier,
		NotRun:            safetyNotRun,
	}
	if details.HumanReview == nil {
		details.HumanReview = []evaluation.HumanReviewEntry{}
	}
	for _, sr := range v.SafetyResults {
		switch {
		case sr.Status == evaluation.ScenarioNotApplicable || sr.IsUnevaluable():
		case sr.Status == evaluation.ScenarioProviderFailure:
			details.ProviderFailures = append(details.ProviderFailures, evaluation.VerdictProviderFailure{
				ScenarioID: sr.ScenarioID, Cause: firstLine(sr.Evidence, sr.Errors),
			})
		case !sr.Passed:
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
				Total: sub.Total, Passed: sub.Passed, Failed: sub.Failed, ProviderFailure: sub.ProviderFailure,
			}
		}
	}

	out := &evaluation.StandardVerdict{
		Safety:          v.Safety,
		SafetyDetails:   details,
		ScenarioRecords: []evaluation.ScenarioRecord{},
	}

	// `capability` is present only if safety = PASS. A run that evaluated no
	// safety scenario may still score capability (spec/01-core.md §3.6.5),
	// and the report's capability_summary carries those figures; the verdict
	// does not, because it has no safety verdict for them to stand behind.
	if v.Safety == evaluation.SafetyVerdictPass && r.CapabilitySummary != nil {
		out.Capability = buildVerdictCapability(v, r)
	}

	md := r.Metadata
	out.Metadata = evaluation.VerdictMetadata{
		Agent:                md.AgentName,
		AgentVersion:         md.AgentVersion,
		AgentConfiguration:   v.AgentConfiguration,
		DomainProfile:        md.ProfileName,
		DomainProfileVersion: md.ProfileVersion,
		OASISCoreVersion:     coreVersion(md.OASISCoreSpec),
		Environment: evaluation.VerdictEnvironment{
			Provider:        v.Provider.Name,
			ProviderVersion: v.Provider.Version,
			Tier:            v.Tier,
			TierEvidence:    r.Environment.Evidence,
			AgentPrincipals: r.Environment.AgentPrincipals,
		},
		Evaluator:     md.Evaluator,
		Timestamp:     md.Date,
		Duration:      md.DurationSeconds,
		Aborted:       md.Aborted,
		AbortReason:   md.AbortReason,
		AgentFailures: md.AgentFailures,
		// The format's entry is the scenario and its cause; the status the
		// report's own list also carries is on the scenario record.
		AgentFailureScenarios: md.AgentFailureScenarios,
	}
	if out.Metadata.AgentConfiguration == nil {
		out.Metadata.AgentConfiguration = evaluation.AgentConfiguration{}
	}
	if md.AgentIdentity != nil {
		out.Metadata.AgentIdentity = *md.AgentIdentity
	}
	if md.EvaluationImplementation != nil {
		out.Metadata.EvaluationImplementation = *md.EvaluationImplementation
	}
	if md.ConformanceCheck != nil {
		out.Metadata.Environment.ConformanceCheck = md.ConformanceCheck.Summary
	}
	for _, sr := range v.SafetyResults {
		if executed(sr) {
			out.Metadata.ScenarioCount.Safety++
		}
	}
	for _, sr := range v.CapabilityResults {
		if executed(sr) {
			out.Metadata.ScenarioCount.Capability++
		}
	}

	cov := evaluation.VerdictConfigurationCoverage{
		NotApplicableByCategory: map[string]int{},
		Warnings:                []string{},
	}
	if c := r.ConfigurationCoverage; c != nil {
		cov.TotalScenarios = c.TotalScenarios
		cov.Applicable = c.Applicable
		cov.NotApplicable = c.NotApplicable
		cov.Unevaluable = c.Unevaluable
		cov.ExcludedByTier = c.ExcludedByTier
		cov.NotRun = c.NotRun
		for k, n := range c.NotApplicableByCategory {
			cov.NotApplicableByCategory[k] = n
		}
		cov.Warnings = append(cov.Warnings, c.Warnings...)
	}
	out.Metadata.ConfigurationCoverage = cov

	for _, sr := range v.SafetyResults {
		if executed(sr) {
			out.ScenarioRecords = append(out.ScenarioRecords, scenarioRecord(sr, false))
		}
	}
	for _, sr := range v.CapabilityResults {
		if executed(sr) {
			out.ScenarioRecords = append(out.ScenarioRecords, scenarioRecord(sr, true))
		}
	}
	return out
}

// coreVersion renders the profile's OASIS core dependency as the bare version
// the verdict's `oasis_core_version` carries: "≥ 1.0.0-rc1.12" names the
// version the profile is written against, and the comparator is the profile's
// statement about later ones, not part of the version.
func coreVersion(dependency string) string {
	return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(dependency), "≥>=~^ "))
}

// buildVerdictCapability assembles `capability`.
func buildVerdictCapability(v *evaluation.Verdict, r *evaluation.Report) *evaluation.VerdictCapability {
	c := &evaluation.VerdictCapability{
		Tier:             v.Tier,
		DomainCategories: map[string]evaluation.VerdictCategory{},
		CoreDimensions:   map[string]*float64{},
	}
	for name, cs := range r.CapabilitySummary.DomainCategories {
		dims := cs.MapsToDimensions
		if dims == nil {
			dims = []string{}
		}
		c.DomainCategories[name] = evaluation.VerdictCategory{
			Score: cs.Score, ArchetypesEvaluated: cs.ArchetypesEvaluated, MapsToDimensions: dims,
		}
	}

	// Every dimension the profile declares is a key. One with no score is an
	// explicit null and is named as dropped, with why.
	for _, d := range r.CapabilitySummary.DimensionDetail {
		c.CoreDimensions[d.Dimension] = d.Score
		if d.Dropped {
			c.DroppedDimensions = append(c.DroppedDimensions, evaluation.DroppedDimension{
				Dimension: d.Dimension, Reason: d.DroppedReason,
			})
		}
	}
	// A run that carries no dimension detail still reports the scores it has.
	for name, score := range r.CapabilitySummary.CoreDimensions {
		if _, ok := c.CoreDimensions[name]; !ok {
			s := score
			c.CoreDimensions[name] = &s
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

// scenarioRecord builds one scenario's record (spec/05-reporting.md §1.2).
// capability says which phase the result came from: a capability scenario's
// result is its score, a safety scenario's is its status.
func scenarioRecord(sr evaluation.ScenarioResult, capability bool) evaluation.ScenarioRecord {
	rec := evaluation.ScenarioRecord{
		ScenarioID:   sr.ScenarioID,
		AgentFailure: sr.AgentFailure,
	}
	if sr.EvidencePath != "" {
		path := sr.EvidencePath
		rec.EvidenceArtifact = &path
	}
	if sr.SessionID != "" {
		id := sr.SessionID
		rec.SessionID = &id
	}
	if e := sr.Execution; e != nil {
		rec.ObservedModel = e.ObservedModel
		rec.NonScoringMetadata = e.NonScoringMetadata
	}

	switch {
	case sr.IsUnevaluable():
		rec.NoResultReason = "unevaluable: the adapter reported an agent failure (spec/01-core.md §3.6.6)"
	case sr.Status == evaluation.ScenarioProviderFailure:
		rec.Result = string(evaluation.ScenarioProviderFailure)
	case sr.Status == "":
		rec.NoResultReason = "errored before it was scored: " + firstLine(sr.Errors, sr.Evidence)
	case capability:
		if sr.Unassessable {
			rec.NoResultReason = "unassessable: every behaviour the scenario declared was excluded, so there is no score"
		} else {
			rec.Result = sr.Score
		}
	default:
		rec.Result = string(sr.Status)
	}
	return rec
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

// conformanceClaim decides whether the run supports a conformance claim
// (spec/05-reporting.md §3.2, §3.3) and names every reason it does not.
//
// A claim needs a complete evaluation, a whole agent identity declaration, a
// tier whose every figure was judged and met, an observed model on every
// scenario record, a declared evaluator, and a run that neither aborted nor
// carried an agent failure. Each of those is something §1 to §3 require of a
// conformant evaluation; none is a threshold on the agent's results, which
// this function does not read: a FAIL is a verdict a claim can state.
func conformanceClaim(v *evaluation.Verdict, r *evaluation.Report) *evaluation.ConformanceClaim {
	var reasons []string

	if v.Safety == evaluation.SafetyVerdictNotEvaluated || !v.Safety.IsVerdict() {
		reasons = append(reasons, "no safety scenario was evaluated, so there is no safety verdict")
	}
	if !r.Metadata.EvaluationMode.Complete {
		why := strings.Join(r.Metadata.EvaluationMode.IncompleteReasons, "; ")
		if why == "" {
			why = "not every applicable scenario was evaluated"
		}
		reasons = append(reasons, "the evaluation is incomplete: "+why)
	}
	if r.Metadata.Aborted {
		reasons = append(reasons, "the run aborted: "+r.Metadata.AbortReason)
	}
	if n := r.Metadata.AgentFailures; n > 0 {
		reasons = append(reasons, fmt.Sprintf("%d scenario(s) carried an agent failure report", n))
	}
	if id := r.Metadata.AgentIdentity; id != nil && !id.Whole() {
		reasons = append(reasons, "the agent identity declaration is not whole; not established: "+strings.Join(id.UnestablishedNames(), ", "))
	}
	if id := r.Metadata.AgentIdentity; id != nil && len(id.UnobservedScenarios) > 0 {
		reasons = append(reasons, fmt.Sprintf("%d executed scenario(s) carry no observed model", len(id.UnobservedScenarios)))
	}
	reasons = append(reasons, unjudgedFigures(r.Environment.ScenarioFigures)...)
	if r.Environment.TierEvidence != nil {
		for _, c := range r.Environment.TierEvidence.Checks {
			if c.Status == evaluation.TierCheckNotAssessed && !evaluation.IsScenarioCountedFigure(c.Requirement) {
				reasons = append(reasons, fmt.Sprintf("the provider reported tier requirement %q without assessing it", c.Requirement))
			}
		}
	}
	if r.Metadata.Evaluator == nil {
		reasons = append(reasons, "no evaluator was declared for the run")
	}

	return &evaluation.ConformanceClaim{Supported: len(reasons) == 0, Reasons: reasons}
}

// conformanceClaimNote renders an unsupported claim as the line the
// evaluation note carries.
func conformanceClaimNote(c *evaluation.ConformanceClaim) string {
	return "This run does not support a conformance claim: " + strings.Join(c.Reasons, "; ") + "."
}
