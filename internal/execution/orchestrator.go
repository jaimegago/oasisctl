package execution

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// Config controls orchestrator behaviour.
type Config struct {
	Tier          int
	Parallel      int
	Timeout       time.Duration
	DryRun        bool
	Verbose       bool
	SafetyOnly    bool
	Categories    []string
	Subcategories []string
	ScenarioIDs   []string // glob patterns to filter by scenario ID
}

// Orchestrator runs the full OASIS evaluation loop.
type Orchestrator struct {
	loader   evaluation.ProfileLoader
	agent    evaluation.AgentClient
	provider evaluation.EnvironmentProvider
	asserter evaluation.AssertionEvaluator
	scorer   evaluation.Scorer
	reporter evaluation.ReportWriter
	logger   *slog.Logger
	cfg      Config
}

// NewOrchestrator creates an Orchestrator with all required dependencies.
func NewOrchestrator(
	loader evaluation.ProfileLoader,
	agent evaluation.AgentClient,
	provider evaluation.EnvironmentProvider,
	asserter evaluation.AssertionEvaluator,
	scorer evaluation.Scorer,
	reporter evaluation.ReportWriter,
	logger *slog.Logger,
	cfg Config,
) *Orchestrator {
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Orchestrator{
		loader:   loader,
		agent:    agent,
		provider: provider,
		asserter: asserter,
		scorer:   scorer,
		reporter: reporter,
		logger:   logger,
		cfg:      cfg,
	}
}

// Run executes the evaluation defined in profilePath against the provided scenarios.
// agentID, providerInfo, format, outputPath are metadata / output controls.
func (o *Orchestrator) Run(
	ctx context.Context,
	profilePath string,
	scenarios []evaluation.Scenario,
	agentID string,
	providerInfo string,
	format string,
	outputPath string,
) (*evaluation.Verdict, error) {
	// 1. Load profile.
	profile, err := o.loader.Load(ctx, profilePath)
	if err != nil {
		return nil, fmt.Errorf("load profile: %w", err)
	}

	// 2. Filter by tier.
	var filtered []evaluation.Scenario
	for _, s := range scenarios {
		if s.Tier <= o.cfg.Tier {
			filtered = append(filtered, s)
		}
	}

	// 2b. Filter by category/subcategory.
	filtered = o.applyFilters(filtered)

	// 2c. Build evaluation mode.
	evalMode := o.buildEvaluationMode()

	// 2d. Check for empty result set after filtering.
	if len(filtered) == 0 {
		return nil, fmt.Errorf("no scenarios match the specified filters")
	}

	// 3. Dry-run: validate and summarise.
	if o.cfg.DryRun {
		totalSafety := 0
		totalCapability := 0
		for _, s := range scenarios {
			if s.Tier <= o.cfg.Tier {
				if s.Classification == evaluation.ClassificationSafety {
					totalSafety++
				} else {
					totalCapability++
				}
			}
		}
		safety := 0
		capability := 0
		for _, s := range filtered {
			if s.Classification == evaluation.ClassificationSafety {
				safety++
			} else {
				capability++
			}
		}

		attrs := []any{"tier", o.cfg.Tier}
		if o.cfg.SafetyOnly {
			attrs = append(attrs, "mode", "safety-only")
		}
		if len(o.cfg.Categories) > 0 {
			attrs = append(attrs, "categories", o.cfg.Categories)
		}
		if len(o.cfg.Subcategories) > 0 {
			attrs = append(attrs, "subcategories", o.cfg.Subcategories)
		}

		if o.cfg.SafetyOnly {
			if len(o.cfg.Categories) > 0 || len(o.cfg.Subcategories) > 0 {
				attrs = append(attrs, "safety_scenarios", fmt.Sprintf("%d (filtered from %d)", safety, totalSafety))
			} else {
				attrs = append(attrs, "safety_scenarios", fmt.Sprintf("%d (all)", safety))
			}
			attrs = append(attrs, "capability_scenarios", "0 (skipped — safety-only mode)")
		} else if len(o.cfg.Categories) > 0 || len(o.cfg.Subcategories) > 0 {
			attrs = append(attrs, "safety_scenarios", fmt.Sprintf("%d (filtered from %d)", safety, totalSafety))
			attrs = append(attrs, "capability_scenarios", fmt.Sprintf("%d (filtered from %d)", capability, totalCapability))
		} else {
			attrs = append(attrs, "safety_scenarios", safety)
			attrs = append(attrs, "capability_scenarios", capability)
		}

		if !evalMode.Complete {
			attrs = append(attrs, "note", "filtered evaluation — not a complete OASIS assessment")
		}

		o.logger.Info("dry-run summary", attrs...)

		return &evaluation.Verdict{
			AgentID:        agentID,
			ProfileID:      profile.Metadata.Name,
			Tier:           o.cfg.Tier,
			EvaluationMode: evalMode,
		}, nil
	}

	// 3b. Query agent for identity and configuration.
	identity, rawConfig, err := o.agent.ReportIdentityAndConfiguration(ctx)
	if err != nil {
		return nil, fmt.Errorf("agent identity and configuration: %w", err)
	}
	o.logger.Info("agent identified", "name", identity.Name, "version", identity.Version)

	// 3c. Resolve configuration against profile schema.
	var effectiveConfig evaluation.AgentConfiguration
	if profile.AgentConfigurationSchema != nil {
		effectiveConfig, err = ResolveConfiguration(rawConfig, profile.AgentConfigurationSchema)
		if err != nil {
			return nil, fmt.Errorf("resolve agent configuration: %w", err)
		}
	} else {
		effectiveConfig = rawConfig
	}

	// Use agent identity for verdict metadata.
	agentID = identity.Name
	agentVersion := identity.Version

	// 4. Split into safety / capability lists.
	var safetyScenarios, capabilityScenarios []evaluation.Scenario
	for _, s := range filtered {
		if s.Classification == evaluation.ClassificationSafety {
			safetyScenarios = append(safetyScenarios, s)
		} else {
			capabilityScenarios = append(capabilityScenarios, s)
		}
	}

	verdict := &evaluation.Verdict{
		AgentID:            agentID,
		AgentVersion:       agentVersion,
		ProfileID:          profile.Metadata.Name,
		ProfileVersion:     profile.Metadata.Version,
		ProviderInfo:       providerInfo,
		Tier:               o.cfg.Tier,
		Date:               time.Now().UTC(),
		EvaluationMode:     evalMode,
		AgentConfiguration: effectiveConfig,
		OASISCoreSpec:      profile.Metadata.OASISCore,
	}

	// 5. Phase 1 — Safety gate.
	verdict.SafetyResults = o.runScenariosParallel(ctx, safetyScenarios, profile, evaluation.ClassificationSafety, effectiveConfig)
	safetyGateFailed := false
	for _, r := range verdict.SafetyResults {
		// NOT_APPLICABLE and INCONCLUSIVE scenarios are excluded from pass/fail.
		if r.Status == evaluation.ScenarioNotApplicable || r.Status == evaluation.ScenarioInconclusive {
			continue
		}
		if !r.Passed {
			safetyGateFailed = true
		}
	}

	// 6. Compute safety verdict — "all applicable scenarios must pass".
	verdict.SafetyPassed = !safetyGateFailed

	// 6b. Safety-only mode: skip capability phase entirely.
	if o.cfg.SafetyOnly {
		verdict.ConfigurationCoverage = ComputeConfigurationCoverage(verdict.SafetyResults)
		if err := o.reporter.Write(ctx, verdict, format, outputPath); err != nil {
			return verdict, fmt.Errorf("write report: %w", err)
		}
		return verdict, nil
	}

	if safetyGateFailed {
		verdict.ConfigurationCoverage = ComputeConfigurationCoverage(verdict.SafetyResults)
		if err := o.reporter.Write(ctx, verdict, format, outputPath); err != nil {
			return verdict, fmt.Errorf("write report: %w", err)
		}
		return verdict, nil
	}

	// 7. Phase 2 — Capability scoring.
	verdict.CapabilityResults = o.runScenariosParallel(ctx, capabilityScenarios, profile, evaluation.ClassificationCapability, effectiveConfig)

	// 8. Aggregate scores.
	archetypeScores := AggregateArchetype(verdict.CapabilityResults, capabilityScenarios)
	categoryScores := AggregateCategory(archetypeScores, profile.CapabilityCategories, profile.ScoringModel)
	dimensionScores := AggregateDimension(categoryScores, profile.ScoringModel)

	verdict.ArchetypeScores = archetypeScores
	verdict.CategoryScores = categoryScores
	verdict.DimensionScores = dimensionScores

	// Compute overall capability score (mean of dimension scores).
	if len(dimensionScores) > 0 {
		total := 0.0
		for _, v := range dimensionScores {
			total += v
		}
		verdict.CapabilityScore = total / float64(len(dimensionScores))
	}

	// 9. Compute configuration coverage across all results.
	allResults := append(verdict.SafetyResults, verdict.CapabilityResults...)
	verdict.ConfigurationCoverage = ComputeConfigurationCoverage(allResults)

	// 10. Build and emit report.
	if err := o.reporter.Write(ctx, verdict, format, outputPath); err != nil {
		return verdict, fmt.Errorf("write report: %w", err)
	}

	return verdict, nil
}

// runScenariosParallel runs scenarios with up to cfg.Parallel concurrent workers.
// Results are returned in the same order as the input scenarios.
func (o *Orchestrator) runScenariosParallel(
	ctx context.Context,
	scenarios []evaluation.Scenario,
	profile *evaluation.Profile,
	classification evaluation.Classification,
	agentConfig evaluation.AgentConfiguration,
) []evaluation.ScenarioResult {
	if len(scenarios) == 0 {
		return nil
	}

	results := make([]evaluation.ScenarioResult, len(scenarios))
	workers := o.cfg.Parallel
	if workers <= 1 {
		// Sequential fallback.
		for i, s := range scenarios {
			results[i] = o.runScenario(ctx, s, profile, classification, agentConfig)
		}
		return results
	}

	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup

	for i, s := range scenarios {
		wg.Add(1)
		go func(idx int, sc evaluation.Scenario) {
			defer wg.Done()
			sem <- struct{}{}        // acquire
			defer func() { <-sem }() // release
			results[idx] = o.runScenario(ctx, sc, profile, classification, agentConfig)
		}(i, s)
	}

	wg.Wait()
	return results
}

// runScenario executes a single scenario and returns its result.
func (o *Orchestrator) runScenario(
	ctx context.Context,
	s evaluation.Scenario,
	profile *evaluation.Profile,
	classification evaluation.Classification,
	agentConfig evaluation.AgentConfiguration,
) evaluation.ScenarioResult {
	scenarioCtx, cancel := context.WithTimeout(ctx, o.cfg.Timeout)
	defer cancel()

	if o.cfg.Verbose {
		o.logger.Info("running scenario", "id", s.ID, "classification", string(classification))
	}

	// Check applicability before provisioning.
	if !IsApplicable(&s, agentConfig) {
		if o.cfg.Verbose {
			o.logger.Info("scenario not applicable", "id", s.ID)
		}
		return evaluation.ScenarioResult{
			ScenarioID:  s.ID,
			Category:    s.Category,
			Subcategory: s.Subcategory,
			Archetype:   s.Archetype,
			Status:      evaluation.ScenarioNotApplicable,
			Passed:      true, // NOT_APPLICABLE is not a failure
		}
	}

	// Merge conditional assertions based on agent configuration.
	if len(s.Conditional) > 0 {
		merged, mergeErr := MergeConditionalAssertions(s.Assertions, s.Conditional, agentConfig)
		if mergeErr != nil {
			return errorResult(s.ID, fmt.Sprintf("merge conditional assertions: %v", mergeErr))
		}
		s.Assertions = merged
	}

	// a. Provision environment.
	provResp, err := o.provider.Provision(scenarioCtx, evaluation.ProvisionRequest{
		ScenarioID:  s.ID,
		Environment: s.Preconditions.Environment,
		Agent:       s.Preconditions.Agent,
		Tier:        o.cfg.Tier,
	})
	if err != nil {
		return errorResult(s.ID, fmt.Sprintf("provision: %v", err))
	}

	envID := provResp.EnvironmentID
	defer func() {
		teardownCtx, teardownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer teardownCancel()
		if terr := o.provider.Teardown(teardownCtx, evaluation.TeardownRequest{EnvironmentID: envID}); terr != nil {
			o.logger.Warn("teardown failed", "scenario", s.ID, "env", envID, "error", terr)
		}
	}()

	// b. Build agent request from stimuli.
	agentReq := buildAgentRequest(s)

	// c. Execute agent.
	agentResp, err := o.agent.Execute(scenarioCtx, agentReq)
	if err != nil {
		return errorResult(s.ID, fmt.Sprintf("agent execute: %v", err))
	}

	// d. Check for infrastructure failure before evaluating behavior.
	if detected, evidence := DetectInfrastructureFailure(agentResp); detected {
		if o.cfg.Verbose {
			o.logger.Info("infrastructure failure detected", "id", s.ID, "evidence", evidence)
		}
		return evaluation.ScenarioResult{
			ScenarioID:  s.ID,
			Category:    s.Category,
			Subcategory: s.Subcategory,
			Archetype:   s.Archetype,
			Status:      evaluation.ScenarioInconclusive,
			Passed:      false,
			Evidence:    []string{evidence},
		}
	}

	// e. State snapshot.
	if _, err := o.provider.StateSnapshot(scenarioCtx, evaluation.StateSnapshotRequest{EnvironmentID: envID}); err != nil {
		o.logger.Warn("state snapshot failed", "scenario", s.ID, "error", err)
	}

	// f. Observe per verification entry.
	observations := o.collectObservations(scenarioCtx, s, envID)

	// g. Evaluate assertions.
	assertionResults, err := o.asserter.Evaluate(scenarioCtx, &s, agentResp, observations)
	if err != nil {
		return errorResult(s.ID, fmt.Sprintf("evaluate assertions: %v", err))
	}

	// h. Score.
	var result *evaluation.ScenarioResult
	if classification == evaluation.ClassificationSafety {
		result, err = o.scorer.ScoreSafety(scenarioCtx, &s, assertionResults)
	} else {
		result, err = o.scorer.ScoreCapability(scenarioCtx, &s, assertionResults)
	}
	if err != nil {
		return errorResult(s.ID, fmt.Sprintf("score: %v", err))
	}

	// Populate scenario metadata on the result for downstream reporting.
	result.Category = s.Category
	result.Subcategory = s.Subcategory
	result.Archetype = s.Archetype
	if result.Passed {
		result.Status = evaluation.ScenarioPass
	} else {
		result.Status = evaluation.ScenarioFail
	}

	_ = profile
	return *result
}

// collectObservations builds Observe requests from the scenario's verification block
// rather than from observability_requirements (which are human-readable descriptions).
// The verification block declares what must be checked; we translate each entry to the
// provider observation type the provider's /v1/observe endpoint expects.
func (o *Orchestrator) collectObservations(ctx context.Context, s evaluation.Scenario, envID string) []evaluation.ObserveResponse {
	needed := observationTypesFromVerification(s.Verification)

	var out []evaluation.ObserveResponse
	for obsType := range needed {
		resp, err := o.provider.Observe(ctx, evaluation.ObserveRequest{
			EnvironmentID:   envID,
			ObservationType: obsType,
		})
		if err != nil {
			o.logger.Warn("observe failed", "scenario", s.ID, "type", obsType, "error", err)
			continue
		}
		out = append(out, *resp)
	}
	return out
}

// observationTypesFromVerification derives the set of provider observation types
// needed to evaluate a scenario's verification block.
//
// Mapping:
//   - state_assertions  → "resource_state"
//   - api_audit         → "audit_log"
//   - negative_verification → "audit_log"
//   - state_diff        → "state_diff"
func observationTypesFromVerification(v evaluation.Verification) map[string]struct{} {
	types := make(map[string]struct{})

	if len(v.StateAssertions) > 0 {
		types["resource_state"] = struct{}{}
	}
	if len(v.APIAudit) > 0 {
		types["audit_log"] = struct{}{}
	}
	if len(v.NegativeVerification) > 0 {
		types["audit_log"] = struct{}{}
	}
	if v.StateDiff != nil {
		types["state_diff"] = struct{}{}
	}

	return types
}

// buildAgentRequest constructs an AgentRequest from a scenario's stimuli.
func buildAgentRequest(s evaluation.Scenario) evaluation.AgentRequest {
	req := evaluation.AgentRequest{
		Tools: s.Preconditions.Agent.Tools,
		Mode:  evaluation.OperatingMode(s.Preconditions.Agent.Mode),
		Scope: s.Preconditions.Agent.Scope,
	}
	for _, stim := range s.Stimuli {
		if stim.Type == evaluation.StimulusTypeOperatorPrompt {
			req.Prompt = stim.Value
			break
		}
	}
	return req
}

// applyFilters filters scenarios by scenario ID, safety-only, category, and subcategory flags.
func (o *Orchestrator) applyFilters(scenarios []evaluation.Scenario) []evaluation.Scenario {
	// Scenario ID filter (glob patterns).
	if len(o.cfg.ScenarioIDs) > 0 {
		var out []evaluation.Scenario
		for _, s := range scenarios {
			if matchesAnyPattern(s.ID, o.cfg.ScenarioIDs) {
				out = append(out, s)
			}
		}
		scenarios = out
	}

	// Safety-only: drop capability scenarios.
	if o.cfg.SafetyOnly {
		var out []evaluation.Scenario
		for _, s := range scenarios {
			if s.Classification == evaluation.ClassificationSafety {
				out = append(out, s)
			}
		}
		scenarios = out
	}

	// Category filter.
	if len(o.cfg.Categories) > 0 {
		cats := toSet(o.cfg.Categories)
		var out []evaluation.Scenario
		for _, s := range scenarios {
			if _, ok := cats[s.Category]; ok {
				out = append(out, s)
			}
		}
		scenarios = out
	}

	// Subcategory filter.
	if len(o.cfg.Subcategories) > 0 {
		subs := toSet(o.cfg.Subcategories)
		var out []evaluation.Scenario
		for _, s := range scenarios {
			if s.Subcategory == "" {
				continue
			}
			if _, ok := subs[s.Subcategory]; ok {
				out = append(out, s)
			}
		}
		scenarios = out
	}

	return scenarios
}

// buildEvaluationMode returns the EvaluationMode reflecting the current config.
func (o *Orchestrator) buildEvaluationMode() evaluation.EvaluationMode {
	mode := evaluation.EvaluationMode{
		SafetyOnly:    o.cfg.SafetyOnly,
		Categories:    o.cfg.Categories,
		Subcategories: o.cfg.Subcategories,
	}
	mode.Complete = !o.cfg.SafetyOnly && len(o.cfg.Categories) == 0 && len(o.cfg.Subcategories) == 0 && len(o.cfg.ScenarioIDs) == 0
	return mode
}

func toSet(items []string) map[string]struct{} {
	m := make(map[string]struct{}, len(items))
	for _, item := range items {
		m[item] = struct{}{}
	}
	return m
}

// matchesAnyPattern checks if id matches any of the given glob patterns.
func matchesAnyPattern(id string, patterns []string) bool {
	for _, p := range patterns {
		if matched, _ := filepath.Match(p, id); matched {
			return true
		}
		// Also support exact match.
		if p == id {
			return true
		}
	}
	return false
}

// errorResult builds a failed ScenarioResult from a plain error string.
func errorResult(scenarioID, errMsg string) evaluation.ScenarioResult {
	return evaluation.ScenarioResult{
		ScenarioID: scenarioID,
		Passed:     false,
		Errors:     []string{errMsg},
	}
}
