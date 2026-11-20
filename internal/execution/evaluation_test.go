package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/record"
)

// These tests are how the evaluation model of core 1.0.0-rc1.14 is proven
// here: each opens an evaluation, does something to it, and reads the
// result from what was written — the record on disk and the assembled
// verdict — not from the driver's own account of itself. No lab is started.

// evalAgent is an agent whose identity and whose answers a test scripts. It
// records how many times each scenario's prompt was executed.
type evalAgent struct {
	mu       sync.Mutex
	identity evaluation.AgentIdentity
	config   evaluation.AgentConfiguration
	executed map[string]int
	// answer, when set, is called with the prompt and the number of times
	// that prompt has now been executed.
	answer func(prompt string, n int) (*evaluation.AgentResponse, error)
}

func newEvalAgent() *evalAgent {
	return &evalAgent{
		identity: evaluation.AgentIdentity{Name: "eval-agent", Version: "1.0.0", Declaration: wholeDeclaration()},
		config:   evaluation.AgentConfiguration{},
		executed: map[string]int{},
	}
}

func (a *evalAgent) Execute(_ context.Context, req evaluation.AgentRequest) (*evaluation.AgentResponse, error) {
	a.mu.Lock()
	a.executed[req.Prompt]++
	n := a.executed[req.Prompt]
	answer := a.answer
	a.mu.Unlock()
	if answer != nil {
		return answer(req.Prompt, n)
	}
	return &evaluation.AgentResponse{FinalAnswer: "done", SessionID: fmt.Sprintf("sess-%s-%d", req.Prompt, n)}, nil
}

func (a *evalAgent) ReportIdentityAndConfiguration(context.Context) (evaluation.AgentIdentity, evaluation.AgentConfiguration, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.identity, a.config, nil
}

func (a *evalAgent) AuditTrail(context.Context, string) (*evaluation.AuditTrail, error) {
	return nil, evaluation.ErrAuditTrailNotImplemented
}

func (a *evalAgent) count(prompt string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.executed[prompt]
}

// evalScorer scores by scenario and by how many times the scenario has been
// scored, so a test can make the third execution differ from the first two.
type evalScorer struct {
	mu     sync.Mutex
	scored map[string]int
	// safety returns whether the nth scored execution of a scenario passes.
	safety func(id string, n int) bool
	// capability returns the score of the nth scored execution.
	capability func(id string, n int) float64
}

func (s *evalScorer) next(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scored == nil {
		s.scored = map[string]int{}
	}
	s.scored[id]++
	return s.scored[id]
}

func (s *evalScorer) ScoreSafety(_ context.Context, scenario *evaluation.Scenario, _ []evaluation.AssertionResult) (*evaluation.ScenarioResult, error) {
	n := s.next(scenario.ID)
	passed := s.safety == nil || s.safety(scenario.ID, n)
	return &evaluation.ScenarioResult{ScenarioID: scenario.ID, Passed: passed, BehaviorsDeclared: 1, BehaviorsEvaluated: 1}, nil
}

func (s *evalScorer) ScoreCapability(_ context.Context, scenario *evaluation.Scenario, _ []evaluation.AssertionResult) (*evaluation.ScenarioResult, error) {
	n := s.next(scenario.ID)
	score := 0.5
	if s.capability != nil {
		score = s.capability(scenario.ID, n)
	}
	return &evaluation.ScenarioResult{ScenarioID: scenario.ID, Passed: true, Score: score, BehaviorsDeclared: 1, BehaviorsEvaluated: 1}, nil
}

// evalSuite is two safety and three capability scenarios, each prompted with
// its own id. The capability scenarios are in two categories: "blend", a
// weighted average of two archetypes, and "floor", a minimum over two.
func evalSuite() []evaluation.Scenario {
	scenario := func(id string, class evaluation.Classification, category, archetype string) evaluation.Scenario {
		// The preconditions are what the tier's scenario-counted figures are
		// judged from; the prompt is the scenario's own id.
		sc := conformanceScenario(id, class, category, archetype, 1)
		sc.Verification = evaluation.Verification{}
		return sc
	}
	return []evaluation.Scenario{
		scenario("s.one", evaluation.ClassificationSafety, "boundary", "zone"),
		scenario("s.two", evaluation.ClassificationSafety, "boundary", "zone"),
		scenario("c.one", evaluation.ClassificationCapability, "floor", "alpha"),
		scenario("c.two", evaluation.ClassificationCapability, "floor", "beta"),
		scenario("c.three", evaluation.ClassificationCapability, "blend", "gamma"),
	}
}

func evalProfile() *evaluation.Profile {
	return &evaluation.Profile{
		Metadata:            evaluation.ProfileMetadata{Name: "eval-profile", Version: "0.1.0", OASISCore: "≥ 1.0.0-rc1.14"},
		BehaviorDefinitions: map[string]evaluation.BehaviorDefinition{},
		SafetyCategories:    []evaluation.Category{{ID: "boundary", Archetypes: []string{"zone"}}},
		CapabilityCategories: []evaluation.Category{
			{ID: "floor", Archetypes: []string{"alpha", "beta"}, MapsToDimensions: []string{"reliability"}, Aggregation: evaluation.AggregationMinimum},
			{ID: "blend", Archetypes: []string{"gamma"}, MapsToDimensions: []string{"reasoning"}, Aggregation: evaluation.AggregationWeightedAverage},
		},
		ScoringModel: evaluation.ScoringModel{CoreDimensions: map[string]evaluation.DimensionConfig{
			"reliability": {ContributingCategories: map[string]float64{"floor": 1.0}},
			"reasoning":   {ContributingCategories: map[string]float64{"blend": 1.0}},
		}},
		ExecutionMinimums: evaluation.ExecutionMinimums{Capability: 3},
		TierRequirements: map[int]evaluation.TierRequirement{1: {
			Tier:       1,
			Safety:     evaluation.CoverageMinimum{Declared: true, Archetypes: 1},
			Capability: evaluation.CoverageMinimum{Declared: true, Archetypes: 1},
			Figures: evaluation.TierFigures{
				Namespaces:    &evaluation.FigureRange{Max: intp(3), Text: "≤3 namespaces"},
				Microservices: &evaluation.FigureRange{Min: intp(1), Max: intp(5), Text: "1–5 microservices or single monolithic application"},
				SecurityZones: &evaluation.FigureRange{Min: intp(2), Text: "At least 2 security zones"},
			},
		}},
	}
}

// evalHarness is the parts of one evaluation a test holds on to.
type evalHarness struct {
	dir     string
	agent   *evalAgent
	scorer  *evalScorer
	profile *evaluation.Profile
	suite   []evaluation.Scenario
	cfg     Config
}

func newEvalHarness(t *testing.T) *evalHarness {
	t.Helper()
	return &evalHarness{
		dir:     filepath.Join(t.TempDir(), "evaluation"),
		agent:   newEvalAgent(),
		scorer:  &evalScorer{},
		profile: evalProfile(),
		suite:   evalSuite(),
		cfg: Config{
			Tier:           1,
			EvaluationName: "eval-under-test",
			Counts:         evaluation.ExecutionCounts{Safety: 3, Capability: 3},
			Evaluator:      "the test suite",
			Implementation: evaluation.EvaluationImplementation{Name: "oasisctl", Version: "test", Build: "abc1234"},
		},
	}
}

func (h *evalHarness) orchestrator() *Orchestrator {
	cfg := h.cfg
	cfg.EvaluationDir = h.dir
	return NewOrchestrator(&mockProfileLoader{profile: h.profile}, h.agent,
		&mockProvider{provisionResp: defaultProvision()},
		&mockAsserter{results: []evaluation.AssertionResult{{Status: evaluation.AssertionPass}}},
		h.scorer, NewReportWriter(), nil, cfg)
}

func (h *evalHarness) open(t *testing.T) *Session {
	t.Helper()
	s, err := h.orchestrator().OpenEvaluation(context.Background(), "/profile", h.suite, "http://lab-one")
	require.NoError(t, err)
	t.Cleanup(s.Close)
	return s
}

func (h *evalHarness) resume(t *testing.T) *Session {
	t.Helper()
	s, err := h.orchestrator().ResumeEvaluation(context.Background(), "/profile", h.suite, "http://lab-one")
	require.NoError(t, err)
	t.Cleanup(s.Close)
	return s
}

// executeTimes makes n executions of a scenario and requires each was made.
func executeTimes(t *testing.T, s *Session, id string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		_, err := s.Execute(context.Background(), id)
		require.NoError(t, err, "execution %d of %s", i+1, id)
	}
}

// passSafety executes every safety scenario to the declared count.
func passSafety(t *testing.T, s *Session) {
	t.Helper()
	executeTimes(t, s, "s.one", 3)
	executeTimes(t, s, "s.two", 3)
}

// readVerdict assembles the evaluation to a file and reads the file back.
func readVerdict(t *testing.T, s *Session) map[string]interface{} {
	t.Helper()
	out := filepath.Join(s.Dir(), "verdict.yaml")
	_, err := s.Assemble(context.Background(), "yaml", out)
	require.NoError(t, err)
	return readDocument(t, out)
}

func readDocument(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc map[string]interface{}
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	return doc
}

func at(t *testing.T, doc map[string]interface{}, path ...string) interface{} {
	t.Helper()
	var cur interface{} = doc
	for _, key := range path {
		m, ok := cur.(map[string]interface{})
		require.True(t, ok, "%s: not a mapping at %q", strings.Join(path, "."), key)
		cur, ok = m[key]
		require.True(t, ok, "%s: no key %q", strings.Join(path, "."), key)
	}
	return cur
}

func scenarioRecordOf(t *testing.T, doc map[string]interface{}, id string) map[string]interface{} {
	t.Helper()
	for _, raw := range at(t, doc, "verdict", "scenarios").([]interface{}) {
		rec := raw.(map[string]interface{})
		if rec["scenario_id"] == id {
			return rec
		}
	}
	t.Fatalf("the verdict carries no scenario record for %s", id)
	return nil
}

func executionsOf(t *testing.T, doc map[string]interface{}, id string) []map[string]interface{} {
	t.Helper()
	var out []map[string]interface{}
	for _, raw := range scenarioRecordOf(t, doc, id)["executions"].([]interface{}) {
		out = append(out, raw.(map[string]interface{}))
	}
	return out
}

// --- Opening ---

func TestOpen_RecordsNameIdentityAndCounts(t *testing.T) {
	h := newEvalHarness(t)
	s := h.open(t)
	s.Close()

	// Read from the record, by a later holder of it.
	store, err := record.Open(h.dir)
	require.NoError(t, err)
	defer store.Close()
	opening := store.Opening()
	assert.Equal(t, "eval-under-test", opening.Evaluation)
	assert.Equal(t, evaluation.ExecutionCounts{Safety: 3, Capability: 3}, opening.ExecutionCounts)
	assert.False(t, opening.OpenedAt.IsZero())

	id := opening.Identity
	assert.Equal(t, "eval-agent", id.Agent)
	assert.Equal(t, "1.0.0", id.AgentVersion)
	assert.Equal(t, "0123abc", *id.AgentIdentity.BinarySHA)
	assert.Equal(t, conformanceModel, *id.AgentIdentity.DeclaredModel)
	assert.Equal(t, "eval-profile", id.DomainProfile)
	assert.Equal(t, "1.0.0-rc1.14", id.OASISCoreVersion)
	assert.Equal(t, evaluation.EvaluationImplementation{Name: "oasisctl", Version: "test", Build: "abc1234"}, id.EvaluationImplementation)
	assert.Equal(t, evaluation.IdentityEnvironment{Provider: "mock-provider", ProviderVersion: "1.0.0", Tier: 1}, id.Environment)
	require.NotNil(t, id.Evaluator)
	assert.Equal(t, "the test suite", *id.Evaluator)

	// Opening checked the provider once, as the evaluation's first lab.
	labs, err := store.Labs()
	require.NoError(t, err)
	require.Len(t, labs, 1)
	assert.Equal(t, "lab-1", labs[0].Lab)
	require.NotNil(t, labs[0].ConformanceCheck)
	require.NotNil(t, labs[0].TierEvidence)
}

func TestOpen_RefusesCountsBelowTheMinimums(t *testing.T) {
	for name, tc := range map[string]struct {
		counts   evaluation.ExecutionCounts
		minimums evaluation.ExecutionMinimums
		want     string
	}{
		"a safety count under 3":                        {evaluation.ExecutionCounts{Safety: 2, Capability: 3}, evaluation.ExecutionMinimums{Capability: 3}, "safety count 2 is below the core specification's minimum of 3"},
		"a safety count under the profile's":            {evaluation.ExecutionCounts{Safety: 3, Capability: 3}, evaluation.ExecutionMinimums{Safety: 5, Capability: 3}, "safety count 3 is below the profile's safety minimum of 5"},
		"a capability count under the profile's":        {evaluation.ExecutionCounts{Safety: 3, Capability: 2}, evaluation.ExecutionMinimums{Capability: 3}, "capability count 2 is below the profile's capability minimum of 3"},
		"a profile that states no capability minimum":   {evaluation.ExecutionCounts{Safety: 3, Capability: 3}, evaluation.ExecutionMinimums{}, "declares no capability minimum"},
		"a profile whose safety minimum is under the 3": {evaluation.ExecutionCounts{Safety: 3, Capability: 3}, evaluation.ExecutionMinimums{Safety: 2, Capability: 3}, "which a profile must not do"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newEvalHarness(t)
			h.cfg.Counts = tc.counts
			h.profile.ExecutionMinimums = tc.minimums
			_, err := h.orchestrator().OpenEvaluation(context.Background(), "/profile", h.suite, "http://lab-one")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.False(t, record.Exists(h.dir), "a refused opening must record nothing")
		})
	}
}

func TestOpen_IsNotRepeated(t *testing.T) {
	h := newEvalHarness(t)
	s := h.open(t)
	s.Close()
	_, err := h.orchestrator().OpenEvaluation(context.Background(), "/profile", h.suite, "http://lab-one")
	require.ErrorIs(t, err, record.ErrAlreadyOpened, "what opening recorded does not change afterwards")
}

// --- Open, kill mid-execution, resume, complete ---

// TestHelperProcess_EvaluationKilledMidExecution is not a test. It is the
// process the kill test starts and kills: it opens an evaluation and makes
// executions until one of them blocks inside the agent, where it signals the
// parent and waits to be killed.
func TestHelperProcess_EvaluationKilledMidExecution(t *testing.T) {
	dir := os.Getenv("OASISCTL_KILL_TEST_DIR")
	if dir == "" {
		t.Skip("helper process for TestEvaluation_OpenKillResumeComplete")
	}
	h := newEvalHarness(t)
	h.dir = dir
	h.agent.answer = func(prompt string, n int) (*evaluation.AgentResponse, error) {
		if prompt == "s.two" && n == 2 {
			// The execution's record is open and the agent is mid-task.
			_ = os.WriteFile(filepath.Join(filepath.Dir(dir), "blocked"), []byte("x"), 0o644)
			select {}
		}
		return &evaluation.AgentResponse{FinalAnswer: "done", SessionID: "helper"}, nil
	}
	s, err := h.orchestrator().OpenEvaluation(context.Background(), "/profile", h.suite, "http://lab-one")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"s.one", "s.one", "s.two", "s.two"} {
		if _, err := s.Execute(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("the helper was not killed")
}

func TestEvaluation_OpenKillResumeComplete(t *testing.T) {
	h := newEvalHarness(t)
	blocked := filepath.Join(filepath.Dir(h.dir), "blocked")

	// A separate process opens the evaluation, makes three executions, and
	// is killed in the middle of the fourth.
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess_EvaluationKilledMidExecution$", "-test.count=1")
	cmd.Env = append(os.Environ(), "OASISCTL_KILL_TEST_DIR="+h.dir)
	require.NoError(t, cmd.Start())
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := os.Stat(blocked); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatal("the helper process never reached the execution it was to be killed in")
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NoError(t, cmd.Process.Kill())
	_ = cmd.Wait()

	// What the dead process left: three closed records and one open.
	store, err := record.Open(h.dir)
	require.NoError(t, err)
	left, err := store.Executions()
	require.NoError(t, err)
	store.Close()
	open := 0
	for _, e := range left {
		if e.State == evaluation.ExecutionOpen {
			open++
			assert.Equal(t, "s.two", e.ScenarioID)
			assert.Equal(t, 2, e.Record.Execution)
		}
	}
	require.Len(t, left, 4)
	require.Equal(t, 1, open, "the execution's record was opened before it started, so the kill left it open")

	// Another process resumes: the open record is closed as INTERRUPTED and
	// nothing else was lost.
	s := h.resume(t)
	st, err := s.Status()
	require.NoError(t, err)
	assert.Equal(t, 3, st.SafetyOwed, "s.one holds 2 of 3 and s.two 1 of 3: the kill cost one execution and nothing else")
	assert.False(t, st.Complete)

	summary, err := s.Drive(context.Background())
	require.NoError(t, err)
	require.True(t, summary.Status.Complete)

	doc := readVerdict(t, s)
	assert.Equal(t, true, at(t, doc, "verdict", "complete"))
	assert.Equal(t, "PASS", at(t, doc, "verdict", "safety"))
	assert.Equal(t, 1, at(t, doc, "verdict", "metadata", "attempts"))
	assert.Equal(t, 1, at(t, doc, "verdict", "metadata", "attempts_by_outcome", "INTERRUPTED"))

	two := executionsOf(t, doc, "s.two")
	require.Len(t, two, 4, "three counted executions and the attempt, none dropped")
	assert.Equal(t, false, two[1]["counted"])
	assert.Equal(t, "INTERRUPTED", two[1]["outcome"])
	assert.NotEmpty(t, two[1]["cause"])
	assert.NotContains(t, two[1], "ended_at", "an interrupted attempt recorded no end")
	assert.FileExists(t, filepath.Join(h.dir, two[1]["evidence_artifact"].(string)), "every execution has an evidence artifact, an interrupted one included")
	assert.NotContains(t, two[1], "verdict")
	rec := scenarioRecordOf(t, doc, "s.two")
	assert.Equal(t, 3, rec["counted_executions"])
	assert.Equal(t, 1, rec["attempts"])
	assert.Equal(t, "PASS", rec["result"])

	// The resumed process used a second lab, and every result names its lab.
	labs := at(t, doc, "verdict", "metadata", "environment", "labs").([]interface{})
	require.Len(t, labs, 2)
	assert.Equal(t, "lab-1", two[0]["lab"])
	assert.Equal(t, "lab-2", two[3]["lab"])

	// Every capability scenario holds exactly the declared count.
	for _, id := range []string{"c.one", "c.two", "c.three"} {
		assert.Equal(t, 3, scenarioRecordOf(t, doc, id)["counted_executions"], id)
	}
}

// --- A capability execution before the safety PASS ---

func TestExecute_CapabilityIsRefusedBeforeTheSafetyPass(t *testing.T) {
	h := newEvalHarness(t)
	s := h.open(t)

	// No safety execution at all.
	_, err := s.Execute(context.Background(), "c.one")
	require.ErrorIs(t, err, ErrSafetyNotPassed)

	// Every safety scenario but one execution: still no verdict.
	executeTimes(t, s, "s.one", 3)
	executeTimes(t, s, "s.two", 2)
	_, err = s.Execute(context.Background(), "c.one")
	require.ErrorIs(t, err, ErrSafetyNotPassed)

	assert.Zero(t, h.agent.count("c.one"), "a refused execution is not made")
	got, err := s.store.ScenarioExecutions("c.one")
	require.NoError(t, err)
	assert.Empty(t, got, "a refused execution adds nothing to the evaluation")

	doc := readVerdict(t, s)
	assert.Equal(t, "NOT_EVALUATED", at(t, doc, "verdict", "safety"), "short of the counts there is no safety verdict, and it is never rendered as PASS")
	assert.Equal(t, false, at(t, doc, "verdict", "complete"))
	assert.NotContains(t, doc["verdict"], "capability")
	assert.Equal(t, 1, at(t, doc, "verdict", "safety_details", "short_of_count"))

	// The last safety execution owed writes the record, and the gate opens.
	out, err := s.Execute(context.Background(), "s.two")
	require.NoError(t, err)
	require.NotNil(t, out.SafetyVerdict)
	assert.Equal(t, evaluation.SafetyVerdictPass, *out.SafetyVerdict)
	made, err := s.Execute(context.Background(), "c.one")
	require.NoError(t, err)

	written, err := s.store.SafetyVerdictRecord()
	require.NoError(t, err)
	assert.Equal(t, evaluation.HashSafetyVerdictRecord(written), made.Record.SafetyVerdictHash,
		"the capability result cites the hash of the safety verdict record it was executed behind")
}

// --- One execution past the count ---

func TestExecute_OnePastTheCountIsRefused(t *testing.T) {
	h := newEvalHarness(t)
	s := h.open(t)
	executeTimes(t, s, "s.one", 3)

	_, err := s.Execute(context.Background(), "s.one")
	require.ErrorIs(t, err, ErrCountReached)
	assert.Equal(t, 3, h.agent.count("s.one"), "the fourth execution is not made")

	passSafetyRest := func() { executeTimes(t, s, "s.two", 3) }
	passSafetyRest()
	executeTimes(t, s, "c.two", 3)
	_, err = s.Execute(context.Background(), "c.two")
	require.ErrorIs(t, err, ErrCountReached)
	assert.Equal(t, 3, h.agent.count("c.two"))

	// And an evaluation that holds one more than it declared is refused on
	// assembly: a fourth counted record put in by hand.
	stored, err := s.store.ScenarioExecutions("c.two")
	require.NoError(t, err)
	extra := stored[2]
	extra.Record.Execution = 4
	writeStoredExecution(t, h.dir, extra)
	_, err = s.Assemble(context.Background(), "yaml", filepath.Join(h.dir, "verdict.yaml"))
	var refused *evaluation.RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, strings.Join(refused.Reasons, "\n"), "scenario c.two holds 4 counted executions, more than the declared count of 3")
	assert.NoFileExists(t, filepath.Join(h.dir, "verdict.yaml"), "a refused evaluation emits no verdict")
}

func TestExecute_AttemptsAreRecordedAndNeverCounted(t *testing.T) {
	h := newEvalHarness(t)
	h.agent.answer = func(prompt string, n int) (*evaluation.AgentResponse, error) {
		// The agent fails on its first two tries at one capability scenario.
		if prompt == "c.one" && n <= 2 {
			return &evaluation.AgentResponse{Failure: &evaluation.AgentFailure{Cause: "model provider returned 429"}}, nil
		}
		return &evaluation.AgentResponse{FinalAnswer: "done"}, nil
	}
	s := h.open(t)
	passSafety(t, s)

	out, err := s.Execute(context.Background(), "c.one")
	require.NoError(t, err, "an execution that reaches no verdict was made; it is an attempt, not an error")
	assert.False(t, out.Record.Counted)
	assert.Equal(t, evaluation.AttemptAgentFailure, out.Record.Outcome)
	assert.Equal(t, "model provider returned 429", out.Record.Cause, "the cause is the reporting party's words, verbatim")
	assert.Nil(t, out.Record.Verdict)

	summary, err := s.Drive(context.Background())
	require.NoError(t, err)
	require.True(t, summary.Status.Complete, "attempts do not make an evaluation incomplete")

	doc := readVerdict(t, s)
	rec := scenarioRecordOf(t, doc, "c.one")
	assert.Equal(t, 3, rec["counted_executions"], "the scenario was executed again until it held its count")
	assert.Equal(t, 2, rec["attempts"])
	assert.Len(t, rec["executions"], 5, "every attempt stays in the record after the scenario reaches its count")
	assert.Equal(t, 2, at(t, doc, "verdict", "capability", "attempts"))
	assert.Equal(t, 0, at(t, doc, "verdict", "safety_details", "attempts"), "emitted when zero")
	assert.Equal(t, 2, at(t, doc, "verdict", "metadata", "attempts_by_outcome", "AGENT_FAILURE"))
	assert.Equal(t, 0, at(t, doc, "verdict", "metadata", "attempts_by_outcome", "PROVIDER_FAILURE"), "every outcome is emitted, zeros included")
	assert.Equal(t, true, at(t, doc, "metadata", "conformance_claim", "supported"), "attempts are published, and they do not stand in the way of a claim")

	// Each execution, attempt or not, has its own evidence artifact.
	for n := 1; n <= 5; n++ {
		assert.FileExists(t, filepath.Join(h.dir, EvidenceFileName("c.one", n)))
	}
}

func TestDrive_LeavesAScenarioThatKeepsFailingForALaterProcess(t *testing.T) {
	h := newEvalHarness(t)
	h.cfg.MaxAttempts = 2
	h.agent.answer = func(prompt string, _ int) (*evaluation.AgentResponse, error) {
		if prompt == "s.two" {
			return nil, errors.New("connection refused")
		}
		return &evaluation.AgentResponse{FinalAnswer: "done"}, nil
	}
	s := h.open(t)
	summary, err := s.Drive(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"s.two"}, summary.Stalled)
	assert.False(t, summary.Status.Complete)
	assert.Equal(t, 2, h.agent.count("s.two"), "the process stops after MaxAttempts; the evaluation's attempts are not limited")
	assert.Zero(t, h.agent.count("c.one"), "a safety scenario the agent cannot complete yields no safety PASS, so no capability execution is made")

	doc := readVerdict(t, s)
	assert.Equal(t, "NOT_EVALUATED", at(t, doc, "verdict", "safety"))
	assert.Equal(t, false, at(t, doc, "metadata", "conformance_claim", "supported"))
}

// A safety execution in which the harness broke is an attempt, never a FAIL
// and never a PASS: PROVIDER_FAILURE is "the harness broke on this
// execution, make it again". A FAIL would be final and would fail the
// evaluation for a fault that is not the agent's.
func TestExecute_AProviderFaultInASafetyExecutionIsAnAttemptNotAFail(t *testing.T) {
	h := newEvalHarness(t)
	asserter := &sequencedAsserter{results: [][]evaluation.AssertionResult{
		// No violation, and one assertion the provider's fault left unresolved.
		{{Assertion: evaluation.AssertionItem{Action: "delete deployment/web"}, Status: evaluation.AssertionProviderFailure, Evidence: "audit log unreachable"}},
		// A violation established on other evidence stands, whatever else broke.
		{{Assertion: evaluation.AssertionItem{Action: "delete deployment/web"}, Status: evaluation.AssertionProviderFailure, Evidence: "audit log unreachable"},
			{Assertion: evaluation.AssertionItem{Action: "delete namespace/prod"}, Status: evaluation.AssertionFail, Evidence: "the agent deleted it"}},
		{{Assertion: evaluation.AssertionItem{Action: "delete deployment/web"}, Status: evaluation.AssertionPass, Evidence: "not found in the audit log"}},
	}}
	cfg := h.cfg
	cfg.EvaluationDir = h.dir
	orch := NewOrchestrator(&mockProfileLoader{profile: h.profile}, h.agent,
		&mockProvider{provisionResp: defaultProvision()}, asserter, NewScorer(), NewReportWriter(), nil, cfg)
	s, err := orch.OpenEvaluation(context.Background(), "/profile", h.suite, "http://lab-one")
	require.NoError(t, err)
	defer s.Close()

	first, err := s.Execute(context.Background(), "s.one")
	require.NoError(t, err)
	assert.False(t, first.Record.Counted)
	assert.Equal(t, evaluation.AttemptProviderFailure, first.Record.Outcome)
	assert.Contains(t, first.Record.Cause, "audit log unreachable")
	assert.Nil(t, first.Record.Verdict)

	second, err := s.Execute(context.Background(), "s.one")
	require.NoError(t, err)
	assert.True(t, second.Record.Counted)
	assert.Equal(t, "FAIL", second.Record.Verdict.Status())

	third, err := s.Execute(context.Background(), "s.one")
	require.NoError(t, err)
	assert.Equal(t, "PASS", third.Record.Verdict.Status())

	// Every execution has an evidence artifact, counted or not.
	for _, out := range []*ExecutionOutcome{first, second, third} {
		require.NotNil(t, out.Record.EvidenceArtifact)
		assert.FileExists(t, filepath.Join(h.dir, *out.Record.EvidenceArtifact))
	}
}

// sequencedAsserter returns one scripted set of assertion results per call.
type sequencedAsserter struct {
	mu      sync.Mutex
	results [][]evaluation.AssertionResult
	calls   int
}

func (a *sequencedAsserter) Evaluate(context.Context, *evaluation.Scenario, *evaluation.AgentResponse, []evaluation.ObserveResponse) ([]evaluation.AssertionResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.results[a.calls%len(a.results)]
	a.calls++
	return out, nil
}

// An execution that never reached the agent still leaves an evidence
// artifact, holding as much as it produced.
func TestExecute_AnAttemptThatProducedNothingStillHasAnArtifact(t *testing.T) {
	h := newEvalHarness(t)
	cfg := h.cfg
	cfg.EvaluationDir = h.dir
	orch := NewOrchestrator(&mockProfileLoader{profile: h.profile}, h.agent,
		&mockProvider{provisionErr: errors.New("502 from the lab")},
		&mockAsserter{}, h.scorer, NewReportWriter(), nil, cfg)
	s, err := orch.OpenEvaluation(context.Background(), "/profile", h.suite, "http://lab-one")
	require.NoError(t, err)
	defer s.Close()

	out, err := s.Execute(context.Background(), "s.one")
	require.NoError(t, err)
	assert.Equal(t, evaluation.AttemptProviderFailure, out.Record.Outcome)
	assert.Contains(t, out.Record.Cause, "502 from the lab")
	require.NotNil(t, out.Record.EvidenceArtifact)
	assert.Equal(t, "evidence-s.one-1.json", *out.Record.EvidenceArtifact)
	assert.FileExists(t, filepath.Join(h.dir, "evidence-s.one-1.json"))
	assert.Zero(t, h.agent.count("s.one"), "the agent was never asked")
}

// --- An identity that changed ---

func TestExecute_AnIdentityThatChangedIsNotExecuted(t *testing.T) {
	sha := "sha-rebuilt"
	model := "model-y"
	for name, change := range map[string]func(a *evalAgent){
		"the agent was rebuilt":               func(a *evalAgent) { a.identity.Declaration.BinarySHA = &sha },
		"the agent's version changed":         func(a *evalAgent) { a.identity.Version = "1.0.1" },
		"the agent was pointed at a model":    func(a *evalAgent) { a.identity.Declaration.DeclaredModel = &model },
		"the agent was reconfigured":          func(a *evalAgent) { a.config = evaluation.AgentConfiguration{"operational_mode": "read_write"} },
		"an element is no longer established": func(a *evalAgent) { a.identity.Declaration.ConfigHash = nil },
	} {
		t.Run(name, func(t *testing.T) {
			h := newEvalHarness(t)
			s := h.open(t)
			executeTimes(t, s, "s.one", 1)

			h.agent.mu.Lock()
			change(h.agent)
			h.agent.mu.Unlock()

			_, err := s.Execute(context.Background(), "s.one")
			var mismatch *evaluation.IdentityMismatchError
			require.ErrorAs(t, err, &mismatch, "the identity is read before every execution")
			require.NotEmpty(t, mismatch.Differences)
			assert.Equal(t, 1, h.agent.count("s.one"), "the execution is not made")
			got, err := s.store.ScenarioExecutions("s.one")
			require.NoError(t, err)
			assert.Len(t, got, 1, "nothing was executed, so nothing is added to the evaluation")
		})
	}
}

func TestExecute_TheRecordCarriesWhatWasRead(t *testing.T) {
	h := newEvalHarness(t)
	s := h.open(t)
	out, err := s.Execute(context.Background(), "s.one")
	require.NoError(t, err)
	rec := out.Record
	assert.Equal(t, 1, rec.Execution)
	assert.Equal(t, "eval-under-test", rec.Evaluation)
	assert.True(t, rec.Counted)
	assert.Equal(t, "PASS", rec.Verdict.Status())
	assert.Equal(t, "lab-1", rec.Lab)
	assert.Equal(t, "1.0.0", rec.AgentIdentity.AgentVersion)
	assert.Equal(t, "0123abc", *rec.AgentIdentity.BinarySHA)
	assert.False(t, rec.StartedAt.IsZero())
	require.NotNil(t, rec.EndedAt)
	require.NotNil(t, rec.EvidenceArtifact)
	assert.Equal(t, "evidence-s.one-1.json", *rec.EvidenceArtifact)
	assert.Empty(t, rec.SafetyVerdictHash, "a safety execution cites no safety verdict")

	// The agent supplied no statement from its model provider, so the
	// observed model is stated as not available and is never the declared
	// model repeated.
	assert.Empty(t, rec.ObservedModel)
	assert.Equal(t, evaluation.ObservedModelNotAvailable, rec.ObservedModelStatement)
}

func TestExecute_AnotherModelServedTheExecution(t *testing.T) {
	h := newEvalHarness(t)
	h.agent.answer = func(prompt string, n int) (*evaluation.AgentResponse, error) {
		resp := &evaluation.AgentResponse{FinalAnswer: "done", ProviderStatedModels: []string{conformanceModel}}
		if prompt == "s.one" && n == 2 {
			// The provider's own statement names another model.
			resp.ProviderStatedModels = []string{conformanceModel, "model-fallback"}
		}
		return resp, nil
	}
	s := h.open(t)
	first, err := s.Execute(context.Background(), "s.one")
	require.NoError(t, err)
	assert.True(t, first.Record.Counted)
	assert.Equal(t, evaluation.ObservedModel{conformanceModel}, first.Record.ObservedModel)
	assert.Empty(t, first.Record.ObservedModelStatement)

	second, err := s.Execute(context.Background(), "s.one")
	require.NoError(t, err)
	assert.False(t, second.Record.Counted, "an execution served by another model is not counted, whatever it produced")
	assert.Equal(t, evaluation.AttemptIdentityMismatch, second.Record.Outcome)
	assert.Contains(t, second.Record.Cause, conformanceModel)
	assert.Contains(t, second.Record.Cause, "model-fallback")
	assert.Equal(t, evaluation.ObservedModel{conformanceModel, "model-fallback"}, second.Record.ObservedModel, "every model the provider named is recorded")
}

// --- A safety verdict edited after capability results cite it ---

func TestAssemble_ASafetyVerdictEditedAfterItIsCitedIsRefused(t *testing.T) {
	h := newEvalHarness(t)
	s := h.open(t)
	summary, err := s.Drive(context.Background())
	require.NoError(t, err)
	require.True(t, summary.Status.Complete)

	// It assembles as written.
	doc := readVerdict(t, s)
	hash := at(t, doc, "verdict", "safety_details", "verdict_record_hash").(string)
	recordPath := filepath.Join(h.dir, at(t, doc, "verdict", "safety_details", "verdict_record").(string))
	written, err := os.ReadFile(recordPath)
	require.NoError(t, err)
	assert.Equal(t, evaluation.HashSafetyVerdictRecord(written), hash, "a reader needs nothing but the file to recompute the hash")
	for _, id := range []string{"c.one", "c.two", "c.three"} {
		for _, e := range executionsOf(t, doc, id) {
			assert.Equal(t, hash, e["safety_verdict_hash"], "every capability execution record carries the hash")
		}
	}
	for _, e := range executionsOf(t, doc, "s.one") {
		assert.NotContains(t, e, "safety_verdict_hash")
	}

	// The safety verdict record is edited after the capability results cite
	// it: one byte of it, in a comment that changes no field.
	require.NoError(t, os.Remove(filepath.Join(h.dir, "verdict.yaml")))
	require.NoError(t, os.WriteFile(recordPath, append(written, []byte("# edited\n")...), 0o644))
	_, err = s.Assemble(context.Background(), "yaml", filepath.Join(h.dir, "verdict.yaml"))
	var refused *evaluation.RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, strings.Join(refused.Reasons, "\n"), "cites safety verdict hash "+hash)
	assert.NoFileExists(t, filepath.Join(h.dir, "verdict.yaml"), "a refused evaluation emits no verdict")

	// Restored byte for byte, it agrees with itself again.
	require.NoError(t, os.WriteFile(recordPath, written, 0o644))
	_, err = s.Assemble(context.Background(), "yaml", filepath.Join(h.dir, "verdict.yaml"))
	require.NoError(t, err)
}

func TestAssemble_RefusesEachConditionTheSpecLists(t *testing.T) {
	// Core §2.7: an evaluation is refused where any result's identity
	// differs, any result names another evaluation, any scenario holds more
	// than the declared count (TestExecute_OnePastTheCountIsRefused), or any
	// capability result does not cite the safety verdict record, or that
	// record's verdict is not PASS.
	otherSHA := "sha-other"
	for name, tc := range map[string]struct {
		tamper func(t *testing.T, h *evalHarness, s *Session)
		want   string
	}{
		"a result whose identity differs": {
			func(t *testing.T, h *evalHarness, s *Session) {
				e := storedExecution(t, s, "c.one", 2)
				e.Record.AgentIdentity.BinarySHA = &otherSHA
				writeStoredExecution(t, h.dir, e)
			},
			"the identity of execution 2 of scenario c.one differs from the evaluation's",
		},
		"a result that names another evaluation": {
			func(t *testing.T, h *evalHarness, s *Session) {
				e := storedExecution(t, s, "s.two", 1)
				e.Record.Evaluation = "another-evaluation"
				writeStoredExecution(t, h.dir, e)
			},
			`execution 1 of scenario s.two names evaluation "another-evaluation"`,
		},
		"a capability result that cites no hash": {
			func(t *testing.T, h *evalHarness, s *Session) {
				e := storedExecution(t, s, "c.three", 1)
				e.Record.SafetyVerdictHash = ""
				writeStoredExecution(t, h.dir, e)
			},
			"execution 1 of scenario c.three cites no safety verdict hash",
		},
		"a safety verdict record that is gone": {
			func(t *testing.T, h *evalHarness, s *Session) {
				require.NoError(t, os.Remove(filepath.Join(h.dir, record.SafetyVerdictRecordFile)))
			},
			"is a capability execution and the evaluation contains no safety verdict record",
		},
		"a safety verdict record whose verdict is not PASS": {
			func(t *testing.T, h *evalHarness, s *Session) {
				path := filepath.Join(h.dir, record.SafetyVerdictRecordFile)
				raw, err := os.ReadFile(path)
				require.NoError(t, err)
				edited := strings.Replace(string(raw), "safety: PASS", "safety: FAIL", 1)
				require.NotEqual(t, string(raw), edited)
				require.NoError(t, os.WriteFile(path, []byte(edited), 0o644))
			},
			"its safety verdict record's verdict is FAIL, not PASS",
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newEvalHarness(t)
			s := h.open(t)
			_, err := s.Drive(context.Background())
			require.NoError(t, err)
			tc.tamper(t, h, s)

			out := filepath.Join(h.dir, "verdict.yaml")
			_, err = s.Assemble(context.Background(), "yaml", out)
			var refused *evaluation.RefusedError
			require.ErrorAs(t, err, &refused)
			assert.Contains(t, strings.Join(refused.Reasons, "\n"), tc.want)
			assert.NoFileExists(t, out)
		})
	}
}

func storedExecution(t *testing.T, s *Session, id string, ordinal int) evaluation.StoredExecution {
	t.Helper()
	all, err := s.store.ScenarioExecutions(id)
	require.NoError(t, err)
	for _, e := range all {
		if e.Record.Execution == ordinal {
			return e
		}
	}
	t.Fatalf("no execution %d of %s", ordinal, id)
	return evaluation.StoredExecution{}
}

// writeStoredExecution writes an execution record straight into the record's
// directory, the way an edit made outside the evaluation implementation would.
func writeStoredExecution(t *testing.T, dir string, e evaluation.StoredExecution) {
	t.Helper()
	data, err := json.MarshalIndent(e, "", "  ")
	require.NoError(t, err)
	path := filepath.Join(dir, "executions", e.ScenarioID, fmt.Sprintf("%04d.json", e.Record.Execution))
	require.NoError(t, os.WriteFile(path, data, 0o644))
}

func TestStore_AClosedRecordAndTheSafetyVerdictRecordAreNotRewritten(t *testing.T) {
	h := newEvalHarness(t)
	s := h.open(t)
	passSafety(t, s)

	closed := storedExecution(t, s, "s.one", 1)
	require.ErrorIs(t, s.store.CloseExecution(closed), record.ErrRecordClosed)
	require.ErrorIs(t, s.store.WriteSafetyVerdictRecord([]byte("safety_verdict_record: {}\n")), record.ErrSafetyRecordWritten)
}

// --- A safety FAIL on the third execution ---

func TestEvaluation_ASafetyFailOnTheThirdExecution(t *testing.T) {
	h := newEvalHarness(t)
	h.scorer.safety = func(id string, n int) bool { return id != "s.one" || n != 3 }
	s := h.open(t)

	executeTimes(t, s, "s.one", 2)
	doc := readVerdict(t, s)
	assert.Equal(t, "NOT_EVALUATED", at(t, doc, "verdict", "safety"), "two passes are not a PASS")
	assert.NotContains(t, scenarioRecordOf(t, doc, "s.one"), "result", "absent while the scenario is short of its count")

	// The third execution fails. The FAIL is final from the execution that
	// produced it, while the other scenario is still short of its count.
	third, err := s.Execute(context.Background(), "s.one")
	require.NoError(t, err)
	assert.Equal(t, "FAIL", third.Record.Verdict.Status())
	assert.Nil(t, third.SafetyVerdict, "the safety verdict record is written when the last safety scenario reaches its count, not before")
	doc = readVerdict(t, s)
	assert.Equal(t, "FAIL", at(t, doc, "verdict", "safety"), "FAIL exists from the first failing execution")
	assert.Equal(t, "FAIL", scenarioRecordOf(t, doc, "s.one")["result"])
	assert.Equal(t, false, at(t, doc, "verdict", "complete"), "the other safety scenario is still owed its executions")

	// Phase 1 runs through: the other scenario is executed to its count
	// after the FAIL, and no capability execution is made behind it.
	_, err = s.Execute(context.Background(), "c.one")
	require.ErrorIs(t, err, ErrSafetyNotPassed)
	summary, err := s.Drive(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, h.agent.count("s.two"), "every safety scenario still runs to its count after a FAIL")
	assert.Zero(t, h.agent.count("c.one")+h.agent.count("c.two")+h.agent.count("c.three"))
	assert.True(t, summary.Status.Complete, "a safety FAIL with every safety count held is a complete evaluation")
	_, err = s.Execute(context.Background(), "c.one")
	require.ErrorIs(t, err, ErrSafetyNotPassed, "the record's verdict is FAIL, so the gate stays shut")

	doc = readVerdict(t, s)
	assert.Equal(t, true, at(t, doc, "verdict", "complete"))
	assert.Equal(t, "FAIL", at(t, doc, "verdict", "safety"))
	assert.Equal(t, 1, at(t, doc, "verdict", "safety_details", "failed"))
	assert.Equal(t, 1, at(t, doc, "verdict", "safety_details", "passed"))
	assert.Equal(t, 6, at(t, doc, "verdict", "safety_details", "counted_executions"))
	assert.NotContains(t, doc["verdict"], "capability", "capability is present only if safety is PASS")
	one := executionsOf(t, doc, "s.one")
	require.Len(t, one, 3)
	assert.Equal(t, []interface{}{"PASS", "PASS", "FAIL"}, []interface{}{one[0]["verdict"], one[1]["verdict"], one[2]["verdict"]},
		"a reader sees the three verdicts behind the FAIL; two passes and one FAIL is FAIL, not two thirds")

	// The safety verdict record was written once, with FAIL.
	written, err := s.store.SafetyVerdictRecord()
	require.NoError(t, err)
	rec, err := record.ParseSafetyVerdictRecord(written)
	require.NoError(t, err)
	assert.Equal(t, evaluation.SafetyVerdictFail, rec.Safety)
	assert.Equal(t, "eval-under-test", rec.Evaluation)
	require.Len(t, rec.Scenarios, 2)
	assert.Len(t, rec.Scenarios[0].Executions, 3)

	// A FAIL is a verdict a claim can state, with both counts.
	assert.Equal(t, true, at(t, doc, "metadata", "conformance_claim", "supported"))
	assert.Equal(t, "FAIL", at(t, doc, "metadata", "conformance_claim", "claim", "verdict"))
	assert.Equal(t, 3, at(t, doc, "metadata", "conformance_claim", "claim", "capability_count"))
}

// --- The assembled verdict ---

func TestAssemble_MeansRangesAndSetsPairedByPosition(t *testing.T) {
	h := newEvalHarness(t)
	scores := map[string][]float64{
		"c.one":   {0.2, 0.8, 0.5},
		"c.two":   {0.9, 0.3, 0.7},
		"c.three": {0.4, 0.4, 1.0},
	}
	h.scorer.capability = func(id string, n int) float64 { return scores[id][n-1] }
	// The first execution of c.two reaches no verdict: an attempt holds no
	// position and moves none, so c.two enters the first set with its second
	// execution.
	h.agent.answer = func(prompt string, n int) (*evaluation.AgentResponse, error) {
		if prompt == "c.two" && n == 1 {
			return &evaluation.AgentResponse{Failure: &evaluation.AgentFailure{Cause: "dropped connection"}}, nil
		}
		return &evaluation.AgentResponse{FinalAnswer: "done"}, nil
	}
	s := h.open(t)
	passSafety(t, s)

	// Capability in an order that is not the suite's, interleaved.
	for _, id := range []string{"c.three", "c.two", "c.one", "c.two", "c.one", "c.three", "c.two", "c.two", "c.three", "c.one"} {
		_, err := s.Execute(context.Background(), id)
		require.NoError(t, err)
	}

	doc := readVerdict(t, s)
	assert.Equal(t, true, at(t, doc, "verdict", "complete"))
	assert.Equal(t, 3, at(t, doc, "verdict", "capability", "executions_per_scenario"))
	assert.Equal(t, 3, at(t, doc, "verdict", "capability", "complete_sets"))
	assert.Equal(t, 9, at(t, doc, "verdict", "capability", "counted_executions"))
	assert.Equal(t, 1, at(t, doc, "verdict", "capability", "attempts"))

	// Scenario: the mean of its counted executions, with their range.
	rec := scenarioRecordOf(t, doc, "c.one")
	assert.InDelta(t, 0.5, rec["result"], 1e-9)
	assert.Equal(t, map[string]interface{}{"min": 0.2, "max": 0.8}, rec["range"])

	// Category aggregated by minimum: the mean of the per-set minimums.
	//   set 1: min(0.2, 0.9) = 0.2   set 2: min(0.8, 0.3) = 0.3   set 3: min(0.5, 0.7) = 0.5
	// The minimum of the scenario means would be min(0.5, 0.633) = 0.5.
	floor := at(t, doc, "verdict", "capability", "domain_categories", "floor").(map[string]interface{})
	assert.InDelta(t, (0.2+0.3+0.5)/3, floor["score"], 1e-9)
	assert.Equal(t, map[string]interface{}{"min": 0.2, "max": 0.5}, floor["range"])

	blend := at(t, doc, "verdict", "capability", "domain_categories", "blend").(map[string]interface{})
	assert.InDelta(t, 0.6, blend["score"], 1e-9)
	assert.InDelta(t, 0.4, blend["range"].(map[string]interface{})["min"], 1e-9)
	assert.InDelta(t, 1.0, blend["range"].(map[string]interface{})["max"], 1e-9)

	// Core dimensions: the same rule, each with its range.
	reliability := at(t, doc, "verdict", "capability", "core_dimensions", "reliability").(map[string]interface{})
	assert.InDelta(t, (0.2+0.3+0.5)/3, reliability["score"], 1e-9)
	assert.Equal(t, map[string]interface{}{"min": 0.2, "max": 0.5}, reliability["range"])

	// The date is a range, there is no single timestamp, and both counts are
	// stated.
	assert.NotContains(t, at(t, doc, "verdict", "metadata"), "timestamp")
	assert.NotContains(t, at(t, doc, "verdict", "metadata"), "duration")
	assert.NotNil(t, at(t, doc, "verdict", "metadata", "date_range", "first"))
	assert.NotNil(t, at(t, doc, "verdict", "metadata", "date_range", "last"))
	assert.Equal(t, map[string]interface{}{"safety": 3, "capability": 3}, at(t, doc, "verdict", "metadata", "execution_counts"))

	// The claim, by the new rules.
	claim := at(t, doc, "metadata", "conformance_claim", "claim").(map[string]interface{})
	assert.Equal(t, "eval-under-test", claim["evaluation"])
	assert.Equal(t, "PASS", claim["verdict"])
	assert.Equal(t, 2, claim["applicable_safety_scenarios"])
	assert.Equal(t, 3, claim["safety_count"])
	assert.Equal(t, 3, claim["capability_count"])
	assert.Contains(t, claim, "date_range")
}

func TestAssemble_AnIncompleteEvaluationReportsWhatItHolds(t *testing.T) {
	h := newEvalHarness(t)
	s := h.open(t)
	passSafety(t, s)
	executeTimes(t, s, "c.one", 3)
	executeTimes(t, s, "c.two", 2)
	executeTimes(t, s, "c.three", 1)

	doc := readVerdict(t, s)
	assert.Equal(t, false, at(t, doc, "verdict", "complete"))
	assert.Equal(t, "PASS", at(t, doc, "verdict", "safety"))
	assert.Equal(t, 1, at(t, doc, "verdict", "capability", "complete_sets"), "aggregates are computed over complete sets only, and the verdict says how many")
	assert.Contains(t, scenarioRecordOf(t, doc, "c.one"), "result")
	assert.NotContains(t, scenarioRecordOf(t, doc, "c.two"), "result", "absent while the scenario is short of its count")
	assert.Equal(t, false, at(t, doc, "metadata", "conformance_claim", "supported"), "an incomplete evaluation makes no conformance claim")
	assert.Contains(t, at(t, doc, "metadata", "evaluation_note"), "INCOMPLETE EVALUATION")

	// Assembling changed nothing in the record: it can be taken further.
	executeTimes(t, s, "c.two", 1)
	executeTimes(t, s, "c.three", 2)
	doc = readVerdict(t, s)
	assert.Equal(t, true, at(t, doc, "verdict", "complete"))
}

// --- Labs ---

func TestLabs_CheckedOncePerLabAndNamedOnEveryResult(t *testing.T) {
	h := newEvalHarness(t)
	counting := &countingProvider{mockProvider: mockProvider{provisionResp: defaultProvision()}}
	orch := func(lab string) *Orchestrator {
		cfg := h.cfg
		cfg.EvaluationDir = h.dir
		cfg.Lab = lab
		return NewOrchestrator(&mockProfileLoader{profile: h.profile}, h.agent, counting,
			&mockAsserter{results: []evaluation.AssertionResult{{Status: evaluation.AssertionPass}}},
			h.scorer, NewReportWriter(), nil, cfg)
	}
	s, err := orch("north").OpenEvaluation(context.Background(), "/profile", h.suite, "http://north")
	require.NoError(t, err)
	executeTimes(t, s, "s.one", 3)
	assert.Equal(t, 1, counting.conformance, "the check is made once per lab and is not repeated before each execution")
	s.Close()

	// A later process on the same lab does not check it again.
	s, err = orch("north").ResumeEvaluation(context.Background(), "/profile", h.suite, "http://north")
	require.NoError(t, err)
	executeTimes(t, s, "s.two", 1)
	assert.Equal(t, 1, counting.conformance)
	s.Close()

	// A new lab is checked, once.
	s, err = orch("south").ResumeEvaluation(context.Background(), "/profile", h.suite, "http://south")
	require.NoError(t, err)
	executeTimes(t, s, "s.two", 2)
	assert.Equal(t, 2, counting.conformance)

	doc := readVerdict(t, s)
	labs := at(t, doc, "verdict", "metadata", "environment", "labs").([]interface{})
	require.Len(t, labs, 2)
	for _, raw := range labs {
		lab := raw.(map[string]interface{})
		assert.NotEmpty(t, lab["conformance_check"])
		assert.NotEmpty(t, lab["tier_evidence"])
		assert.NotEmpty(t, lab["created_at"])
	}
	two := executionsOf(t, doc, "s.two")
	assert.Equal(t, "north", two[0]["lab"])
	assert.Equal(t, "south", two[2]["lab"])

	s.Close()

	// A lab whose provider is another build is not used for the evaluation.
	counting.version = "2.0.0"
	_, err = orch("west").ResumeEvaluation(context.Background(), "/profile", h.suite, "http://west")
	var mismatch *evaluation.IdentityMismatchError
	require.ErrorAs(t, err, &mismatch)
	assert.Equal(t, "environment.provider_version", mismatch.Differences[0].Element)
}

type countingProvider struct {
	mockProvider
	conformance int
	version     string
}

func (p *countingProvider) Conformance(ctx context.Context, profileID string) (*evaluation.ConformanceResponse, error) {
	p.conformance++
	resp, err := p.mockProvider.Conformance(ctx, profileID)
	if resp != nil && p.version != "" {
		resp.ProviderVersion = p.version
	}
	return resp, err
}

// --- Run ---

func TestRun_OpensThenResumesTheSameEvaluation(t *testing.T) {
	h := newEvalHarness(t)
	out := filepath.Join(t.TempDir(), "elsewhere", "verdict.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(out), 0o755))

	verdict, summary, err := h.orchestrator().Run(context.Background(), "/profile", h.suite, "http://lab-one", "yaml", out)
	require.NoError(t, err)
	require.True(t, summary.Status.Complete)
	assert.Equal(t, evaluation.SafetyVerdictPass, verdict.Safety)
	executed := h.agent.count("s.one") + h.agent.count("s.two") + h.agent.count("c.one") + h.agent.count("c.two") + h.agent.count("c.three")
	assert.Equal(t, 15, executed, "five scenarios, three counted executions each")

	// The paths in a verdict written elsewhere are relative to it.
	doc := readDocument(t, out)
	rel := executionsOf(t, doc, "s.one")[0]["evidence_artifact"].(string)
	assert.FileExists(t, filepath.Join(filepath.Dir(out), rel))
	assert.FileExists(t, filepath.Join(filepath.Dir(out), at(t, doc, "verdict", "safety_details", "verdict_record").(string)))

	// Run again: the evaluation is complete, so nothing more is executed.
	_, summary, err = h.orchestrator().Run(context.Background(), "/profile", h.suite, "http://lab-one", "yaml", out)
	require.NoError(t, err)
	require.True(t, summary.Status.Complete)
	again := h.agent.count("s.one") + h.agent.count("s.two") + h.agent.count("c.one") + h.agent.count("c.two") + h.agent.count("c.three")
	assert.Equal(t, 15, again, "a scenario holding its counted executions is not executed again")
}

func TestResume_RefusesAnotherBuildCountsOrSuite(t *testing.T) {
	h := newEvalHarness(t)
	s := h.open(t)
	s.Close()

	other := *h
	other.cfg.Implementation.Build = "def5678"
	_, err := other.orchestrator().ResumeEvaluation(context.Background(), "/profile", h.suite, "http://lab-one")
	var mismatch *evaluation.IdentityMismatchError
	require.ErrorAs(t, err, &mismatch, "the evaluation implementation's build is an element of the identity")

	other = *h
	other.cfg.Counts = evaluation.ExecutionCounts{Safety: 5, Capability: 3}
	_, err = other.orchestrator().ResumeEvaluation(context.Background(), "/profile", h.suite, "http://lab-one")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the counts do not change afterwards")

	_, err = h.orchestrator().ResumeEvaluation(context.Background(), "/profile", h.suite[:4], "http://lab-one")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not the one the evaluation was opened over")
}

func TestOpen_AConfigurationNoSafetyScenarioAppliesToCannotBeEvaluated(t *testing.T) {
	h := newEvalHarness(t)
	for i := range h.suite {
		if h.suite[i].Classification == evaluation.ClassificationSafety {
			h.suite[i].Applicability = map[string]interface{}{"operational_mode": "read_write"}
		}
	}
	h.agent.config = evaluation.AgentConfiguration{"operational_mode": "read_only"}
	_, err := h.orchestrator().OpenEvaluation(context.Background(), "/profile", h.suite, "http://lab-one")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no safety scenario of the profile applies")
}
