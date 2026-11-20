package execution

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/record"
)

// Assemble builds the verdict and the report from the evaluation's record
// and writes them (spec/04-execution.md §3.5). It may be called at any time,
// including while the evaluation is incomplete, and it changes nothing in the
// record.
//
// An evaluation that does not agree with itself is refused: no verdict is
// emitted, and the error is an *evaluation.RefusedError naming every reason.
func (s *Session) Assemble(ctx context.Context, format, outputPath string) (*evaluation.Verdict, error) {
	verdict, err := s.assemble(outputPath)
	if err != nil {
		return nil, err
	}
	if err := s.o.reporter.Write(ctx, verdict, format, outputPath); err != nil {
		return verdict, fmt.Errorf("write report: %w", err)
	}
	return verdict, nil
}

// rebase makes a path that is relative to the evaluation directory relative
// to the directory the report is written to.
func (s *Session) rebase(path, outputPath string) string {
	if path == "" {
		return ""
	}
	return evidenceRelPath(filepath.Join(s.store.Dir(), path), outputPath)
}

func (s *Session) assemble(outputPath string) (*evaluation.Verdict, error) {
	all, err := s.store.Executions()
	if err != nil {
		return nil, err
	}
	safetyBytes, err := s.store.SafetyVerdictRecord()
	if err != nil {
		return nil, err
	}

	// 1. Refuse an evaluation that does not agree with itself.
	var written *evaluation.SafetyVerdictRecord
	recordVerdict := evaluation.SafetyVerdictNotEvaluated
	var reasons []string
	if safetyBytes != nil {
		rec, perr := record.ParseSafetyVerdictRecord(safetyBytes)
		if perr != nil {
			reasons = append(reasons, fmt.Sprintf("the safety verdict record cannot be read: %v", perr))
		} else {
			written = &rec
			recordVerdict = rec.Safety
			if rec.Evaluation != s.opening.Evaluation {
				reasons = append(reasons, fmt.Sprintf("the safety verdict record names evaluation %q, not %q", rec.Evaluation, s.opening.Evaluation))
			}
		}
	}
	reasons = append(reasons, evaluation.CheckAgreement(s.opening, all, safetyBytes, recordVerdict)...)

	// 2. Each scenario's result, from its counted executions.
	safetyRecords := s.scenarioRecords(evaluation.ClassificationSafety, all)
	capabilityRecords := s.scenarioRecords(evaluation.ClassificationCapability, all)
	safety := evaluation.SafetyVerdictOf(safetyRecords)
	if written != nil && written.Safety != safety {
		// The record was written from these executions and is not rewritten,
		// so the two can differ only if one of them was changed afterwards.
		reasons = append(reasons, fmt.Sprintf("the safety verdict record's verdict is %s and the evaluation's safety executions give %s", written.Safety, safety))
	}
	if len(reasons) > 0 {
		return nil, &evaluation.RefusedError{Reasons: reasons}
	}

	byScenario := map[string][]evaluation.StoredExecution{}
	for _, e := range all {
		byScenario[e.ScenarioID] = append(byScenario[e.ScenarioID], e)
	}
	recordOf := map[string]evaluation.ScenarioRecord{}
	for _, r := range safetyRecords {
		recordOf[r.ScenarioID] = r
	}
	for _, r := range capabilityRecords {
		recordOf[r.ScenarioID] = r
	}

	id := s.opening.Identity
	tier := id.Environment.Tier
	assembled := &evaluation.AssembledEvaluation{
		Opening:           s.opening,
		AttemptsByOutcome: evaluation.NewAttemptCounts(),
	}
	verdict := &evaluation.Verdict{
		OutputPath:         outputPath,
		AgentID:            id.Agent,
		AgentVersion:       id.AgentVersion,
		ProfileID:          id.DomainProfile,
		ProfileVersion:     id.DomainProfileVersion,
		Tier:               tier,
		Date:               s.o.now().UTC(),
		AgentConfiguration: id.AgentConfiguration,
		OASISCoreSpec:      s.profile.Metadata.OASISCore,
		Safety:             safety,
		SafetyPassed:       safety == evaluation.SafetyVerdictPass,
		Implementation:     id.EvaluationImplementation,
		Provider:           evaluation.ProviderIdentity{Name: id.Environment.Provider, Version: id.Environment.ProviderVersion},
		Evaluation:         assembled,
	}
	identity := id.AgentIdentity
	verdict.IdentityFacts = &identity
	if id.Evaluator != nil {
		verdict.Evaluator = *id.Evaluator
	}

	// The scenario-level results, in suite order: one per scenario at or
	// below the tier, whatever became of it.
	usedLabs := map[string]bool{}
	var capabilityScenarios []evaluation.Scenario
	for _, entry := range s.opening.Scenarios {
		scenario := s.byID[entry.ScenarioID]
		result := evaluation.ScenarioResult{
			ScenarioID:  entry.ScenarioID,
			Category:    scenario.Category,
			Subcategory: scenario.Subcategory,
			Archetype:   scenario.Archetype,
		}
		describeScenario(&result, &scenario)
		if !entry.Applicable {
			result.Status = evaluation.ScenarioNotApplicable
			result.Passed = true
			result.ExclusionConditions = entry.ExclusionConditions
		} else {
			rec := recordOf[entry.ScenarioID]
			s.scenarioLevel(&result, rec, byScenario[entry.ScenarioID], outputPath)
			for _, e := range rec.Executions {
				usedLabs[e.Lab] = true
				if !e.Counted {
					assembled.AttemptsByOutcome[e.Outcome]++
				}
			}
			if entry.Classification == evaluation.ClassificationSafety {
				assembled.SafetyAttempts += rec.Attempts
				assembled.SafetyCounted += rec.CountedExecutions
			} else {
				assembled.CapabilityAttempts += rec.Attempts
				assembled.CapabilityCounted += rec.CountedExecutions
			}
		}
		if entry.Classification == evaluation.ClassificationSafety {
			verdict.SafetyResults = append(verdict.SafetyResults, result)
		} else {
			verdict.CapabilityResults = append(verdict.CapabilityResults, result)
			if entry.Applicable {
				capabilityScenarios = append(capabilityScenarios, scenario)
			}
		}
	}

	// The records the verdict publishes carry paths relative to the report.
	for i := range safetyRecords {
		safetyRecords[i].Executions = s.rebaseRecords(safetyRecords[i].Executions, outputPath)
	}
	for i := range capabilityRecords {
		capabilityRecords[i].Executions = s.rebaseRecords(capabilityRecords[i].Executions, outputPath)
	}
	assembled.SafetyRecords = safetyRecords
	assembled.CapabilityRecords = capabilityRecords
	assembled.DateRange = evaluation.DateRangeOf(append(append([]evaluation.ScenarioRecord{}, safetyRecords...), capabilityRecords...))
	if safetyBytes != nil {
		assembled.SafetyVerdictRecord = s.rebase(record.SafetyVerdictRecordFile, outputPath)
		assembled.SafetyVerdictHash = evaluation.HashSafetyVerdictRecord(safetyBytes)
	}

	// 3. The capability scores, over sets of paired executions.
	if safety == evaluation.SafetyVerdictPass {
		sets := pairedSets(capabilityScenarios, byScenario, s.opening.ExecutionCounts.Capability)
		assembled.CompleteSets = len(sets)
		agg := aggregateSets(sets, capabilityScenarios, s.profile)
		verdict.ArchetypeScores = agg.archetypes
		verdict.CategoryScores = agg.categories
		verdict.DimensionScores = agg.dimensions
		verdict.DimensionRanges = agg.dimensionRanges
		if len(agg.dimensions) > 0 {
			total := 0.0
			for _, v := range agg.dimensions {
				total += v
			}
			verdict.CapabilityScore = total / float64(len(agg.dimensions))
		}
	}

	// 4. Coverage against the tier's requirements, and configuration
	// coverage.
	suite := s.profile.Scenarios
	if len(suite) == 0 {
		suite = s.handed
	}
	allResults := append(append([]evaluation.ScenarioResult{}, verdict.SafetyResults...), verdict.CapabilityResults...)
	verdict.SuiteCounts = ComputeSuiteCounts(suite, s.handed, tier, allResults)
	verdict.ConfigurationCoverage = ComputeConfigurationCoverage(allResults)
	ApplySuiteCounts(verdict.ConfigurationCoverage, verdict.SuiteCounts, allResults)
	verdict.CoverageMatrix = ComputeCoverage(s.profile, suite, id.AgentConfiguration, tier, allResults)
	if safety == evaluation.SafetyVerdictPass {
		verdict.DimensionDetail = DimensionDetails(verdict.CategoryScores, s.profile.ScoringModel, verdict.CoverageMatrix)
		for i := range verdict.DimensionDetail {
			if r, ok := verdict.DimensionRanges[verdict.DimensionDetail[i].Dimension]; ok {
				rr := r
				verdict.DimensionDetail[i].Range = &rr
			}
		}
	}

	// The labs an execution was made on, each with the check made once at
	// its creation.
	labs, err := s.store.Labs()
	if err != nil {
		return nil, err
	}
	for _, lab := range labs {
		if usedLabs[lab.Lab] {
			assembled.Labs = append(assembled.Labs, lab)
		}
	}
	executedAtTier := make([]evaluation.Scenario, 0, len(s.byID))
	for _, entry := range s.opening.Scenarios {
		executedAtTier = append(executedAtTier, s.byID[entry.ScenarioID])
	}
	figures := JudgeScenarioFigures(s.profile.TierRequirements[tier], tier, executedScenarios(executedResults(allResults), executedAtTier))
	verdict.Environment = evaluation.ReportEnvironment{
		TierClaimed:     tier,
		Provider:        id.Environment.Provider,
		ProviderVersion: id.Environment.ProviderVersion,
		ScenarioFigures: figures,
		Labs:            assembled.Labs,
	}
	if len(assembled.Labs) > 0 {
		first := assembled.Labs[0]
		verdict.Environment.TierEvidence = first.TierEvidence
		verdict.Environment.Characteristics = first.Characteristics
		verdict.Environment.Evidence = evaluation.TierEvidenceSummary(first.Provider, first.ProviderVersion, first.TierEvidence, figures...)
		verdict.ConformanceCheck = first.ConformanceCheck
		verdict.ProviderInfo = first.Endpoint
		verdict.Provider.Endpoint = first.Endpoint
	} else {
		verdict.Environment.Evidence = evaluation.TierEvidenceSummary(id.Environment.Provider, id.Environment.ProviderVersion, nil, figures...)
	}

	// 5. Whether the evaluation is complete.
	st, err := s.Status()
	if err != nil {
		return nil, err
	}
	assembled.Complete = st.Complete
	mode := evaluation.EvaluationMode{}
	switch {
	case st.SafetyOwed > 0:
		mode.IncompleteReasons = append(mode.IncompleteReasons, fmt.Sprintf("%d safety execution(s) are still to be made", st.SafetyOwed))
	case safety == evaluation.SafetyVerdictPass && st.CapabilityOwed > 0:
		mode.IncompleteReasons = append(mode.IncompleteReasons, fmt.Sprintf("%d capability execution(s) are still to be made", st.CapabilityOwed))
	}
	// An evaluation that holds every execution it declared and does not meet
	// minimum coverage for its tier is labelled incomplete too. On a safety
	// FAIL no capability execution is made, so capability coverage is not
	// what the label turns on.
	if st.Complete && safety == evaluation.SafetyVerdictPass && verdict.CoverageMatrix != nil && !verdict.CoverageMatrix.MinimumCoverageMet {
		mode.IncompleteReasons = append(mode.IncompleteReasons, fmt.Sprintf("minimum coverage for tier %d was not met", tier))
	}
	mode.Complete = len(mode.IncompleteReasons) == 0
	verdict.EvaluationMode = mode
	return verdict, nil
}

// executedResults returns the scenario-level results of scenarios of which
// at least one execution was made.
func executedResults(results []evaluation.ScenarioResult) []evaluation.ScenarioResult {
	var out []evaluation.ScenarioResult
	for _, r := range results {
		if r.Status != evaluation.ScenarioNotApplicable && len(r.Executions) > 0 {
			out = append(out, r)
		}
	}
	return out
}

func (s *Session) rebaseRecords(in []evaluation.ExecutionRecord, outputPath string) []evaluation.ExecutionRecord {
	out := make([]evaluation.ExecutionRecord, len(in))
	for i, rec := range in {
		if rec.EvidenceArtifact != nil {
			path := s.rebase(*rec.EvidenceArtifact, outputPath)
			rec.EvidenceArtifact = &path
		}
		out[i] = rec
	}
	return out
}

// scenarioLevel fills a scenario-level result from the scenario's record and
// the detail of each of its executions.
func (s *Session) scenarioLevel(result *evaluation.ScenarioResult, rec evaluation.ScenarioRecord, executions []evaluation.StoredExecution, outputPath string) {
	result.DeclaredCount = s.opening.Count(rec.Classification)
	result.CountedExecutions = rec.CountedExecutions
	result.Attempts = rec.Attempts
	allPassed := true
	for _, e := range executions {
		read := s.rebaseRecords([]evaluation.ExecutionRecord{e.AsRead()}, outputPath)[0]
		detail := evaluation.ExecutionDetail{Record: read}
		if e.Detail != nil && e.State == evaluation.ExecutionClosed {
			d := *e.Detail
			if d.EvidencePath != "" {
				d.EvidencePath = s.rebase(d.EvidencePath, outputPath)
			}
			detail.Result = &d
			if read.Counted {
				result.BehaviorsDeclared += d.BehaviorsDeclared
				result.BehaviorsEvaluated += d.BehaviorsEvaluated
				result.ToleranceFlag = result.ToleranceFlag || d.ToleranceFlag
				allPassed = allPassed && d.Passed
				if result.Band == "" {
					result.SynonymListVersion = d.SynonymListVersion
				}
			}
		}
		result.Executions = append(result.Executions, detail)
	}
	switch {
	case rec.Result == nil:
		result.ShortOfCount = true
	case rec.Classification == evaluation.ClassificationSafety:
		result.Status = evaluation.ScenarioResultStatus(rec.Result.Status())
		result.Passed = result.Status == evaluation.ScenarioPass
	default:
		result.Score = rec.Result.Score()
		result.Range = rec.Range
		result.Passed = allPassed
		result.Status = evaluation.ScenarioFail
		if allPassed {
			result.Status = evaluation.ScenarioPass
		}
	}
}

// pairedSets pairs the counted executions of the applicable capability
// scenarios by position (spec/01-core.md §4.3). The first counted execution
// of each scenario, in order of start, forms the first set; the second the
// second; to the capability count. An attempt holds no position and moves
// none. Only complete sets are returned: one in which every applicable
// capability scenario holds a counted execution at that position.
//
// Each set is the per-execution results of the scenarios, in the order
// scenarios is in.
func pairedSets(scenarios []evaluation.Scenario, byScenario map[string][]evaluation.StoredExecution, declared int) [][]evaluation.ScenarioResult {
	if len(scenarios) == 0 {
		return nil
	}
	positions := make([][]evaluation.ScenarioResult, len(scenarios))
	complete := declared
	for i, sc := range scenarios {
		for _, e := range byScenario[sc.ID] {
			if e.State != evaluation.ExecutionClosed || !e.Record.Counted || e.Detail == nil {
				continue
			}
			positions[i] = append(positions[i], *e.Detail)
		}
		if len(positions[i]) < complete {
			complete = len(positions[i])
		}
	}
	sets := make([][]evaluation.ScenarioResult, 0, complete)
	for k := 0; k < complete; k++ {
		set := make([]evaluation.ScenarioResult, len(scenarios))
		for i := range scenarios {
			set[i] = positions[i][k]
		}
		sets = append(sets, set)
	}
	return sets
}

type setAggregates struct {
	archetypes      map[string]evaluation.ArchetypeScore
	categories      map[string]evaluation.CategoryScore
	dimensions      map[string]float64
	dimensionRanges map[string]evaluation.ScoreRange
}

// aggregateSets computes, for each complete set, the archetype, category and
// core dimension scores the profile's aggregation gives from that set's
// scenario scores, exactly as it would from a single execution of every
// scenario; and then, for each archetype, category and core dimension, the
// mean of its per-set scores and their range (spec/01-core.md §4.3).
//
// For a category aggregated by a minimum the score is therefore the mean of
// the per-set minimums, not the minimum of the scenario means.
func aggregateSets(sets [][]evaluation.ScenarioResult, scenarios []evaluation.Scenario, profile *evaluation.Profile) setAggregates {
	out := setAggregates{
		archetypes:      map[string]evaluation.ArchetypeScore{},
		categories:      map[string]evaluation.CategoryScore{},
		dimensions:      map[string]float64{},
		dimensionRanges: map[string]evaluation.ScoreRange{},
	}
	archetypeScores := map[string][]float64{}
	categoryScores := map[string][]float64{}
	dimensionScores := map[string][]float64{}
	breakdown := map[string]map[string][]float64{}
	for _, set := range sets {
		archetypes := AggregateArchetype(set, scenarios)
		categories := AggregateCategory(archetypes, profile.CapabilityCategories)
		dimensions := AggregateDimension(categories, profile.ScoringModel)
		for name, a := range archetypes {
			archetypeScores[name] = append(archetypeScores[name], a.Score)
			base, seen := out.archetypes[name]
			if !seen {
				base = a
			} else {
				// An archetype is comparable only if it was in every set.
				base.Comparable = base.Comparable && a.Comparable
				base.ShrunkenScenarios = mergeNames(base.ShrunkenScenarios, a.ShrunkenScenarios)
			}
			out.archetypes[name] = base
		}
		for name, c := range categories {
			categoryScores[name] = append(categoryScores[name], c.Score)
			base, seen := out.categories[name]
			if !seen {
				base = c
			} else {
				base.Comparable = base.Comparable && c.Comparable
				base.UnscoredArchetypes = mergeNames(base.UnscoredArchetypes, c.UnscoredArchetypes)
				base.IncomparableArchetypes = mergeNames(base.IncomparableArchetypes, c.IncomparableArchetypes)
			}
			out.categories[name] = base
			if breakdown[name] == nil {
				breakdown[name] = map[string][]float64{}
			}
			for _, b := range c.ArchetypeBreakdown {
				breakdown[name][b.Archetype] = append(breakdown[name][b.Archetype], b.Score)
			}
		}
		for name, d := range dimensions {
			dimensionScores[name] = append(dimensionScores[name], d)
		}
	}
	for name, scores := range archetypeScores {
		mean, r, _ := evaluation.MeanAndRange(scores)
		a := out.archetypes[name]
		a.Score, a.Range = mean, &r
		out.archetypes[name] = a
	}
	for name, scores := range categoryScores {
		mean, r, _ := evaluation.MeanAndRange(scores)
		c := out.categories[name]
		c.Score, c.Range = mean, &r
		for i, b := range c.ArchetypeBreakdown {
			if m, br, ok := evaluation.MeanAndRange(breakdown[name][b.Archetype]); ok {
				c.ArchetypeBreakdown[i].Score = m
				c.ArchetypeBreakdown[i].Range = &br
			}
		}
		out.categories[name] = c
	}
	for name, scores := range dimensionScores {
		mean, r, _ := evaluation.MeanAndRange(scores)
		out.dimensions[name] = mean
		out.dimensionRanges[name] = r
	}
	return out
}

// mergeNames returns the sorted union of two lists of names.
func mergeNames(a, b []string) []string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	set := map[string]struct{}{}
	for _, n := range a {
		set[n] = struct{}{}
	}
	for _, n := range b {
		set[n] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
