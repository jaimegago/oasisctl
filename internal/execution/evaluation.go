package execution

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/record"
	"github.com/jaimegago/oasisctl/internal/scoring"
)

// This file is how an evaluation is executed from core 1.0.0-rc1.14: it is
// opened, and then executed one scenario at a time, by this process or a
// later one (spec/04-execution.md §3). What is normative is what happens at
// five points, and each has one function here:
//
//	§3.1 opening an evaluation      Orchestrator.OpenEvaluation
//	§3.2 creating a lab             Session.attachLab
//	§3.3 before every execution     Session.Execute, up to the opened record
//	§3.4 during and after           Session.Execute, from the opened record
//	§3.5 assembling                 Session.Assemble (assemble.go)
//
// Session.Drive is the reference flow of §3.6 over those: every execution
// owed, safety first.

// The refusals of spec/04-execution.md §3.3. In each case the execution is
// not made and nothing is added to the evaluation.
var (
	// ErrUnknownScenario: the scenario is not in the suite the evaluation was
	// opened over.
	ErrUnknownScenario = errors.New("the scenario is not in the suite the evaluation was opened over")
	// ErrNotApplicable: the agent's configuration excludes the scenario.
	ErrNotApplicable = errors.New("the scenario is NOT_APPLICABLE to the agent's configuration and is not executed")
	// ErrCountReached: the scenario already holds its declared count of
	// counted executions (spec/01-core.md §2.4).
	ErrCountReached = errors.New("the scenario already holds its declared count of counted executions, and no further execution of it is made in this evaluation")
	// ErrSafetyNotPassed: a capability execution was asked for and the
	// evaluation's safety verdict is not PASS (spec/01-core.md §2.6).
	ErrSafetyNotPassed = errors.New("no capability execution is made before the evaluation's safety verdict is PASS")
)

// Session is one process's hold on one evaluation's record.
type Session struct {
	o       *Orchestrator
	store   *record.Store
	opening evaluation.EvaluationOpening
	profile *evaluation.Profile
	// handed is the scenarios the caller loaded, whatever their tier, and
	// byID those at or below the evaluation's tier.
	handed []evaluation.Scenario
	byID   map[string]evaluation.Scenario
	// lab is the lab this process executes on. Empty on a session loaded for
	// reading, which makes no execution.
	lab string

	locks    sync.Map
	recordMu sync.Mutex
}

// Close releases the evaluation. Its record stays where it is.
func (s *Session) Close() {
	if s != nil && s.store != nil {
		s.store.Close()
	}
}

// Opening is what opening the evaluation recorded.
func (s *Session) Opening() evaluation.EvaluationOpening { return s.opening }

// Dir is the evaluation directory.
func (s *Session) Dir() string { return s.store.Dir() }

// Lab is the identifier of the lab this session executes on.
func (s *Session) Lab() string { return s.lab }

func (o *Orchestrator) implementation() evaluation.EvaluationImplementation {
	impl := o.cfg.Implementation
	if impl.Name == "" {
		impl.Name = implementationName
	}
	if impl.Version == "" {
		impl.Version = unversionedBuild
	}
	return impl
}

// readAgent asks the adapter for the agent's identity and configuration and
// applies the profile schema's defaults. It is asked when an evaluation is
// opened and again before every execution; the report is not made once and
// remembered (spec/04-execution.md §1).
func (o *Orchestrator) readAgent(ctx context.Context, profile *evaluation.Profile) (evaluation.AgentReading, error) {
	identity, raw, err := o.agent.ReportIdentityAndConfiguration(ctx)
	if err != nil {
		return evaluation.AgentReading{}, fmt.Errorf("agent identity and configuration: %w", err)
	}
	effective := raw
	if profile.AgentConfigurationSchema != nil {
		effective, err = ResolveConfiguration(raw, profile.AgentConfigurationSchema)
		if err != nil {
			return evaluation.AgentReading{}, fmt.Errorf("resolve agent configuration: %w", err)
		}
	}
	return evaluation.AgentReading{Identity: identity, Configuration: effective}, nil
}

// suiteAtTier returns the scenarios at or below the tier, by id, and the
// suite's entries for an opening.
func suiteAtTier(scenarios []evaluation.Scenario, tier int, config evaluation.AgentConfiguration) (map[string]evaluation.Scenario, []evaluation.OpeningScenario, []evaluation.TierExcludedScenario) {
	byID := map[string]evaluation.Scenario{}
	var entries []evaluation.OpeningScenario
	var above []evaluation.TierExcludedScenario
	for i := range scenarios {
		sc := scenarios[i]
		if _, dup := byID[sc.ID]; dup {
			continue
		}
		if sc.Tier > tier {
			above = append(above, evaluation.TierExcludedScenario{ScenarioID: sc.ID, Classification: sc.Classification, Category: sc.Category, Tier: sc.Tier})
			continue
		}
		byID[sc.ID] = sc
		entry := evaluation.OpeningScenario{ScenarioID: sc.ID, Classification: sc.Classification, Category: sc.Category, Applicable: IsApplicable(&sc, config)}
		if !entry.Applicable {
			entry.ExclusionConditions = applicabilityConditions([]evaluation.Scenario{sc})
		}
		entries = append(entries, entry)
	}
	return byID, entries, above
}

// evaluationName returns the name to open an evaluation under: the one the
// caller gave, or one made of the agent's name, the time and a random
// suffix, which distinguishes it from every other this evaluator has opened.
func (o *Orchestrator) evaluationName(agent string) string {
	if name := strings.TrimSpace(o.cfg.EvaluationName); name != "" {
		return name
	}
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		}
		return '-'
	}, agent)
	if slug == "" {
		slug = "evaluation"
	}
	return fmt.Sprintf("%s-%s-%s", slug, o.now().UTC().Format("20060102T150405Z"), hex.EncodeToString(suffix))
}

// checkLab performs what spec/04-execution.md §3.2 asks when a lab is
// created: the provider conformance check, and the evidence that the lab
// meets the evaluation's tier. It returns the record of a lab that passed
// both; a lab that fails either is not used and adds nothing to the
// evaluation.
func (o *Orchestrator) checkLab(ctx context.Context, profile *evaluation.Profile, tier int, id, endpoint string) (evaluation.LabRecord, error) {
	conformance, err := o.preflightConformanceCheck(ctx, profile, tier)
	if err != nil {
		return evaluation.LabRecord{}, err
	}
	tierEvidence, err := VerifyTierEvidence(conformance, tier)
	if err != nil {
		return evaluation.LabRecord{}, err
	}
	return evaluation.LabRecord{
		Lab:                 id,
		CreatedAt:           o.now().UTC(),
		Endpoint:            endpoint,
		Provider:            conformance.Provider,
		ProviderVersion:     conformance.ProviderVersion,
		ConformanceCheck:    buildConformanceCheck(conformance, profile.ProviderConformanceRequirements, tier),
		TierEvidence:        tierEvidence,
		TierEvidenceSummary: evaluation.TierEvidenceSummary(conformance.Provider, conformance.ProviderVersion, tierEvidence),
		Characteristics:     environmentCharacteristics(conformance),
	}, nil
}

// OpenEvaluation opens a new evaluation in Config.EvaluationDir
// (spec/04-execution.md §3.1): it loads the profile, reads the agent's
// identity and configuration, and records the evaluation's name, its identity
// and its two declared execution counts. It refuses a safety count below 3 or
// below the profile's safety minimum, and a capability count below the
// profile's capability minimum.
//
// The environment provider's build is an element of the identity, and the
// only place it is declared is a provider's conformance response. So opening
// checks the provider it was pointed at as a lab (§3.2), takes the build
// from it, and records it as the evaluation's first lab.
func (o *Orchestrator) OpenEvaluation(ctx context.Context, profilePath string, scenarios []evaluation.Scenario, providerEndpoint string) (*Session, error) {
	if o.cfg.EvaluationDir == "" {
		return nil, fmt.Errorf("an evaluation's record is kept where the caller says, and no evaluation directory was given")
	}
	if record.Exists(o.cfg.EvaluationDir) {
		return nil, fmt.Errorf("%w: %s", record.ErrAlreadyOpened, o.cfg.EvaluationDir)
	}
	profile, err := o.loader.Load(ctx, profilePath)
	if err != nil {
		return nil, fmt.Errorf("load profile: %w", err)
	}
	if err := evaluation.ValidateExecutionCounts(o.cfg.Counts, profile.ExecutionMinimums); err != nil {
		return nil, fmt.Errorf("the evaluation is not opened: %w", err)
	}

	reading, err := o.readAgent(ctx, profile)
	if err != nil {
		return nil, err
	}
	byID, entries, above := suiteAtTier(scenarios, o.cfg.Tier, reading.Configuration)
	if len(entries) == 0 {
		return nil, fmt.Errorf("the evaluation is not opened: the profile has no scenario at or below tier %d", o.cfg.Tier)
	}
	if err := bindFormB(byID); err != nil {
		return nil, err
	}

	labID := o.cfg.Lab
	if labID == "" {
		labID = "lab-1"
	}
	lab, err := o.checkLab(ctx, profile, o.cfg.Tier, labID, providerEndpoint)
	if err != nil {
		return nil, err
	}

	var evaluator *string
	if e := strings.TrimSpace(o.cfg.Evaluator); e != "" {
		evaluator = &e
	}
	opening := evaluation.EvaluationOpening{
		Evaluation: o.evaluationName(reading.Identity.Name),
		OpenedAt:   o.now().UTC(),
		Identity: evaluation.EvaluationIdentity{
			Agent:                    reading.Identity.Name,
			AgentVersion:             reading.Identity.Version,
			AgentIdentity:            evaluation.BuildVerdictAgentIdentity(reading.Identity.Declaration),
			AgentConfiguration:       reading.Configuration,
			DomainProfile:            profile.Metadata.Name,
			DomainProfileVersion:     profile.Metadata.Version,
			OASISCoreVersion:         coreVersion(profile.Metadata.OASISCore),
			EvaluationImplementation: o.implementation(),
			Environment:              evaluation.IdentityEnvironment{Provider: lab.Provider, ProviderVersion: lab.ProviderVersion, Tier: o.cfg.Tier},
			Evaluator:                evaluator,
		},
		ExecutionCounts: o.cfg.Counts,
		Minimums:        profile.ExecutionMinimums,
		Scenarios:       entries,
		ExcludedByTier:  above,
	}
	if opening.Identity.AgentConfiguration == nil {
		opening.Identity.AgentConfiguration = evaluation.AgentConfiguration{}
	}
	// An agent configuration to which no safety scenario applies cannot be
	// evaluated under the profile (spec/01-core.md §3.6.5): there would never
	// be a safety verdict, so no capability execution could ever be made.
	if len(opening.Applicable(evaluation.ClassificationSafety)) == 0 {
		return nil, fmt.Errorf("the evaluation is not opened: no safety scenario of the profile applies to the agent's configuration at tier %d, so it cannot be evaluated under this profile (spec/01-core.md §3.6.5)", o.cfg.Tier)
	}

	store, err := record.Create(o.cfg.EvaluationDir, opening)
	if err != nil {
		return nil, err
	}
	if err := store.AddLab(lab); err != nil {
		store.Close()
		return nil, err
	}
	o.logger.Info("evaluation opened", "evaluation", opening.Evaluation, "dir", o.cfg.EvaluationDir,
		"safety_count", opening.ExecutionCounts.Safety, "capability_count", opening.ExecutionCounts.Capability, "lab", lab.Lab)
	return &Session{o: o, store: store, opening: opening, profile: profile, handed: scenarios, byID: byID, lab: lab.Lab}, nil
}

// bindFormB binds every Form B scoring block up front. A binding that
// references an unregistered archetype template, or omits a role the
// template declares, fails here rather than as an attempt after an agent has
// been driven through the scenario.
func bindFormB(byID map[string]evaluation.Scenario) error {
	for id := range byID {
		sc := byID[id]
		if sc.Classification != evaluation.ClassificationCapability || !sc.Scoring.IsFormB() {
			continue
		}
		if _, err := scoring.Bind(&sc); err != nil {
			return fmt.Errorf("scoring binding: %w", err)
		}
	}
	return nil
}

// load opens the evaluation's record and checks the elements of its identity
// this process can read without the agent or a provider: the spec, and the
// evaluation implementation's build. A process that is another build, or
// holds another version of the profile, produces results that belong to
// another evaluation, so it does not continue this one.
func (o *Orchestrator) load(ctx context.Context, profilePath string, scenarios []evaluation.Scenario) (*Session, error) {
	if o.cfg.EvaluationDir == "" {
		return nil, fmt.Errorf("no evaluation directory was given")
	}
	profile, err := o.loader.Load(ctx, profilePath)
	if err != nil {
		return nil, fmt.Errorf("load profile: %w", err)
	}
	store, err := record.Open(o.cfg.EvaluationDir)
	if err != nil {
		return nil, err
	}
	opening := store.Opening()

	var diffs []evaluation.IdentityDifference
	diff := func(element, was, read string) {
		if was != read {
			diffs = append(diffs, evaluation.IdentityDifference{Element: element, Evaluation: was, Read: read})
		}
	}
	id := opening.Identity
	impl := o.implementation()
	diff("domain_profile", id.DomainProfile, profile.Metadata.Name)
	diff("domain_profile_version", id.DomainProfileVersion, profile.Metadata.Version)
	diff("oasis_core_version", id.OASISCoreVersion, coreVersion(profile.Metadata.OASISCore))
	diff("evaluation_implementation", fmt.Sprintf("%s %s (%s)", id.EvaluationImplementation.Name, id.EvaluationImplementation.Version, id.EvaluationImplementation.Build),
		fmt.Sprintf("%s %s (%s)", impl.Name, impl.Version, impl.Build))
	if o.cfg.Tier != 0 {
		diff("environment.tier", fmt.Sprint(id.Environment.Tier), fmt.Sprint(o.cfg.Tier))
	}
	if e := strings.TrimSpace(o.cfg.Evaluator); e != "" {
		was := "null"
		if id.Evaluator != nil {
			was = *id.Evaluator
		}
		diff("evaluator", was, e)
	}
	if len(diffs) > 0 {
		store.Close()
		return nil, &evaluation.IdentityMismatchError{Differences: diffs}
	}
	if c := o.cfg.Counts; (c.Safety != 0 || c.Capability != 0) && c != opening.ExecutionCounts {
		store.Close()
		return nil, fmt.Errorf("the evaluation was opened with a safety count of %d and a capability count of %d, and the counts do not change afterwards (spec/01-core.md §2.4); %d and %d were given",
			opening.ExecutionCounts.Safety, opening.ExecutionCounts.Capability, c.Safety, c.Capability)
	}

	// The tier is the evaluation's from here on, whether or not this process
	// was told it: it is what every provision request names.
	o.cfg.Tier = id.Environment.Tier

	byID, entries, _ := suiteAtTier(scenarios, id.Environment.Tier, id.AgentConfiguration)
	if why := suiteDiffers(opening.Scenarios, entries); why != "" {
		store.Close()
		return nil, fmt.Errorf("the scenario suite is not the one the evaluation was opened over: %s", why)
	}
	return &Session{o: o, store: store, opening: opening, profile: profile, handed: scenarios, byID: byID}, nil
}

// suiteDiffers compares the suite an evaluation was opened over with the one
// loaded now, by scenario id, classification and applicability.
func suiteDiffers(opened, now []evaluation.OpeningScenario) string {
	was := map[string]evaluation.OpeningScenario{}
	for _, s := range opened {
		was[s.ScenarioID] = s
	}
	for _, s := range now {
		o, ok := was[s.ScenarioID]
		switch {
		case !ok:
			return fmt.Sprintf("scenario %s is not in it", s.ScenarioID)
		case o.Classification != s.Classification:
			return fmt.Sprintf("scenario %s was %s and is now %s", s.ScenarioID, o.Classification, s.Classification)
		case o.Applicable != s.Applicable:
			return fmt.Sprintf("the applicability of scenario %s changed", s.ScenarioID)
		}
		delete(was, s.ScenarioID)
	}
	for id := range was {
		return fmt.Sprintf("scenario %s is in it and is no longer in the profile", id)
	}
	return ""
}

// LoadEvaluation loads the evaluation in Config.EvaluationDir for reading:
// its status, or its verdict and report. It reaches no agent and no provider,
// makes no execution, and leaves the record as it found it.
func (o *Orchestrator) LoadEvaluation(ctx context.Context, profilePath string, scenarios []evaluation.Scenario) (*Session, error) {
	return o.load(ctx, profilePath, scenarios)
}

// ResumeEvaluation loads the evaluation in Config.EvaluationDir to continue
// it (spec/04-execution.md §3.6 steps 1 to 3): it loads the record, attaches
// to a lab already recorded or checks and records a new one, and closes as
// INTERRUPTED any execution record a dead process left open.
func (o *Orchestrator) ResumeEvaluation(ctx context.Context, profilePath string, scenarios []evaluation.Scenario, providerEndpoint string) (*Session, error) {
	s, err := o.load(ctx, profilePath, scenarios)
	if err != nil {
		return nil, err
	}
	if err := bindFormB(s.byID); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.attachLab(ctx, providerEndpoint); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.closeInterrupted(); err != nil {
		s.Close()
		return nil, err
	}
	if _, err := s.ensureSafetyVerdictRecord(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// attachLab settles which lab this process executes on. A lab the caller
// names that the evaluation has already recorded is attached to and is not
// checked again: a lab is checked once, when it is created
// (spec/04-execution.md §2.5). Anything else is a new lab: it is checked, the
// build its provider declares is compared with the evaluation's, and it is
// recorded (§3.2).
func (s *Session) attachLab(ctx context.Context, endpoint string) error {
	labs, err := s.store.Labs()
	if err != nil {
		return err
	}
	if want := s.o.cfg.Lab; want != "" {
		for _, lab := range labs {
			if lab.Lab != want {
				continue
			}
			if lab.Endpoint != "" && endpoint != "" && lab.Endpoint != endpoint {
				return fmt.Errorf("lab %q was recorded for this evaluation at %s, and the provider given is at %s: a provider at another address is another lab and is given another name", want, lab.Endpoint, endpoint)
			}
			s.lab = lab.Lab
			return nil
		}
	}
	id := s.o.cfg.Lab
	if id == "" {
		id = fmt.Sprintf("lab-%d", len(labs)+1)
	}
	lab, err := s.o.checkLab(ctx, s.profile, s.opening.Identity.Environment.Tier, id, endpoint)
	if err != nil {
		return err
	}
	env := s.opening.Identity.Environment
	var diffs []evaluation.IdentityDifference
	if lab.Provider != env.Provider {
		diffs = append(diffs, evaluation.IdentityDifference{Element: "environment.provider", Evaluation: env.Provider, Read: lab.Provider})
	}
	if lab.ProviderVersion != env.ProviderVersion {
		diffs = append(diffs, evaluation.IdentityDifference{Element: "environment.provider_version", Evaluation: env.ProviderVersion, Read: lab.ProviderVersion})
	}
	if len(diffs) > 0 {
		// A lab whose provider is another build is not used for this
		// evaluation, and adds nothing to it.
		return &evaluation.IdentityMismatchError{Differences: diffs}
	}
	if err := s.store.AddLab(lab); err != nil {
		return err
	}
	s.lab = lab.Lab
	s.o.logger.Info("lab recorded", "evaluation", s.opening.Evaluation, "lab", lab.Lab)
	return nil
}

// closeInterrupted closes, as an INTERRUPTED attempt, every execution record
// that was opened and never closed (spec/04-execution.md §3.4). This process
// holds the evaluation's lock, so no other process is making those
// executions: the one that opened each is gone.
func (s *Session) closeInterrupted() error {
	all, err := s.store.Executions()
	if err != nil {
		return err
	}
	for _, e := range all {
		if e.State == evaluation.ExecutionClosed {
			continue
		}
		e.Record = e.AsRead()
		// The artifact of an execution a dead process left open holds what
		// that process persisted, which is nothing: it wrote none.
		interrupted := evaluation.ScenarioResult{ScenarioID: e.ScenarioID}
		s.writeEmptyArtifact(e.ScenarioID, e.Record.Execution, &interrupted)
		if interrupted.EvidencePath != "" {
			path := interrupted.EvidencePath
			e.Record.EvidenceArtifact = &path
		}
		if err := s.store.CloseExecution(e); err != nil {
			return err
		}
		s.o.logger.Warn("execution record found open and closed as INTERRUPTED", "scenario", e.ScenarioID, "execution", e.Record.Execution)
	}
	return nil
}

func (s *Session) scenarioLock(id string) *sync.Mutex {
	m, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// counted returns how many counted executions a scenario holds.
func counted(executions []evaluation.StoredExecution) int {
	n := 0
	for _, e := range executions {
		if e.State == evaluation.ExecutionClosed && e.Record.Counted {
			n++
		}
	}
	return n
}

// safetyGate reads the evaluation's safety verdict record and returns its
// hash where its verdict is PASS (spec/04-execution.md §3.3 step 4). The
// hash is taken from the record's bytes as written, before the execution is
// made, never computed afterwards from whatever record then exists.
func (s *Session) safetyGate() (string, error) {
	data, err := s.store.SafetyVerdictRecord()
	if err != nil {
		return "", err
	}
	if data == nil {
		return "", fmt.Errorf("%w: the evaluation has no safety verdict record yet", ErrSafetyNotPassed)
	}
	rec, err := record.ParseSafetyVerdictRecord(data)
	if err != nil {
		return "", err
	}
	if rec.Safety != evaluation.SafetyVerdictPass {
		return "", fmt.Errorf("%w: the safety verdict record's verdict is %s", ErrSafetyNotPassed, rec.Safety)
	}
	return evaluation.HashSafetyVerdictRecord(data), nil
}

// ExecutionOutcome is what one call to Session.Execute did.
type ExecutionOutcome struct {
	ScenarioID string
	// Record is the execution record as it was closed.
	Record evaluation.ExecutionRecord
	// SafetyVerdict is set when this execution was the last the safety
	// scenarios were owed and the safety verdict record was written.
	SafetyVerdict *evaluation.SafetyVerdict
}

// Execute makes one execution of one scenario for the evaluation, in the
// order spec/04-execution.md §3.3 gives, and closes its record as §3.4
// says. An error means the execution was not made and nothing was added to
// the evaluation; an execution that was made and reached no verdict is not an
// error, it is an attempt, and is returned as one.
func (s *Session) Execute(ctx context.Context, scenarioID string) (*ExecutionOutcome, error) {
	if s.lab == "" {
		return nil, fmt.Errorf("the evaluation was loaded for reading; resume it to make an execution")
	}
	entry, ok := s.opening.Scenario(scenarioID)
	scenario, known := s.byID[scenarioID]
	if !ok || !known {
		return nil, fmt.Errorf("%w: %s", ErrUnknownScenario, scenarioID)
	}

	// One execution of a scenario at a time: the count is checked and the
	// record opened as one step.
	lock := s.scenarioLock(scenarioID)
	lock.Lock()
	defer lock.Unlock()

	// 1. Read the agent's identity and configuration, and compare.
	reading, err := s.o.readAgent(ctx, s.profile)
	if err != nil {
		return nil, err
	}
	if diffs := s.opening.Identity.CompareReading(reading); len(diffs) > 0 {
		return nil, &evaluation.IdentityMismatchError{Differences: diffs}
	}

	// 2. Applicability. The configuration is the evaluation's, so this is
	// what opening recorded.
	if !entry.Applicable {
		return nil, fmt.Errorf("%w: %s (%s)", ErrNotApplicable, scenarioID, entry.ExclusionConditions)
	}

	// 3. The scenario's count.
	existing, err := s.store.ScenarioExecutions(scenarioID)
	if err != nil {
		return nil, err
	}
	declared := s.opening.Count(entry.Classification)
	if counted(existing) >= declared {
		return nil, fmt.Errorf("%w: %s holds %d of %d", ErrCountReached, scenarioID, counted(existing), declared)
	}

	// 4. For a capability scenario, the gate.
	rec := evaluation.ExecutionRecord{
		Evaluation:         s.opening.Evaluation,
		StartedAt:          s.o.now().UTC(),
		Lab:                s.lab,
		AgentIdentity:      reading.ForRecord(),
		AgentConfiguration: reading.Configuration,
	}
	if rec.AgentConfiguration == nil {
		rec.AgentConfiguration = evaluation.AgentConfiguration{}
	}
	if entry.Classification == evaluation.ClassificationCapability {
		hash, err := s.safetyGate()
		if err != nil {
			return nil, err
		}
		rec.SafetyVerdictHash = hash
	}

	// 5. Open the execution's record, before anything is provisioned.
	stored, err := s.store.OpenExecution(scenarioID, entry.Classification, rec)
	if err != nil {
		return nil, err
	}

	// The execution itself. Its evidence artifact is kept with the record.
	target := evidenceTarget{dir: s.store.Dir(), outputPath: filepath.Join(s.store.Dir(), "verdict")}
	result := finishExecution(
		s.o.runScenario(ctx, scenario, reading.Configuration, target, stored.Record.Execution),
		s.opening.Identity.AgentIdentity.DeclaredModel)
	if result.Attempt != nil && ctx.Err() != nil {
		// This process was told to stop while the execution was in flight.
		result.Attempt = &evaluation.Attempt{
			Outcome: evaluation.AttemptInterrupted,
			Cause:   fmt.Sprintf("the evaluation implementation was stopped before the execution ended (%v)", ctx.Err()),
		}
	}

	// Every execution has an evidence artifact, counted or not
	// (spec/05-reporting.md §1.2). One that stopped before it wrote its own —
	// the environment was never provisioned, the adapter never answered — is
	// given one holding as much as it produced, which is nothing.
	if result.EvidencePath == "" {
		s.writeEmptyArtifact(scenarioID, stored.Record.Execution, &result)
	}

	closeExecutionRecord(&stored, &result, s.o.now().UTC())
	if err := s.store.CloseExecution(stored); err != nil {
		return nil, err
	}
	out := &ExecutionOutcome{ScenarioID: scenarioID, Record: stored.Record}
	if stored.Record.Counted {
		s.o.logger.Info("execution counted", "scenario", scenarioID, "execution", stored.Record.Execution, "verdict", stored.Record.Verdict.String())
	} else {
		s.o.logger.Warn("execution reached no verdict", "scenario", scenarioID, "execution", stored.Record.Execution,
			"outcome", string(stored.Record.Outcome), "cause", stored.Record.Cause)
	}

	// When the execution that closes is the last counted execution the
	// safety scenarios were owed, write the safety verdict record.
	if entry.Classification == evaluation.ClassificationSafety && stored.Record.Counted {
		verdict, err := s.ensureSafetyVerdictRecord()
		if err != nil {
			return out, err
		}
		out.SafetyVerdict = verdict
	}
	return out, nil
}

// writeEmptyArtifact persists the evidence artifact of an execution that
// produced no evidence before it stopped.
func (s *Session) writeEmptyArtifact(scenarioID string, ordinal int, result *evaluation.ScenarioResult) {
	artifact := BuildEvidenceArtifact(scenarioID, ordinal, nil, nil)
	path, err := WriteEvidenceArtifact(artifact, s.store.Dir(), filepath.Join(s.store.Dir(), "verdict"))
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("write evidence artifact: %v", err))
		return
	}
	result.EvidencePath = path
}

// closeExecutionRecord fills an opened record from what the execution
// produced: as a counted execution where a verdict was reached, and as an
// attempt where none was.
func closeExecutionRecord(stored *evaluation.StoredExecution, result *evaluation.ScenarioResult, ended time.Time) {
	rec := &stored.Record
	rec.EndedAt = &ended
	if result.Attempt != nil {
		rec.Counted = false
		rec.Outcome = result.Attempt.Outcome
		rec.Cause = result.Attempt.Cause
	} else {
		rec.Counted = true
		if stored.Classification == evaluation.ClassificationSafety {
			rec.Verdict = evaluation.StatusVerdict(result.Status)
		} else {
			rec.Verdict = evaluation.ScoreVerdict(result.Score)
		}
	}
	rec.AgentFailure = copyAgentFailure(result.AgentFailure)
	if result.EvidencePath != "" {
		path := result.EvidencePath
		rec.EvidenceArtifact = &path
	}
	if result.SessionID != "" {
		id := result.SessionID
		rec.SessionID = &id
	}
	if e := result.Execution; e != nil {
		rec.AgentPrincipal = e.AgentPrincipal
		rec.ObservedModel = e.ObservedModel
		rec.NonScoringMetadata = e.NonScoringMetadata
	}
	if rec.AgentPrincipal == nil {
		rec.AgentPrincipalStatement = evaluation.AgentPrincipalNotEstablished
	}
	if len(rec.ObservedModel) == 0 {
		rec.ObservedModel = nil
		rec.ObservedModelStatement = evaluation.ObservedModelNotAvailable
	}
	stored.Detail = result
}

// scenarioRecords builds the scenario record of every applicable scenario of
// a classification, in suite order, from the evaluation's execution records.
func (s *Session) scenarioRecords(class evaluation.Classification, all []evaluation.StoredExecution) []evaluation.ScenarioRecord {
	byScenario := map[string][]evaluation.ExecutionRecord{}
	for _, e := range all {
		byScenario[e.ScenarioID] = append(byScenario[e.ScenarioID], e.AsRead())
	}
	var out []evaluation.ScenarioRecord
	for _, entry := range s.opening.Applicable(class) {
		out = append(out, evaluation.BuildScenarioRecord(entry.ScenarioID, class, s.opening.Count(class), byScenario[entry.ScenarioID]))
	}
	return out
}

// ensureSafetyVerdictRecord writes the evaluation's safety verdict record if
// every applicable safety scenario holds its counted executions and the
// record is not written yet (spec/01-core.md §2.6). It is written once,
// whatever the verdict, and is not rewritten. It returns the verdict it
// wrote, or nil where it wrote nothing.
func (s *Session) ensureSafetyVerdictRecord() (*evaluation.SafetyVerdict, error) {
	s.recordMu.Lock()
	defer s.recordMu.Unlock()

	existing, err := s.store.SafetyVerdictRecord()
	if err != nil || existing != nil {
		return nil, err
	}
	all, err := s.store.Executions()
	if err != nil {
		return nil, err
	}
	records := s.scenarioRecords(evaluation.ClassificationSafety, all)
	declared := s.opening.ExecutionCounts.Safety
	for _, r := range records {
		if r.CountedExecutions < declared {
			return nil, nil
		}
	}
	verdict := evaluation.SafetyVerdictOf(records)
	if !verdict.IsVerdict() {
		return nil, nil
	}
	rec := evaluation.SafetyVerdictRecord{
		Evaluation:      s.opening.Evaluation,
		Identity:        s.opening.Identity,
		ExecutionCounts: s.opening.ExecutionCounts,
		Safety:          verdict,
		WrittenAt:       s.o.now().UTC(),
		NotApplicable:   []evaluation.ExcludedScenario{},
		Scenarios:       records,
	}
	for _, entry := range s.opening.Scenarios {
		if entry.Classification == evaluation.ClassificationSafety && !entry.Applicable {
			rec.NotApplicable = append(rec.NotApplicable, evaluation.ExcludedScenario{
				ScenarioID: entry.ScenarioID, Category: entry.Category, Conditions: entry.ExclusionConditions,
			})
		}
	}
	data, err := record.MarshalSafetyVerdictRecord(rec)
	if err != nil {
		return nil, fmt.Errorf("marshal the safety verdict record: %w", err)
	}
	if err := s.store.WriteSafetyVerdictRecord(data); err != nil {
		if errors.Is(err, record.ErrSafetyRecordWritten) {
			return nil, nil
		}
		return nil, err
	}
	s.o.logger.Info("safety verdict record written", "evaluation", s.opening.Evaluation, "safety", string(verdict),
		"hash", evaluation.HashSafetyVerdictRecord(data))
	return &verdict, nil
}

// OwedScenario is one applicable scenario and what it holds.
type OwedScenario struct {
	ScenarioID     string                    `json:"scenario_id" yaml:"scenario_id"`
	Classification evaluation.Classification `json:"classification" yaml:"classification"`
	Declared       int                       `json:"declared" yaml:"declared"`
	Counted        int                       `json:"counted" yaml:"counted"`
	Attempts       int                       `json:"attempts" yaml:"attempts"`
	// Owed is the counted executions still to be made.
	Owed int `json:"owed" yaml:"owed"`
}

// EvaluationStatus is what an evaluation holds and what it is still owed.
type EvaluationStatus struct {
	Evaluation      string                     `json:"evaluation" yaml:"evaluation"`
	Complete        bool                       `json:"complete" yaml:"complete"`
	Safety          evaluation.SafetyVerdict   `json:"safety" yaml:"safety"`
	ExecutionCounts evaluation.ExecutionCounts `json:"execution_counts" yaml:"execution_counts"`
	// SafetyOwed and CapabilityOwed are the counted executions still to be
	// made in each phase. CapabilityOwed is what the capability scenarios
	// lack; none of it may be made until Safety is PASS, and none is ever
	// made behind a FAIL.
	SafetyOwed     int            `json:"safety_owed" yaml:"safety_owed"`
	CapabilityOwed int            `json:"capability_owed" yaml:"capability_owed"`
	Scenarios      []OwedScenario `json:"scenarios" yaml:"scenarios"`
}

// Status reads what the evaluation holds. It changes nothing.
func (s *Session) Status() (*EvaluationStatus, error) {
	all, err := s.store.Executions()
	if err != nil {
		return nil, err
	}
	st := &EvaluationStatus{Evaluation: s.opening.Evaluation, ExecutionCounts: s.opening.ExecutionCounts}
	add := func(class evaluation.Classification) (owed int, records []evaluation.ScenarioRecord) {
		records = s.scenarioRecords(class, all)
		for _, r := range records {
			o := OwedScenario{ScenarioID: r.ScenarioID, Classification: class, Declared: s.opening.Count(class), Counted: r.CountedExecutions, Attempts: r.Attempts}
			if o.Counted < o.Declared {
				o.Owed = o.Declared - o.Counted
			}
			owed += o.Owed
			st.Scenarios = append(st.Scenarios, o)
		}
		return owed, records
	}
	var safety []evaluation.ScenarioRecord
	st.SafetyOwed, safety = add(evaluation.ClassificationSafety)
	st.CapabilityOwed, _ = add(evaluation.ClassificationCapability)
	st.Safety = evaluation.SafetyVerdictOf(safety)
	st.Complete = evaluationComplete(st.SafetyOwed, st.CapabilityOwed, st.Safety)
	return st, nil
}

// evaluationComplete is spec/01-core.md §2.7: no execution remains to be
// made. Every applicable safety scenario holds the safety count, and either
// the safety verdict is FAIL, or it is PASS and every applicable capability
// scenario holds the capability count.
func evaluationComplete(safetyOwed, capabilityOwed int, safety evaluation.SafetyVerdict) bool {
	if safetyOwed > 0 {
		return false
	}
	switch safety {
	case evaluation.SafetyVerdictFail:
		return true
	case evaluation.SafetyVerdictPass:
		return capabilityOwed == 0
	}
	return false
}

// DriveSummary says where a call to Drive left the evaluation.
type DriveSummary struct {
	Status *EvaluationStatus
	// Stalled names the scenarios this process stopped executing because
	// MaxAttempts executions of each reached no verdict. They are left short
	// of their count for a later process.
	Stalled []string
}

// Drive makes every execution the evaluation is owed, safety first
// (spec/04-execution.md §3.6 steps 4 to 6). Safety scenarios are executed to
// their count and the executions go on after a FAIL; capability scenarios
// are executed only behind a safety PASS. Within each phase the order is the
// suite's, and up to Config.Parallel scenarios are executed at once; the
// executions of one scenario are never concurrent.
//
// It stops early, leaving the evaluation incomplete and resumable, when the
// agent's identity no longer matches the evaluation's, when ctx is done, and
// for any scenario whose executions keep reaching no verdict.
func (s *Session) Drive(ctx context.Context) (*DriveSummary, error) {
	summary := &DriveSummary{}
	stalled, err := s.drivePhase(ctx, evaluation.ClassificationSafety)
	summary.Stalled = append(summary.Stalled, stalled...)
	if err != nil {
		return summary, err
	}
	if _, err := s.safetyGate(); err == nil {
		stalled, err := s.drivePhase(ctx, evaluation.ClassificationCapability)
		summary.Stalled = append(summary.Stalled, stalled...)
		if err != nil {
			return summary, err
		}
	} else if !errors.Is(err, ErrSafetyNotPassed) {
		return summary, err
	}
	st, err := s.Status()
	summary.Status = st
	return summary, err
}

func (s *Session) drivePhase(ctx context.Context, class evaluation.Classification) ([]string, error) {
	entries := s.opening.Applicable(class)
	workers := s.o.cfg.Parallel
	if workers < 1 {
		workers = 1
	}
	maxAttempts := s.o.cfg.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = DefaultMaxAttempts
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		mu       sync.Mutex
		stalled  []string
		firstErr error
		wg       sync.WaitGroup
	)
	sem := make(chan struct{}, workers)
	for _, entry := range entries {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			attempts := 0
			for ctx.Err() == nil {
				out, err := s.Execute(ctx, id)
				if errors.Is(err, ErrCountReached) {
					return
				}
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					cancel()
					return
				}
				if out.Record.Counted {
					continue
				}
				attempts++
				if attempts >= maxAttempts {
					mu.Lock()
					stalled = append(stalled, id)
					mu.Unlock()
					s.o.logger.Warn("scenario left short of its count for a later process", "scenario", id, "attempts_this_process", attempts)
					return
				}
			}
		}(entry.ScenarioID)
	}
	wg.Wait()
	if firstErr != nil {
		return stalled, firstErr
	}
	return stalled, ctx.Err()
}

// Run drives one evaluation to completion: it opens the evaluation in
// Config.EvaluationDir, or resumes the one already there, makes every
// execution it is owed, and assembles the verdict and the report. A process
// that stops anywhere loses at most the execution it was making; running it
// again continues from the record.
func (o *Orchestrator) Run(
	ctx context.Context,
	profilePath string,
	scenarios []evaluation.Scenario,
	providerInfo string,
	format string,
	outputPath string,
) (*evaluation.Verdict, *DriveSummary, error) {
	var (
		session *Session
		err     error
	)
	if record.Exists(o.cfg.EvaluationDir) {
		session, err = o.ResumeEvaluation(ctx, profilePath, scenarios, providerInfo)
	} else {
		session, err = o.OpenEvaluation(ctx, profilePath, scenarios, providerInfo)
	}
	if err != nil {
		return nil, nil, err
	}
	defer session.Close()

	summary, driveErr := session.Drive(ctx)
	// Whatever stopped the drive, what the evaluation holds is assembled: an
	// incomplete evaluation reports what it holds and says it is incomplete.
	verdict, err := session.Assemble(ctx, format, outputPath)
	if driveErr != nil {
		return verdict, summary, driveErr
	}
	return verdict, summary, err
}
