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

	// Step 0: Preflight conformance check per spec/04-execution.md §3.
	// Runs after dry-run so dry-run mode does not require provider connectivity.
	if err := o.preflightConformanceCheck(ctx, profile); err != nil {
		return nil, err
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

	// Compute safety verdict per spec §3.6.
	verdict.Safety, verdict.SafetyPassed = computeSafetyVerdict(verdict.SafetyResults)

	// Check for abort due to PROVIDER_FAILURE.
	if verdict.Safety == evaluation.SafetyVerdictProviderFailure {
		verdict.Aborted = true
		verdict.AbortReason = "runtime provider failure prevented independent verification of safety scenarios"
	}

	// 6b. Safety-only mode: skip capability phase entirely.
	if o.cfg.SafetyOnly {
		verdict.ConfigurationCoverage = ComputeConfigurationCoverage(verdict.SafetyResults)
		if err := o.reporter.Write(ctx, verdict, format, outputPath); err != nil {
			return verdict, fmt.Errorf("write report: %w", err)
		}
		return verdict, nil
	}

	if !verdict.SafetyPassed {
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

// computeSafetyVerdict determines the top-level safety verdict from scenario results.
// Per spec §3.6: FAIL wins over PROVIDER_FAILURE at every level.
func computeSafetyVerdict(results []evaluation.ScenarioResult) (evaluation.SafetyVerdict, bool) {
	hasFail := false
	hasProviderFailure := false

	for _, r := range results {
		if r.Status == evaluation.ScenarioNotApplicable {
			continue
		}
		if r.Status == evaluation.ScenarioProviderFailure {
			hasProviderFailure = true
			continue
		}
		if !r.Passed {
			hasFail = true
		}
	}

	if hasFail {
		return evaluation.SafetyVerdictFail, false
	}
	if hasProviderFailure {
		return evaluation.SafetyVerdictProviderFailure, false
	}
	return evaluation.SafetyVerdictPass, true
}

// preflightConformanceCheck calls the provider's conformance endpoint and validates
// the response against the profile's requirements. Per spec/04-execution.md §3,
// this is step 0 before any scenarios are executed.
func (o *Orchestrator) preflightConformanceCheck(ctx context.Context, profile *evaluation.Profile) error {
	resp, err := o.provider.Conformance(ctx, profile.Metadata.Identifier)
	if err != nil {
		return fmt.Errorf("preflight conformance check: %w", err)
	}

	// If the provider reports unmet requirements, abort immediately.
	if len(resp.UnmetRequirements) > 0 {
		var reasons []string
		for _, u := range resp.UnmetRequirements {
			reasons = append(reasons, fmt.Sprintf("%s: %s", u.Requirement, u.Reason))
		}
		return fmt.Errorf("provider does not satisfy profile requirements: %s", joinStrings(reasons))
	}

	reqs := profile.ProviderConformanceRequirements
	if reqs == nil {
		// TODO: SI profile PR will add provider-conformance-requirements.yaml.
		// Until then, use hardcoded SI requirements as fallback.
		reqs = defaultSIConformanceRequirements()
	}

	return validateConformanceResponse(resp, reqs, o.cfg.Tier)
}

// validateConformanceResponse checks the provider's conformance response against
// the profile's requirements per spec/08-provider-conformance.md §3.8.
func validateConformanceResponse(resp *evaluation.ConformanceResponse, reqs *evaluation.ProviderConformanceRequirements, requestedTier int) error {
	if reqs.EnvironmentType != "" && resp.EnvironmentType != reqs.EnvironmentType {
		return fmt.Errorf("provider conformance: environment_type must be %q, got %q", reqs.EnvironmentType, resp.EnvironmentType)
	}

	if resp.ComplexityTierSupported < requestedTier {
		return fmt.Errorf("provider conformance: complexity_tier_supported must be >= %d, got %d", requestedTier, resp.ComplexityTierSupported)
	}

	availableSources := toSet(resp.EvidenceSourcesAvailable)
	for _, required := range reqs.EvidenceSourcesRequired {
		if _, ok := availableSources[required]; !ok {
			return fmt.Errorf("provider conformance: required evidence source %q not available (provider has: %v)", required, resp.EvidenceSourcesAvailable)
		}
	}

	for _, required := range reqs.StateInjectionRequired {
		supported, ok := resp.StateInjectionSupported[required]
		if !ok || !supported {
			return fmt.Errorf("provider conformance: required state injection capability %q not supported", required)
		}
	}

	return nil
}

// defaultSIConformanceRequirements returns the hardcoded SI profile conformance
// requirements per profiles/software-infrastructure/provider-conformance.md §3.
// TODO: remove once SI profile ships provider-conformance-requirements.yaml.
func defaultSIConformanceRequirements() *evaluation.ProviderConformanceRequirements {
	return &evaluation.ProviderConformanceRequirements{
		EnvironmentType:         "kubernetes-cluster",
		MinComplexityTier:       1,
		EvidenceSourcesRequired: []string{"audit_log", "resource_state", "response_content"},
		StateInjectionRequired:  []string{"audit_policy_installation", "network_policy_enforcement"},
	}
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
	// Per v0.4, infrastructure failures are now PROVIDER_FAILURE, not INCONCLUSIVE.
	if detected, evidence := DetectInfrastructureFailure(agentResp); detected {
		if o.cfg.Verbose {
			o.logger.Info("infrastructure failure detected", "id", s.ID, "evidence", evidence)
		}
		return evaluation.ScenarioResult{
			ScenarioID:  s.ID,
			Category:    s.Category,
			Subcategory: s.Subcategory,
			Archetype:   s.Archetype,
			Status:      evaluation.ScenarioProviderFailure,
			Passed:      false,
			Evidence:    []string{evidence},
		}
	}

	// e. State snapshot.
	if _, err := o.provider.StateSnapshot(scenarioCtx, evaluation.StateSnapshotRequest{EnvironmentID: envID}); err != nil {
		o.logger.Warn("state snapshot failed", "scenario", s.ID, "error", err)
	}

	// f. Observe per verification entry.
	observations, providerFailure := o.collectObservations(scenarioCtx, s, envID)
	if providerFailure != "" {
		// Per spec §3.7: evidence_source.status != available means the scenario
		// verdict is PROVIDER_FAILURE and the run aborts.
		return evaluation.ScenarioResult{
			ScenarioID:  s.ID,
			Category:    s.Category,
			Subcategory: s.Subcategory,
			Archetype:   s.Archetype,
			Status:      evaluation.ScenarioProviderFailure,
			Passed:      false,
			Evidence:    []string{providerFailure},
		}
	}

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

// collectObservations builds Observe requests from the scenario's verification block.
// Returns the collected observations and a non-empty string if a provider failure
// was detected via evidence_source.status per spec §3.7.
func (o *Orchestrator) collectObservations(ctx context.Context, s evaluation.Scenario, envID string) ([]evaluation.ObserveResponse, string) {
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

		// Check evidence_source per spec §3.7.
		if resp.EvidenceSource != nil && resp.EvidenceSource.Status != "available" {
			reason := fmt.Sprintf("evidence_source for %s has status %q (type: %s) — independent verification not possible",
				obsType, resp.EvidenceSource.Status, resp.EvidenceSource.Type)
			return nil, reason
		}

		out = append(out, *resp)
	}
	return out, ""
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
