package execution

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// Config controls orchestrator behaviour.
type Config struct {
	Tier     int
	Parallel int // reserved — currently always 1
	Timeout  time.Duration
	DryRun   bool
	Verbose  bool
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

	// 3. Dry-run: validate and summarise.
	if o.cfg.DryRun {
		safety := 0
		capability := 0
		for _, s := range filtered {
			if s.Classification == evaluation.ClassificationSafety {
				safety++
			} else {
				capability++
			}
		}
		o.logger.Info("dry-run summary",
			"tier", o.cfg.Tier,
			"safety_scenarios", safety,
			"capability_scenarios", capability,
		)
		return &evaluation.Verdict{
			AgentID:   agentID,
			ProfileID: profile.Metadata.Name,
			Tier:      o.cfg.Tier,
		}, nil
	}

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
		AgentID:        agentID,
		ProfileID:      profile.Metadata.Name,
		ProfileVersion: profile.Metadata.Version,
		ProviderInfo:   providerInfo,
		Tier:           o.cfg.Tier,
		Date:           time.Now().UTC(),
	}

	// 5. Phase 1 — Safety gate.
	safetyGateFailed := false
	for _, s := range safetyScenarios {
		result := o.runScenario(ctx, s, profile, evaluation.ClassificationSafety)
		verdict.SafetyResults = append(verdict.SafetyResults, result)
		if !result.Passed {
			safetyGateFailed = true
		}
	}

	// 6. Compute safety verdict.
	verdict.SafetyPassed = !safetyGateFailed
	if safetyGateFailed {
		if err := o.reporter.Write(ctx, verdict, format, outputPath); err != nil {
			o.logger.Error("failed to write report", "error", err)
		}
		return verdict, nil
	}

	// 7. Phase 2 — Capability scoring.
	for _, s := range capabilityScenarios {
		result := o.runScenario(ctx, s, profile, evaluation.ClassificationCapability)
		verdict.CapabilityResults = append(verdict.CapabilityResults, result)
	}

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

	// 9. Build and emit report.
	if err := o.reporter.Write(ctx, verdict, format, outputPath); err != nil {
		return verdict, fmt.Errorf("write report: %w", err)
	}

	return verdict, nil
}

// runScenario executes a single scenario and returns its result.
func (o *Orchestrator) runScenario(
	ctx context.Context,
	s evaluation.Scenario,
	profile *evaluation.Profile,
	classification evaluation.Classification,
) evaluation.ScenarioResult {
	scenarioCtx, cancel := context.WithTimeout(ctx, o.cfg.Timeout)
	defer cancel()

	if o.cfg.Verbose {
		o.logger.Info("running scenario", "id", s.ID, "classification", string(classification))
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

	// d. State snapshot.
	if _, err := o.provider.StateSnapshot(scenarioCtx, evaluation.StateSnapshotRequest{EnvironmentID: envID}); err != nil {
		o.logger.Warn("state snapshot failed", "scenario", s.ID, "error", err)
	}

	// e. Observe per verification entry.
	observations := o.collectObservations(scenarioCtx, s, envID)

	// f. Evaluate assertions.
	assertionResults, err := o.asserter.Evaluate(scenarioCtx, &s, agentResp, observations)
	if err != nil {
		return errorResult(s.ID, fmt.Sprintf("evaluate assertions: %v", err))
	}

	// g. Score.
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

	_ = profile
	return *result
}

// collectObservations calls provider.Observe for each observability requirement in the scenario.
func (o *Orchestrator) collectObservations(ctx context.Context, s evaluation.Scenario, envID string) []evaluation.ObserveResponse {
	var out []evaluation.ObserveResponse
	for _, obsType := range s.Observability {
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

// errorResult builds a failed ScenarioResult from a plain error string.
func errorResult(scenarioID, errMsg string) evaluation.ScenarioResult {
	return evaluation.ScenarioResult{
		ScenarioID: scenarioID,
		Passed:     false,
		Errors:     []string{errMsg},
	}
}
