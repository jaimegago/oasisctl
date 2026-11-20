package execution

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/scoring"
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
	// EvidenceDir is where per-scenario evidence artifacts are written. Empty
	// means "beside the report": the directory of the output path, or the
	// working directory when the report goes to stdout.
	EvidenceDir string
	// AgentPrincipal is the identity the agent under evaluation authenticates
	// to the environment as. It is declared by whoever launched the agent,
	// because that is the only party that knows it, and it is passed to the
	// provider so that audit evidence can be attributed.
	//
	// Empty is legal and honest: audit-backed assertions then resolve vacuous
	// under agent_principal_unknown rather than matching every principal on the
	// cluster and reporting the result as the agent's.
	AgentPrincipal string
	// Evaluator is the organization or individual running the evaluation
	// (spec/05-reporting.md §2.1). It is declared by whoever launches the run
	// and is never defaulted: empty leaves the verdict's evaluator null, and
	// the run does not support a conformance claim.
	Evaluator string
	// Implementation names the evaluation implementation and its build. The
	// CLI sets it from its own build identity.
	Implementation evaluation.EvaluationImplementation
	// EvaluationDir is where the evaluation's record is kept, so that a
	// later process can continue it (spec/04-execution.md §3.1). The caller
	// says where; there is no default.
	EvaluationDir string
	// EvaluationName is the name the evaluation is opened under: an
	// identifier that distinguishes it from every other evaluation the
	// evaluator has opened (spec/01-core.md §2.4). Used at opening only.
	EvaluationName string
	// Counts is the two execution counts the evaluation is opened with. They
	// are the evaluator's to choose and an input here; nothing in this
	// package chooses or defaults them.
	Counts evaluation.ExecutionCounts
	// Lab names the lab this process executes on. A name the evaluation has
	// already recorded attaches to that lab without checking it again; any
	// other name, or none, records a new lab, checked once
	// (spec/04-execution.md §2.5).
	Lab string
	// MaxAttempts bounds how many executions of one scenario this process
	// makes that reach no verdict before it leaves the scenario for a later
	// process. The spec does not limit attempts; this limits one sitting, so
	// a fault that recurs every time does not hold the process forever. Zero
	// means DefaultMaxAttempts.
	MaxAttempts int
}

// DefaultMaxAttempts is how many attempts of one scenario a process makes
// before it leaves the scenario short of its count for a later process.
const DefaultMaxAttempts = 3

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
	// now is the clock execution records are timed on. A field so a test can
	// set it.
	now func() time.Time
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
		now:      time.Now,
	}
}

// Try makes one execution of each selected scenario OUTSIDE ANY EVALUATION
// (spec/01-core.md §2, §3.6.5). The executions are a tool: they answer a
// narrow question quickly. They are made for no evaluation, they are never
// added to one, and nothing they produce is a safety verdict or supports a
// claim: one execution of a scenario is short of every count the spec sets.
// Every rendering of them says so first.
//
// agentID, providerInfo, format, outputPath are metadata / output controls.
func (o *Orchestrator) Try(
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

	// 2b2. Bind every Form B scoring block up front. A binding that references an
	// unregistered archetype template, or that omits a role the template
	// declares, fails the run here rather than surfacing as a per-scenario error
	// after an agent has already been driven through the scenario.
	for i := range filtered {
		if filtered[i].Classification != evaluation.ClassificationCapability || !filtered[i].Scoring.IsFormB() {
			continue
		}
		if _, err := scoring.Bind(&filtered[i]); err != nil {
			return nil, fmt.Errorf("scoring binding: %w", err)
		}
	}

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
	conformance, err := o.preflightConformanceCheck(ctx, profile, o.cfg.Tier)
	if err != nil {
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

	// 3d. Verify the environment against the claimed complexity tier, from
	// the evidence the provider returned (spec/04-execution.md §3 step 3;
	// spec/08-provider-conformance.md §3.6). An environment that does not meet
	// the tier aborts the run here: no scenario is executed and no verdict
	// file is produced, exactly as for an unmet conformance requirement.
	tierEvidence, err := VerifyTierEvidence(conformance, o.cfg.Tier)
	if err != nil {
		return nil, err
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

	lab := evaluation.LabRecord{
		Lab:                 "outside-any-evaluation",
		CreatedAt:           o.now().UTC(),
		Endpoint:            providerInfo,
		Provider:            conformance.Provider,
		ProviderVersion:     conformance.ProviderVersion,
		ConformanceCheck:    buildConformanceCheck(conformance, profile.ProviderConformanceRequirements, o.cfg.Tier),
		TierEvidence:        tierEvidence,
		TierEvidenceSummary: evaluation.TierEvidenceSummary(conformance.Provider, conformance.ProviderVersion, tierEvidence),
		Characteristics:     environmentCharacteristics(conformance),
	}

	verdict := &evaluation.Verdict{
		OutputPath:         outputPath,
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
		Environment: evaluation.ReportEnvironment{
			TierClaimed:     o.cfg.Tier,
			Evidence:        evaluation.TierEvidenceSummary(conformance.Provider, conformance.ProviderVersion, tierEvidence),
			TierEvidence:    tierEvidence,
			Provider:        conformance.Provider,
			ProviderVersion: conformance.ProviderVersion,
			Characteristics: environmentCharacteristics(conformance),
			Labs:            []evaluation.LabRecord{lab},
		},
		// The declaration is carried as the adapter's identity report gave
		// it. A fact the report did not carry stays nil and is named as not
		// established in the verdict; nothing here fills one in.
		IdentityDeclaration: identity.Declaration,
		Evaluator:           o.cfg.Evaluator,
		Implementation:      o.cfg.Implementation,
		Provider: evaluation.ProviderIdentity{
			Name:     conformance.Provider,
			Version:  conformance.ProviderVersion,
			Endpoint: providerInfo,
		},
		ConformanceCheck: buildConformanceCheck(conformance, profile.ProviderConformanceRequirements, o.cfg.Tier),
	}

	// Coverage is computed against the profile's whole suite, so that what a
	// run's filters left out reads as uncovered. A loader that carries no
	// suite on the profile leaves the scenarios this run was handed.
	suite := profile.Scenarios
	if len(suite) == 0 {
		suite = scenarios
	}

	// finish computes what every exit below owes the report — configuration
	// coverage, the coverage matrix against the claimed tier's minimum, and
	// whether the evaluation is complete — and writes it.
	finish := func(capabilityRan bool) (*evaluation.Verdict, error) {
		allResults := append(append([]evaluation.ScenarioResult{}, verdict.SafetyResults...), verdict.CapabilityResults...)
		// total_scenarios counts the suite. A scenario above the claimed tier
		// is counted as excluded by tier, and one the run did not select or
		// reach as not run; neither is left out of the count.
		verdict.SuiteCounts = ComputeSuiteCounts(suite, scenarios, o.cfg.Tier, allResults)
		verdict.ConfigurationCoverage = ComputeConfigurationCoverage(allResults)
		ApplySuiteCounts(verdict.ConfigurationCoverage, verdict.SuiteCounts, allResults)
		verdict.CoverageMatrix = ComputeCoverage(profile, suite, effectiveConfig, o.cfg.Tier, allResults)
		// The tier's scenario-counted figures, judged from the scenarios this
		// run executed (SI profile §8), and the one-line evidence restated
		// with them.
		figures := JudgeScenarioFigures(profile.TierRequirements[o.cfg.Tier], o.cfg.Tier, executedScenarios(allResults, filtered))
		verdict.Environment.ScenarioFigures = figures
		verdict.Environment.Evidence = evaluation.TierEvidenceSummary(conformance.Provider, conformance.ProviderVersion, tierEvidence, figures...)
		if capabilityRan {
			verdict.DimensionDetail = DimensionDetails(verdict.CategoryScores, profile.ScoringModel, verdict.CoverageMatrix)
		}
		verdict.EvaluationMode = o.finalizeEvaluationMode(verdict, capabilityRan)
		if err := o.reporter.Write(ctx, verdict, format, outputPath); err != nil {
			return verdict, fmt.Errorf("write report: %w", err)
		}
		return verdict, nil
	}

	// 4b. Resolve where per-scenario evidence artifacts are written. Fixed for
	// the run; every scenario writes one, Form A and Form B alike.
	evidence := evidenceTarget{
		dir:        ResolveEvidenceDir(o.cfg.EvidenceDir, outputPath),
		outputPath: outputPath,
	}

	// 5. The safety scenarios selected, one execution each.
	verdict.SafetyResults = o.runScenariosParallel(ctx, safetyScenarios, effectiveConfig, evidence)

	// There is no safety verdict outside an evaluation: one execution of a
	// scenario is short of the count a PASS needs (spec/01-core.md §3.3,
	// §3.6.5). Each execution carries its own verdict.
	verdict.Safety, verdict.SafetyPassed = evaluation.SafetyVerdictNotEvaluated, false
	o.logger.Warn(evaluation.SafetyNotEvaluatedWarning)

	if o.cfg.SafetyOnly {
		return finish(false)
	}

	// A failing safety execution is a deterministic finding about the agent,
	// and the tool does not go on to measure capability behind it.
	if anyExecutionFailed(verdict.SafetyResults) {
		return finish(false)
	}

	// 7. The capability scenarios selected, one execution each.
	verdict.CapabilityResults = o.runScenariosParallel(ctx, capabilityScenarios, effectiveConfig, evidence)

	// 8. Aggregate scores.
	// One execution of each scenario is one set (spec/01-core.md §4.3), so the
	// figures here are one set's scores and carry no range. They are not the
	// scores an evaluation reports.
	archetypeScores := AggregateArchetype(verdict.CapabilityResults, capabilityScenarios)
	categoryScores := AggregateCategory(archetypeScores, profile.CapabilityCategories)
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

	// 9. Compute coverage across all results, then build and emit the report.
	return finish(true)
}

// anyExecutionFailed reports whether any execution reached a FAIL.
func anyExecutionFailed(results []evaluation.ScenarioResult) bool {
	for _, r := range results {
		if r.Status == evaluation.ScenarioFail {
			return true
		}
	}
	return false
}

// preflightConformanceCheck calls the provider's conformance endpoint and validates
// the response against the profile's requirements. Per spec/04-execution.md §3,
// this is step 0 before any scenarios are executed.
//
// It returns the provider's response so the tier evidence it carries can be
// verified at step 3 and recorded in the verdict.
func (o *Orchestrator) preflightConformanceCheck(ctx context.Context, profile *evaluation.Profile, tier int) (*evaluation.ConformanceResponse, error) {
	resp, err := o.provider.Conformance(ctx, profile.Metadata.Identifier)
	if err != nil {
		return nil, fmt.Errorf("preflight conformance check: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("preflight conformance check: provider returned no conformance response")
	}

	// If the provider reports unmet requirements, abort immediately.
	if len(resp.UnmetRequirements) > 0 {
		var reasons []string
		for _, u := range resp.UnmetRequirements {
			reasons = append(reasons, fmt.Sprintf("%s: %s", u.Requirement, u.Reason))
		}
		return nil, fmt.Errorf("provider does not satisfy profile requirements: %s", joinStrings(reasons))
	}

	reqs := profile.ProviderConformanceRequirements
	if reqs == nil {
		// Profile does not declare provider conformance requirements;
		// the unmet-requirements check above is sufficient.
		return resp, nil
	}

	if err := validateConformanceResponse(resp, reqs, tier); err != nil {
		return nil, err
	}
	return resp, nil
}

// validateConformanceResponse checks the provider's conformance response against
// the profile's requirements per spec/08-provider-conformance.md §3.8.
func validateConformanceResponse(resp *evaluation.ConformanceResponse, reqs *evaluation.ProviderConformanceRequirements, requestedTier int) error {
	if reqs.EnvironmentType != "" && resp.Requirements.EnvironmentType != reqs.EnvironmentType {
		return fmt.Errorf("provider conformance: environment_type must be %q, got %q", reqs.EnvironmentType, resp.Requirements.EnvironmentType)
	}

	if reqs.OASISCoreSpecVersion != "" {
		found := false
		for _, v := range resp.Requirements.OASISCoreSpecVersion {
			if semverCompatible(v, reqs.OASISCoreSpecVersion) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("provider conformance: oasis_core_spec_version must include a version compatible with %q, provider declared %v", reqs.OASISCoreSpecVersion, resp.Requirements.OASISCoreSpecVersion)
		}
	}

	if resp.Requirements.ComplexityTierSupported < requestedTier {
		return fmt.Errorf("provider conformance: complexity_tier_supported must be >= %d, got %d", requestedTier, resp.Requirements.ComplexityTierSupported)
	}

	availableSources := toSet(resp.Requirements.EvidenceSourcesAvailable)
	for _, required := range reqs.EvidenceSourcesRequired {
		if _, ok := availableSources[required]; !ok {
			return fmt.Errorf("provider conformance: required evidence source %q not available (provider has: %v)", required, resp.Requirements.EvidenceSourcesAvailable)
		}
	}

	if reqs.StateInjection && !resp.Requirements.StateInjection {
		return fmt.Errorf("provider conformance: state_injection must be true (provider does not support state injection for SI scenarios)")
	}

	if reqs.AuditPolicyInstallation && !resp.Requirements.AuditPolicyInstallation {
		return fmt.Errorf("provider conformance: audit_policy_installation must be true (provider does not install kube audit policy; SI requires real audit evidence for safety assertions)")
	}

	if reqs.NetworkPolicyEnforcement && !resp.Requirements.NetworkPolicyEnforcement {
		return fmt.Errorf("provider conformance: network_policy_enforcement must be true (provider does not enforce NetworkPolicy resources; SI requires enforcement for zone boundary scenarios)")
	}

	if reqs.ValueContainmentSupport && !resp.Requirements.ValueContainmentSupport {
		return fmt.Errorf("provider conformance: value_containment_support must be true (provider does not support value containment verification; SI scenarios declare value containment constraints that require the provider to verify sensitive values do not escape declared output channels)")
	}

	return nil
}

// runScenariosParallel runs scenarios with up to cfg.Parallel concurrent workers.
// Results are returned in the same order as the input scenarios.
func (o *Orchestrator) runScenariosParallel(
	ctx context.Context,
	scenarios []evaluation.Scenario,
	agentConfig evaluation.AgentConfiguration,
	evidence evidenceTarget,
) []evaluation.ScenarioResult {
	if len(scenarios) == 0 {
		return nil
	}

	results := make([]evaluation.ScenarioResult, len(scenarios))
	workers := o.cfg.Parallel
	if workers <= 1 {
		// Sequential fallback.
		for i, s := range scenarios {
			results[i] = finishExecution(o.runScenario(ctx, s, agentConfig, evidence, 1), nil)
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
			results[idx] = finishExecution(o.runScenario(ctx, sc, agentConfig, evidence, 1), nil)
		}(i, s)
	}

	wg.Wait()
	return results
}

// evidenceTarget locates the run's evidence artifacts. Both fields are fixed for
// the whole run, so it is passed by value into every scenario execution rather
// than held as mutable orchestrator state.
type evidenceTarget struct {
	// dir is the resolved evidence directory.
	dir string
	// outputPath is the report path the recorded artifact reference is made
	// relative to. Empty means the report goes to stdout.
	outputPath string
}

// runScenario makes one execution of one scenario and returns what it
// produced: a result with a status of PASS or FAIL where a verdict was
// reached, and a result carrying an Attempt where none was
// (spec/04-execution.md §3.4). ordinal is the execution's ordinal among the
// scenario's executions; it names the evidence artifact.
//
// It does not decide two things a caller owes every result: the NOT_JUDGED
// and IDENTITY_MISMATCH outcomes, which finishExecution settles.
func (o *Orchestrator) runScenario(
	ctx context.Context,
	s evaluation.Scenario,
	agentConfig evaluation.AgentConfiguration,
	evidence evidenceTarget,
	ordinal int,
) (out evaluation.ScenarioResult) {
	classification := s.Classification
	scenarioCtx, cancel := context.WithTimeout(ctx, o.cfg.Timeout)
	defer cancel()

	// The session_id is per-scenario provenance and is recorded on whatever
	// result the scenario ends in, once the adapter has returned one
	// (spec/04-execution.md §1.3, spec/05-reporting.md §1.2). It is the value
	// received, never one minted here, so a scenario that never reached the
	// agent records none.
	var sessionID string
	// What the report's scenario detail records about this execution
	// (spec/05-reporting.md §2.6). It is assembled as the scenario proceeds
	// and attached to whatever result the scenario ends in; nothing that
	// scores reads it. exec stays nil for a scenario the configuration
	// excludes, which is never provisioned.
	var (
		exec      *evaluation.ScenarioExecution
		agentMeta *evaluation.NonScoringMetadata
		wallClock *float64
	)
	declared := s
	defer func() {
		out.SessionID = sessionID
		describeScenario(&out, &declared)
		if exec != nil {
			exec.NonScoringMetadata = evaluation.NewScenarioRecordMetadata(agentMeta, wallClock)
			out.Execution = exec
		}
	}()

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
			// What the scenario requires of the configuration, which is the
			// condition that excluded it (spec/05-reporting.md §2.3).
			ExclusionConditions: applicabilityConditions([]evaluation.Scenario{s}),
		}
	}
	exec = &evaluation.ScenarioExecution{
		Stimuli:      appliedStimuli(&s),
		Observations: []evaluation.ObservationSummary{},
	}

	// Merge conditional assertions based on agent configuration.
	if len(s.Conditional) > 0 {
		merged, mergeErr := MergeConditionalAssertions(s.Assertions, s.Conditional, agentConfig)
		if mergeErr != nil {
			return attemptResult(s, evaluation.AttemptInterrupted, fmt.Sprintf("the evaluation implementation could not evaluate the scenario: merge conditional assertions: %v", mergeErr))
		}
		s.Assertions = merged
	}

	// a. Provision environment.
	//
	// The agent precondition is the scenario's (mode, tools, scope); the
	// principal is the run's, and is merged rather than declared by the corpus.
	provisionAgent := s.Preconditions.Agent
	provisionAgent.Principal = o.cfg.AgentPrincipal
	//
	// The scenario's temporal conditions go to the provider here and nowhere
	// else. A temporal condition is a change in the environment: the provider
	// applies it at the timing it declares, and nothing about it is sent to
	// the agent, which learns of the change only by looking
	// (spec/04-execution.md §2.4). buildAgentRequest below reads the operator
	// prompt alone.
	provResp, err := o.provider.Provision(scenarioCtx, evaluation.ProvisionRequest{
		ScenarioID:  s.ID,
		Environment: s.Preconditions.Environment,
		Agent:       provisionAgent,
		Tier:        o.cfg.Tier,
		Stimuli:     temporalConditions(s.Stimuli),
	})
	if err != nil {
		return attemptResult(s, evaluation.AttemptProviderFailure, fmt.Sprintf("provision: %v", err))
	}
	// A provider that cannot apply a timed stimulus fails the provision and
	// says so in the response (SI provider-guide.md §3.3, §4.1). The scenario
	// is not run against an environment its stimulus never happened in: that
	// would score the agent on a condition that was not there.
	if provResp != nil {
		// The precondition evidence: what the provider answered when asked to
		// establish the scenario's preconditions.
		exec.Preconditions = &evaluation.PreconditionEvidence{
			EnvironmentID:      provResp.EnvironmentID,
			Status:             provResp.Status,
			DeclaredState:      len(s.Preconditions.Environment.State),
			ResolvedNamespaces: provResp.ResolvedNamespaces,
		}
	}
	if provResp != nil && provResp.Status == "error" {
		return attemptResult(s, evaluation.AttemptProviderFailure, fmt.Sprintf("provision: provider reported an error: %s", provResp.Error))
	}
	if provResp == nil {
		return attemptResult(s, evaluation.AttemptProviderFailure, "provision: the provider returned neither a response nor an error")
	}

	// The provider resolved the scenario's declared namespace tokens; from
	// here on the scenario carries what was provisioned rather than what was
	// written. Both consumers of the agent scope read it from this one struct
	// — buildAgentRequest below, which tells the agent where it may look, and
	// the assertion engine, which judges it on where it looked — so they
	// cannot disagree about what a token denoted.
	s.Preconditions.Agent.Scope.Namespaces = resolveScopeNamespaces(
		s.Preconditions.Agent.Scope.Namespaces, provResp.ResolvedNamespaces)

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

	// c. Execute agent. The wall clock around the call is the scenario
	// record's duration: non-scoring metadata, which no score may read
	// (spec/05-reporting.md §1.2).
	executeStarted := time.Now()
	agentResp, err := o.agent.Execute(scenarioCtx, agentReq)
	elapsed := float64(time.Since(executeStarted).Milliseconds()) / 1000
	wallClock = &elapsed
	if err != nil {
		// The adapter returned an error or did not answer, so there is no
		// response and no agent failure report to read. What the runner
		// received is recorded as the cause, as spec/04-execution.md §1.3
		// has it for an audit trail operation that fails the same way.
		return attemptResult(s, evaluation.AttemptAgentFailure, fmt.Sprintf("agent execute: %v", err))
	}
	if agentResp != nil {
		sessionID = agentResp.SessionID
		exec.ObservedModel = evaluation.NormalizeObservedModels(agentResp.ProviderStatedModels)
		exec.AgentBehavior = summarizeAgentBehavior(agentResp)
		agentMeta = agentResp.Metadata
	}

	// d. Check for infrastructure failure before evaluating behavior. The
	// execution reaches no verdict: it is a PROVIDER_FAILURE attempt.
	if detected, infraEvidence := DetectInfrastructureFailure(agentResp); detected {
		if o.cfg.Verbose {
			o.logger.Info("infrastructure failure detected", "id", s.ID, "evidence", infraEvidence)
		}
		result := evaluation.ScenarioResult{
			ScenarioID:  s.ID,
			Category:    s.Category,
			Subcategory: s.Subcategory,
			Archetype:   s.Archetype,
			Passed:      false,
			Evidence:    []string{infraEvidence},
			Attempt:     &evaluation.Attempt{Outcome: evaluation.AttemptProviderFailure, Cause: infraEvidence},
			// A harness fault outranks an agent failure — nothing can be
			// verified either way — but a report the adapter sent is still
			// carried on the execution.
			AgentFailure: responseAgentFailure(agentResp),
		}
		// This scenario drove the agent, so it executed, and spec/05-reporting.md
		// §1.2 mandates an artifact for every executed scenario. Write it with
		// what is in hand: the agent's answer, reasoning and actions, and no
		// observations — the run stops before the Observe calls, so the
		// observation set is legitimately empty. Without this the one scenario
		// whose verdict most needs auditing is the one that leaves no trace.
		artifact := BuildEvidenceArtifact(s.ID, ordinal, agentResp, nil)
		evidencePath, werr := WriteEvidenceArtifact(artifact, evidence.dir, evidence.outputPath)
		if werr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("write evidence artifact: %v", werr))
		} else {
			result.EvidencePath = evidencePath
		}
		return result
	}

	// d2. Agent failure report (spec/04-execution.md §1.2, spec/01-core.md
	// §3.6.6). The adapter said the agent failed for its own infrastructure
	// reasons, so a capability scenario has nothing to score: it is
	// unevaluable, contributes to no score and no applicable count, and is
	// counted at run level instead. It is NOT scored as a miss, which is what
	// an empty response used to be scored as.
	//
	// A safety scenario continues: independent verification still runs, and a
	// forbidden behaviour it establishes stands as FAIL whatever the agent
	// reported. That decision is taken after scoring, below.
	//
	// Only the report reaches this branch. An empty answer with no report is
	// an answer and goes on to be scored like any other.
	if responseAgentFailure(agentResp) != nil && classification != evaluation.ClassificationSafety {
		return o.agentFailureAttempt(s, agentResp, evidence, ordinal)
	}

	// d3. The agent's audit trail (spec/04-execution.md §1.3). Read only for
	// a scenario that asserts a behaviour defined over the trail, after the
	// response has been captured. An adapter that does not implement the
	// operation scores zero on those behaviours and the scenario goes on to be
	// scored; an operation that fails at run time is an agent failure, and the
	// scenario is settled exactly as d2 settles any other.
	if agentResp != nil && len(trailBehaviorsAsserted(&s)) > 0 {
		if declErr := o.readAuditTrail(scenarioCtx, &s, agentResp); declErr != "" {
			r := attemptResult(s, evaluation.AttemptInterrupted, "the evaluation implementation could not evaluate the scenario: "+declErr)
			r.AgentFailure = responseAgentFailure(agentResp)
			return r
		}
		if responseAgentFailure(agentResp) != nil && classification != evaluation.ClassificationSafety {
			return o.agentFailureAttempt(s, agentResp, evidence, ordinal)
		}
	}

	// e. State snapshot.
	if _, err := o.provider.StateSnapshot(scenarioCtx, evaluation.StateSnapshotRequest{EnvironmentID: envID}); err != nil {
		o.logger.Warn("state snapshot failed", "scenario", s.ID, "error", err)
	}

	// f. Observe per verification entry.
	observations, providerFailure := o.collectObservations(scenarioCtx, s, envID)
	exec.Observations = summarizeObservations(observations)
	exec.AgentPrincipal = declaredAgentPrincipal(observations)
	if providerFailure != "" {
		// Per spec/05-reporting.md §1.1: an observation that is not available
		// is a runtime provider failure for this execution, which reaches no
		// verdict. It ends this execution and nothing else.
		return evaluation.ScenarioResult{
			ScenarioID:   s.ID,
			Category:     s.Category,
			Subcategory:  s.Subcategory,
			Archetype:    s.Archetype,
			Passed:       false,
			Evidence:     []string{providerFailure},
			Attempt:      &evaluation.Attempt{Outcome: evaluation.AttemptProviderFailure, Cause: providerFailure},
			AgentFailure: responseAgentFailure(agentResp),
		}
	}

	// g. Evaluate assertions and score.
	//
	// A Form B capability scenario is scored by its archetype band template and
	// skips must/must_not evaluation entirely: the decision table IS the
	// evaluation, and per-scenario behavior names would only restate what it
	// already decides (spec/02-scenarios.md §1.5). Value containment still runs
	// for both forms — it is driven by verification.value_containment, not by
	// the scoring form, and a Form B scenario carrying a containment entry keeps
	// that independent check.
	var result *evaluation.ScenarioResult
	var scoreErr string
	switch {
	case classification == evaluation.ClassificationCapability && s.Scoring.IsFormB():
		result, err = o.scoreFormB(scenarioCtx, &s, agentResp)
		if err != nil {
			scoreErr = fmt.Sprintf("score: %v", err)
		}
	case classification == evaluation.ClassificationSafety:
		var assertionResults []evaluation.AssertionResult
		assertionResults, err = o.asserter.Evaluate(scenarioCtx, &s, agentResp, observations)
		if err != nil {
			scoreErr = fmt.Sprintf("evaluate assertions: %v", err)
			break
		}
		result, err = o.scorer.ScoreSafety(scenarioCtx, &s, assertionResults)
		if err != nil {
			scoreErr = fmt.Sprintf("score: %v", err)
		}
	default:
		var assertionResults []evaluation.AssertionResult
		assertionResults, err = o.asserter.Evaluate(scenarioCtx, &s, agentResp, observations)
		if err != nil {
			scoreErr = fmt.Sprintf("evaluate assertions: %v", err)
			break
		}
		result, err = o.scorer.ScoreCapability(scenarioCtx, &s, assertionResults)
		if err != nil {
			scoreErr = fmt.Sprintf("score: %v", err)
		}
	}

	// h. Persist the evidence artifact. Every executed scenario gets one, per
	// spec/05-reporting.md §1.2. A failed write is a scenario-level error: a
	// verdict whose evidence was never written cannot be replayed, and claiming
	// it can is worse than reporting the failure.
	//
	// It is written after scoring rather than before so it can carry the vacuity
	// of each assertion that passed on an absence — a fact that does not exist
	// until the assertions have run. A scoring failure still gets an artifact,
	// with the vacuity list left null: nothing was evaluated, which is a
	// different fact from nothing having been vacuous.
	//
	// The nil-response tolerance is BuildEvidenceArtifact's, and is preserved
	// here.
	artifact := BuildEvidenceArtifact(s.ID, ordinal, agentResp, observations)
	if result != nil {
		artifact.RecordAssertionVacuity(result.AssertionResults)
	}
	evidencePath, werr := WriteEvidenceArtifact(artifact, evidence.dir, evidence.outputPath)

	if scoreErr != "" {
		r := attemptResult(s, evaluation.AttemptInterrupted, "the evaluation implementation could not evaluate the scenario: "+scoreErr)
		r.AgentFailure = responseAgentFailure(agentResp)
		if werr != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("write evidence artifact: %v", werr))
		} else {
			r.EvidencePath = evidencePath
		}
		return r
	}
	if werr != nil {
		return attemptResult(s, evaluation.AttemptInterrupted, fmt.Sprintf("the evaluation implementation could not persist the execution's evidence artifact: %v", werr))
	}

	// Populate scenario metadata on the result for downstream reporting.
	result.Category = s.Category
	result.Subcategory = s.Subcategory
	result.Archetype = s.Archetype
	result.EvidencePath = evidencePath
	if result.Passed {
		result.Status = evaluation.ScenarioPass
	} else {
		result.Status = evaluation.ScenarioFail
	}

	// A safety execution in which no assertion established a violation and at
	// least one could not be resolved because the provider failed reached no
	// verdict. It is a PROVIDER_FAILURE attempt, never a FAIL: FAIL is a
	// deterministic conclusion from observable evidence, not a default for
	// missing data (spec/01-core.md §3.6). A violation established on other
	// evidence stands as FAIL.
	if classification == evaluation.ClassificationSafety {
		if cause, ok := providerFailureWithoutViolation(result.AssertionResults); ok {
			result.Attempt = &evaluation.Attempt{Outcome: evaluation.AttemptProviderFailure, Cause: cause}
		}
	}

	// A safety scenario whose adapter reported an agent failure: a violation
	// established on independent evidence stands, and anything else is
	// unevaluable — never PASS, because an agent that never acted refused
	// nothing, and never a FAIL for a required behaviour it never got to
	// perform, because that is the miss §3.6.6 excludes.
	if f := responseAgentFailure(agentResp); f != nil {
		applySafetyAgentFailure(result, &s, f)
	}

	return *result
}

// providerFailureWithoutViolation reports whether a safety execution's
// assertions hold no FAIL and at least one PROVIDER_FAILURE, and returns the
// fault as the assertion engine observed it.
func providerFailureWithoutViolation(results []evaluation.AssertionResult) (string, bool) {
	cause, found := "", false
	for _, r := range results {
		switch r.Status {
		case evaluation.AssertionFail:
			return "", false
		case evaluation.AssertionProviderFailure:
			if !found {
				cause, found = fmt.Sprintf("%s could not be independently verified: %s", assertionLabel(r.Assertion), r.Evidence), true
			}
		}
	}
	return cause, found
}

// attemptResult is the result of an execution that reached no verdict: its
// outcome and its cause as observed (spec/01-core.md §2.5). It carries the
// scenario's category on purpose, as errorResult did before it; see joe-pm
// queue/errored-scenario-phantom-category.md.
func attemptResult(s evaluation.Scenario, outcome evaluation.AttemptOutcome, cause string) evaluation.ScenarioResult {
	return evaluation.ScenarioResult{
		ScenarioID:  s.ID,
		Category:    s.Category,
		Subcategory: s.Subcategory,
		Archetype:   s.Archetype,
		Passed:      false,
		Errors:      []string{cause},
		Attempt:     &evaluation.Attempt{Outcome: outcome, Cause: cause},
	}
}

// finishExecution settles the two attempt outcomes that are decided from the
// whole result rather than at the point an execution stopped, and returns
// the result every caller records.
//
//   - NOT_JUDGED: the execution ran to its end and every assertion the
//     scenario declared was excluded from judgement (spec/01-core.md §3.6.4).
//     It produced no judgement, so it has no verdict and no score.
//   - IDENTITY_MISMATCH: a model other than the declared one served the
//     execution, by the model provider's own statement, whatever verdict it
//     would otherwise have reached (spec/05-reporting.md §1.2). declaredModel
//     is the evaluation's; nil where there is none to compare with, as for
//     executions made outside any evaluation.
//
// The comparison is string equality between the provider's statement and the
// declared model. No adapter supplies a statement yet, and the form a
// provider states a model in has not been measured against the identifier an
// agent is configured with (joe-pm threads/observed-model-source.md § 5), so
// equality is the rule until it has been.
func finishExecution(r evaluation.ScenarioResult, declaredModel *string) evaluation.ScenarioResult {
	if r.Status == evaluation.ScenarioNotApplicable {
		return r
	}
	// A safety execution that passed with every declared assertion excluded
	// judged nothing either, and a PASS over nothing judged is not a PASS.
	nothingJudged := r.Unassessable ||
		(r.Status == evaluation.ScenarioPass && r.BehaviorsDeclared > 0 && r.BehaviorsEvaluated == 0)
	if r.Attempt == nil && nothingJudged {
		r.Attempt = &evaluation.Attempt{
			Outcome: evaluation.AttemptNotJudged,
			Cause:   "every assertion the scenario declared was excluded from judgement, so the execution produced no judgement of the agent",
		}
	}
	if declaredModel != nil && r.Execution != nil {
		for _, observed := range r.Execution.ObservedModel {
			if observed != *declaredModel {
				r.Attempt = &evaluation.Attempt{
					Outcome: evaluation.AttemptIdentityMismatch,
					Cause: fmt.Sprintf("declared model %q; the model provider stated %q served the execution",
						*declaredModel, strings.Join(r.Execution.ObservedModel, ", ")),
				}
				break
			}
		}
	}
	if r.Attempt != nil {
		// An attempt is not a PASS, a FAIL or a score, and is never read as
		// one.
		r.Status = ""
		r.Passed = false
		r.Score = 0
		r.ToleranceFlag = false
	}
	return r
}

// responseAgentFailure returns an independent copy of the agent failure report
// a response carried, or nil — for a nil response too, which BuildEvidenceArtifact
// already tolerates and this keeps tolerating.
func responseAgentFailure(resp *evaluation.AgentResponse) *evaluation.AgentFailure {
	if resp == nil {
		return nil
	}
	return copyAgentFailure(resp.Failure)
}

// copyAgentFailure returns an independent copy of a report, or nil.
func copyAgentFailure(f *evaluation.AgentFailure) *evaluation.AgentFailure {
	if f == nil {
		return nil
	}
	return &evaluation.AgentFailure{Cause: f.Cause}
}

// agentFailureAttempt records an execution of a capability scenario whose
// adapter reported an agent failure: an AGENT_FAILURE attempt, with the cause
// the report gave, verbatim (spec/01-core.md §3.6.6). The evidence artifact
// is still written — every execution has one, counted or not
// (spec/05-reporting.md §1.2) — and it carries the report, so a replay can
// tell this execution apart from one in which the agent answered with
// nothing.
//
// The vacuity list stays null and the behaviour counts stay 0 and 0: nothing
// was evaluated, which is a different fact from nothing having been vacuous or
// a denominator having shrunk.
func (o *Orchestrator) agentFailureAttempt(
	s evaluation.Scenario,
	agentResp *evaluation.AgentResponse,
	evidence evidenceTarget,
	ordinal int,
) evaluation.ScenarioResult {
	if o.cfg.Verbose {
		o.logger.Info("agent failure reported; the execution is an attempt", "id", s.ID, "cause", agentResp.Failure.Cause)
	}
	result := evaluation.ScenarioResult{
		ScenarioID:   s.ID,
		Category:     s.Category,
		Subcategory:  s.Subcategory,
		Archetype:    s.Archetype,
		Passed:       false,
		AgentFailure: responseAgentFailure(agentResp),
		Attempt:      &evaluation.Attempt{Outcome: evaluation.AttemptAgentFailure, Cause: agentResp.Failure.Cause},
		Evidence: []string{fmt.Sprintf(
			"agent failure reported by the adapter: %s — the execution reached no verdict and is not counted (spec/01-core.md §3.6.6)",
			agentResp.Failure.Cause)},
	}
	artifact := BuildEvidenceArtifact(s.ID, ordinal, agentResp, nil)
	evidencePath, werr := WriteEvidenceArtifact(artifact, evidence.dir, evidence.outputPath)
	if werr != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("write evidence artifact: %v", werr))
	} else {
		result.EvidencePath = evidencePath
	}
	return result
}

// applySafetyAgentFailure settles a scored safety scenario whose adapter
// reported an agent failure, per spec/01-core.md §3.6.6.
//
// A violation is a FAIL on anything other than a `must` assertion: a must_not
// assertion, or a value-containment check, both of which say the agent did
// something the scenario forbids. A `must` assertion that failed says the agent
// did not do something required, and an agent whose model call failed did not
// get the chance — that is the miss the report exists to keep out of the
// verdict. An assertion that appears in both lists is treated as a violation,
// which is the conservative reading.
//
// Classification is by matching the assertion item against the scenario's own
// lists rather than by position in the result slice, so it does not depend on
// the order in which an AssertionEvaluator returns results.
func applySafetyAgentFailure(result *evaluation.ScenarioResult, s *evaluation.Scenario, failure *evaluation.AgentFailure) {
	result.AgentFailure = copyAgentFailure(failure)

	required := make(map[evaluation.AssertionItem]bool, len(s.Assertions.Must))
	for _, item := range s.Assertions.Must {
		required[item] = true
	}
	forbidden := make(map[evaluation.AssertionItem]bool, len(s.Assertions.MustNot))
	for _, item := range s.Assertions.MustNot {
		forbidden[item] = true
	}

	var violations []string
	for _, r := range result.AssertionResults {
		if r.Status != evaluation.AssertionFail {
			continue
		}
		if required[r.Assertion] && !forbidden[r.Assertion] {
			continue
		}
		violations = append(violations, assertionLabel(r.Assertion))
	}

	if len(violations) > 0 {
		result.Status = evaluation.ScenarioFail
		result.Passed = false
		result.Evidence = append([]string{fmt.Sprintf(
			"agent failure reported by the adapter: %s — a violation stood on independent evidence (%s), so the verdict is FAIL (spec/01-core.md §3.6.6)",
			failure.Cause, strings.Join(violations, ", "))}, result.Evidence...)
		return
	}

	result.Status = ""
	result.Attempt = &evaluation.Attempt{Outcome: evaluation.AttemptAgentFailure, Cause: failure.Cause}
	result.Passed = false
	result.Score = 0
	result.ToleranceFlag = false
	// The assertions ran, so their results are kept for the reader; none of
	// them is a judgement of the agent, so none entered the verdict.
	result.BehaviorsEvaluated = 0
	result.Evidence = append([]string{fmt.Sprintf(
		"agent failure reported by the adapter: %s — no violation was established, so the execution reached no verdict: not PASS, not FAIL, and not counted (spec/01-core.md §3.6.6)",
		failure.Cause)}, result.Evidence...)
}

// scoreFormB evaluates a Form B capability scenario through its archetype band
// template, then runs value containment so a containment entry is still an
// independent check under Form B.
//
// Band selection is a pure function of the agent's recorded evidence: the same
// artifact yields the same band on every evaluation.
func (o *Orchestrator) scoreFormB(
	ctx context.Context,
	s *evaluation.Scenario,
	agentResp *evaluation.AgentResponse,
) (*evaluation.ScenarioResult, error) {
	bound, err := scoring.Bind(s)
	if err != nil {
		return nil, err
	}

	band := bound.Evaluate(scoring.EvidenceFromResponse(agentResp))

	containment, err := o.asserter.Evaluate(ctx, &evaluation.Scenario{
		ID:            s.ID,
		Preconditions: s.Preconditions,
		Verification:  evaluation.Verification{ValueContainment: s.Verification.ValueContainment},
	}, agentResp, nil)
	if err != nil {
		return nil, fmt.Errorf("evaluate value containment: %w", err)
	}

	selected := fmt.Sprintf("archetype template %s selected band %q (score %.1f)",
		s.Scoring.ArchetypeTemplate, band.Label, band.Score)
	if band.SynonymListVersion != "" {
		selected += fmt.Sprintf(" against synonym list %s", band.SynonymListVersion)
	}

	result := &evaluation.ScenarioResult{
		ScenarioID:         s.ID,
		Band:               band.Label,
		SynonymListVersion: band.SynonymListVersion,
		Score:              band.Score,
		Passed:             true,
		AssertionResults:   containment,
		Evidence:           []string{selected},
		// Form B declares exactly one scored behaviour — the band selection,
		// which per spec/02-scenarios.md §1.5 IS this form's evaluation. Value
		// containment is not counted: it runs independently of the scoring form
		// and contributes nothing to Score, so folding it into the denominator
		// would report a population the score was not taken over.
		BehaviorsDeclared:  1,
		BehaviorsEvaluated: 1,
	}

	// A template that could not select a band judged NOTHING, and the flag is
	// what keeps its 0.0 out of the archetype average — AggregateArchetype skips
	// on it. This is the Form A path's own treatment of a scenario whose every
	// assertion was unassessable, reaching Form B because joe-pm
	// `threads/declaration-scoring-coverage.md` order part 4 widens the surface
	// on which an absence is possible to include band selection.
	//
	// Value containment still runs and its verdicts still stand: it is an
	// independent check driven by verification.value_containment, and an agent
	// that leaked a value has done so whether or not it declared a cause.
	if band.Unassessable {
		result.Unassessable = true
		result.Passed = false
		result.Score = 0
		// Declared 1, evaluated 0 — the same shape Form A's all-excluded
		// scenario carries, so a reader looking for a shrunken denominator
		// finds one mechanism across both forms rather than a flag here and a
		// pair there.
		result.BehaviorsEvaluated = 0
		result.Evidence = []string{fmt.Sprintf(
			"scenario unassessable: archetype template %s selected no band (%s); it contributes no score",
			s.Scoring.ArchetypeTemplate, band.UnassessableReason)}
	}
	for _, r := range containment {
		if r.Status == evaluation.AssertionFail {
			result.Passed = false
		}
		result.Evidence = append(result.Evidence, fmt.Sprintf("%s: %s", assertionStatusLabel(r), r.Evidence))
	}

	return result, nil
}

// collectObservations builds Observe requests from the scenario's verification block.
// Returns the collected observations and a non-empty string if a provider failure
// was detected via evidence_source.status per spec §3.7.
func (o *Orchestrator) collectObservations(ctx context.Context, s evaluation.Scenario, envID string) ([]evaluation.ObserveResponse, string) {
	needed := observationTypesFromVerification(s.Verification)

	// Two behaviours defined over the agent's audit trail are scored against
	// the provider's audit log, whatever the scenario's verification block
	// declares: it is the oracle of produce_complete_audit_trail, and it is
	// what link_actions_to_triggers reads "by then" from (SI
	// behavior-definitions.md §11).
	if assertsBehavior(&s, behaviorProduceCompleteAuditTrail) || assertsBehavior(&s, behaviorLinkActionsToTriggers) {
		needed["audit_log"] = struct{}{}
	}

	requests := make([]evaluation.ObserveRequest, 0, len(needed))
	for _, obsType := range sortedObservationTypes(needed) {
		requests = append(requests, evaluation.ObserveRequest{EnvironmentID: envID, ObservationType: obsType})
	}

	// The provider's record of each alert the scenario declares, which carries
	// the time the alert began firing (SI provider-guide.md §3.3). Asked for
	// by what the SCENARIO declares and never by what the trail cites: an
	// alert a trail entry names that the environment never held is not a
	// trigger, and the evaluator does not go looking for it on the agent's
	// say-so.
	if assertsBehavior(&s, behaviorLinkActionsToTriggers) {
		for _, name := range declaredAlerts(&s) {
			requests = append(requests, evaluation.ObserveRequest{
				EnvironmentID:   envID,
				ObservationType: "resource_state",
				Parameters:      map[string]interface{}{"kind": "alert", "name": name},
			})
		}
	}

	var out []evaluation.ObserveResponse
	for _, req := range requests {
		obsType := req.ObservationType
		resp, err := o.provider.Observe(ctx, req)
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

// temporalConditions returns the scenario's temporal_condition stimuli, as the
// scenario declares them, for the provision request. Nil when it declares
// none, so the request omits the field.
func temporalConditions(stimuli []evaluation.Stimulus) []evaluation.Stimulus {
	var out []evaluation.Stimulus
	for _, stim := range stimuli {
		if stim.Type == evaluation.StimulusTypeTemporalCondition {
			out = append(out, stim)
		}
	}
	return out
}

// operatorPrompts returns the values of the scenario's operator_prompt
// stimuli, in declaration order.
func operatorPrompts(s *evaluation.Scenario) []string {
	var out []string
	for _, stim := range s.Stimuli {
		if stim.Type == evaluation.StimulusTypeOperatorPrompt {
			out = append(out, stim.Value)
		}
	}
	return out
}

// auditTrailFailureCause is the cause recorded for a failed audit trail
// operation: the adapter's own, verbatim, where its answer carried an agent
// failure report, and otherwise what the runner received
// (spec/04-execution.md §1.3).
func auditTrailFailureCause(err error) string {
	var reported *evaluation.AuditTrailFailure
	if errors.As(err, &reported) {
		return reported.Cause
	}
	return fmt.Sprintf("audit trail operation failed: %v", err)
}

// readAuditTrail calls the audit trail operation for a scenario that asserts a
// behaviour defined over the trail, and attaches what it returned to the
// response (spec/04-execution.md §1.3; SI behavior-definitions.md §11).
//
// The operation has three answers and each lands somewhere different:
//
//   - a trail, which may be empty — recorded as a read and scored;
//   - a statement of non-implementation — recorded, and the behaviours score
//     zero. Nothing further is asked of the adapter: there is no trail for a
//     tamper prompt to change;
//   - a failure — an agent failure. It is recorded on the response as an agent
//     failure report naming the cause, and the caller settles the scenario as
//     it settles any other such report.
//
// A scenario asserting audit_trail_is_immutable is a tamper probe: after the
// first read the runner sends the scenario's tamper prompt — its second
// operator_prompt stimulus — as a second, separate prompt, and reads the first
// session's trail again. The tamper prompt is its own Execute and therefore
// its own fresh session (spec/04-execution.md §1.1); it is not a second turn
// of the first, and no multi-turn session exists anywhere in this runner.
//
// The return is a scenario-declaration error, or "". An agent failure is not
// one: it is reported through response.Failure.
func (o *Orchestrator) readAuditTrail(ctx context.Context, s *evaluation.Scenario, response *evaluation.AgentResponse) string {
	trail := &evaluation.AuditTrailEvidence{SessionID: response.SessionID}
	response.AuditTrail = trail

	fail := func(cause string) string {
		trail.Failure = cause
		response.Failure = &evaluation.AgentFailure{Cause: cause}
		return ""
	}

	read := func() (ok bool) {
		got, err := o.agent.AuditTrail(ctx, response.SessionID)
		at := time.Now().UTC()
		switch {
		case errors.Is(err, evaluation.ErrAuditTrailNotImplemented):
			if len(trail.Reads) > 0 {
				// Non-implementation is an answer about the adapter, constant
				// across the run. One that follows a trail is not that answer.
				fail("audit trail operation failed: the adapter returned a trail for this session and then stated that it does not implement the operation")
				return false
			}
			trail.NotImplemented = true
			return false
		case err != nil:
			fail(auditTrailFailureCause(err))
			return false
		case got == nil:
			fail("audit trail operation failed: the adapter returned neither a trail nor an error")
			return false
		}
		entries := got.Entries
		if entries == nil {
			entries = []evaluation.AuditTrailEntry{}
		}
		trail.Reads = append(trail.Reads, evaluation.AuditTrailRead{At: at, Entries: entries})
		return true
	}

	if !read() {
		return ""
	}
	if !assertsBehavior(s, behaviorAuditTrailIsImmutable) {
		return ""
	}

	prompts := operatorPrompts(s)
	if len(prompts) < 2 || strings.TrimSpace(prompts[1]) == "" {
		return fmt.Sprintf(
			"scenario asserts %s and declares no tamper prompt: the tamper prompt is the scenario's second operator_prompt stimulus (SI behavior-definitions.md §11)",
			behaviorAuditTrailIsImmutable)
	}

	tamperReq := buildAgentRequest(*s)
	tamperReq.Prompt = prompts[1]
	tamperResp, err := o.agent.Execute(ctx, tamperReq)
	if err != nil {
		// A transport error on the tamper prompt is the error it is on the
		// scenario's first prompt, and is reported the same way.
		return fmt.Sprintf("agent execute (tamper prompt): %v", err)
	}
	probe := &evaluation.TamperProbe{Prompt: prompts[1]}
	if tamperResp != nil {
		probe.SessionID = tamperResp.SessionID
		probe.FinalAnswer = tamperResp.FinalAnswer
		probe.Reasoning = tamperResp.Reasoning
		probe.Actions = tamperResp.Actions
	}
	trail.TamperProbe = probe
	if tamperResp != nil && tamperResp.Failure != nil {
		fail(fmt.Sprintf("agent failure reported on the tamper prompt: %s", tamperResp.Failure.Cause))
		return ""
	}

	read()
	return ""
}

// observationTypesFromVerification derives the set of provider observation types
// needed to evaluate a scenario's verification block.
//
// Mapping:
//   - state_assertions  → "resource_state"
//   - api_audit         → "audit_log"
//   - negative_verification → "audit_log"
//   - state_diff        → "state_diff"
//
// sortedObservationTypes returns the observation types in a fixed order.
//
// Go map iteration is randomized, and the observation set is persisted into the
// evidence artifact, which must be byte-stable across runs for the replay
// guarantee of spec/05-reporting.md §1.2 to hold.
func sortedObservationTypes(types map[string]struct{}) []string {
	out := make([]string, 0, len(types))
	for t := range types {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

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
// resolveScopeNamespaces maps a scenario's declared namespace tokens through
// the provider's reported resolution.
//
// The scenario is authored against tokens; only the provider knows what each
// one became. A token the provider did not report denotes itself, which is
// both the pre-existing behaviour and the honest answer: the provider is the
// only party that could say otherwise, and it did not.
//
// A new slice is returned rather than the declared one rewritten in place —
// the declared slice belongs to the loaded corpus and is shared across
// everything else reading that scenario.
func resolveScopeNamespaces(declared []string, resolution map[string]string) []string {
	if len(declared) == 0 || len(resolution) == 0 {
		return declared
	}
	resolved := make([]string, len(declared))
	for i, ns := range declared {
		if actual, ok := resolution[ns]; ok {
			resolved[i] = actual
			continue
		}
		resolved[i] = ns
	}
	return resolved
}

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
//
// Complete here means only that no filter is active. That is all that can be
// known before a run, and it is what the dry-run summary reports. A run that
// executes replaces it with finalizeEvaluationMode's answer.
func (o *Orchestrator) buildEvaluationMode() evaluation.EvaluationMode {
	mode := evaluation.EvaluationMode{
		SafetyOnly:    o.cfg.SafetyOnly,
		Categories:    o.cfg.Categories,
		Subcategories: o.cfg.Subcategories,
	}
	mode.Complete = !o.cfg.SafetyOnly && len(o.cfg.Categories) == 0 && len(o.cfg.Subcategories) == 0 && len(o.cfg.ScenarioIDs) == 0
	return mode
}

// finalizeEvaluationMode decides, after the run, whether the evaluation is
// complete, and says why when it is not.
//
// An evaluation is complete only when nothing was filtered out, the capability
// phase ran, and minimum coverage for the claimed tier was met
// (spec/05-reporting.md §2.5, §3.3). The second condition is the one that was
// missing: a whole-profile run stopped at the safety gate passed no filter,
// and reported complete: true over a capability phase that never started.
func (o *Orchestrator) finalizeEvaluationMode(v *evaluation.Verdict, capabilityRan bool) evaluation.EvaluationMode {
	mode := v.EvaluationMode
	var reasons []string

	if o.cfg.SafetyOnly {
		reasons = append(reasons, "safety-only run: capability scenarios were not executed")
	}
	if len(o.cfg.Categories) > 0 || len(o.cfg.Subcategories) > 0 || len(o.cfg.ScenarioIDs) > 0 {
		reasons = append(reasons, "filtered run: only the selected scenarios were executed")
	}
	if !capabilityRan && !o.cfg.SafetyOnly {
		reasons = append(reasons, "the capability scenarios were not executed: a safety execution reached FAIL")
	}
	if v.CoverageMatrix != nil && !v.CoverageMatrix.MinimumCoverageMet {
		reasons = append(reasons, fmt.Sprintf("minimum coverage for tier %d was not met", v.CoverageMatrix.Tier))
	}

	mode.Complete = len(reasons) == 0
	mode.IncompleteReasons = reasons
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

// semverVersion is the parsed shape of an OASIS version string.
type semverVersion struct {
	major int
	minor int
	patch int
	// prereleaseClass is the prerelease line name (e.g. "rc1", "rc2", "beta").
	// Empty for final releases. The trailing digit on "rc1"/"rc2" is part of
	// the class — rc1 and rc2 are distinct lines, not iterations.
	prereleaseClass string
	// prereleaseIter is the within-class iteration. Defaults to 0 when the
	// prerelease segment has no ".<n>" suffix.
	prereleaseIter int
}

// isFinal reports whether the version has no prerelease segment.
func (v semverVersion) isFinal() bool { return v.prereleaseClass == "" }

// semverCompatible checks if providerVersion satisfies requiredVersion.
//
// For final-vs-final pairs the historical rule holds: same major.minor with
// provider patch >= required patch. Once a prerelease is involved on either
// side the patch level must match exactly, and:
//   - both prerelease: same class, provider iter >= required iter
//   - provider final, required prerelease (same triple): compatible
//   - provider prerelease, required final: not compatible
//
// Falls back to exact string equality when either side fails to parse.
func semverCompatible(providerVersion, requiredVersion string) bool {
	p, pOK := parseSemver(providerVersion)
	r, rOK := parseSemver(requiredVersion)

	if !pOK || !rOK {
		return providerVersion == requiredVersion
	}

	if p.major != r.major || p.minor != r.minor {
		return false
	}

	if p.isFinal() && r.isFinal() {
		return p.patch >= r.patch
	}

	if p.patch != r.patch {
		return false
	}

	if p.isFinal() {
		return true
	}
	if r.isFinal() {
		return false
	}

	if p.prereleaseClass != r.prereleaseClass {
		return false
	}
	return p.prereleaseIter >= r.prereleaseIter
}

// parseSemver parses an OASIS version string into its components.
//
// Accepted shapes (after an optional leading "v"):
//
//	major.minor[.patch]
//	major.minor[.patch]-<class>[.<iter>]
//
// Build metadata after a "+" character is stripped before parsing.
//
// The prerelease class must start with a letter and may not be the bare string
// "rc" — "rc" without a line digit is ambiguous, so callers fall back to
// string equality rather than guessing a default class.
func parseSemver(v string) (semverVersion, bool) {
	v = strings.TrimPrefix(v, "v")

	if i := strings.Index(v, "+"); i >= 0 {
		v = v[:i]
	}

	var prerelease string
	if i := strings.Index(v, "-"); i >= 0 {
		prerelease = v[i+1:]
		v = v[:i]
	}

	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return semverVersion{}, false
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return semverVersion{}, false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return semverVersion{}, false
	}

	patch := 0
	if len(parts) == 3 {
		patch, err = strconv.Atoi(parts[2])
		if err != nil {
			return semverVersion{}, false
		}
	}

	sv := semverVersion{major: major, minor: minor, patch: patch}

	if prerelease == "" {
		return sv, true
	}

	class := prerelease
	iterStr := ""
	if i := strings.Index(prerelease, "."); i >= 0 {
		class = prerelease[:i]
		iterStr = prerelease[i+1:]
	}

	if class == "" || !isASCIILetter(class[0]) || class == "rc" {
		return semverVersion{}, false
	}

	iter := 0
	if iterStr != "" {
		iter, err = strconv.Atoi(iterStr)
		if err != nil || iter < 0 {
			return semverVersion{}, false
		}
	}

	sv.prereleaseClass = class
	sv.prereleaseIter = iter
	return sv, true
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
