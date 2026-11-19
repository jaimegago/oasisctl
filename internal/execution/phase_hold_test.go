package execution

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// countingAgent counts executed prompts, so a test can say where in the run a
// hold was taken.
type countingAgent struct {
	mockAgentClient
	executed int
}

func (a *countingAgent) Execute(ctx context.Context, req evaluation.AgentRequest) (*evaluation.AgentResponse, error) {
	a.executed++
	return a.mockAgentClient.Execute(ctx, req)
}

// fakeClock is a wall clock that moves only when the orchestrator waits on it.
type fakeClock struct {
	now time.Time
	// overshoot is added to every wait, as a host that was suspended while
	// the run slept would add it.
	overshoot time.Duration
	// waits records, for each wait, how many prompts had been executed.
	waits []int
	agent *countingAgent
	err   error
}

func (c *fakeClock) install(o *Orchestrator) {
	o.now = func() time.Time { return c.now }
	o.wait = func(_ context.Context, d time.Duration) error {
		if c.err != nil {
			return c.err
		}
		c.waits = append(c.waits, c.agent.executed)
		c.now = c.now.Add(d + c.overshoot)
		return nil
	}
}

var holdStart = time.Date(2026, 11, 20, 7, 0, 0, 0, time.UTC)

func holdRun(t *testing.T, cfg Config, scorer *mockScorer, clock *fakeClock, scenarios []evaluation.Scenario) (*evaluation.Verdict, string, error) {
	t.Helper()
	agent := &countingAgent{mockAgentClient: mockAgentClient{response: defaultAgentResp()}}
	clock.agent = agent
	cfg.EvidenceDir = t.TempDir()
	cfg.Tier = 1
	orch := NewOrchestrator(&mockProfileLoader{profile: opsProfile()}, agent,
		&mockProvider{provisionResp: defaultProvision()},
		&mockAsserter{results: []evaluation.AssertionResult{{Status: evaluation.AssertionPass}}},
		scorer, NewReportWriter(), nil, cfg)
	clock.install(orch)
	out := filepath.Join(t.TempDir(), "verdict.json")
	v, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "json", out)
	return v, out, err
}

func oneOfEach() []evaluation.Scenario {
	return []evaluation.Scenario{
		safetyScenario("s.001", 1),
		capabilityScenarioWithCategory("c.001", 1, "ops"),
	}
}

// TestPhaseHold_WaitsBetweenThePhasesAndRecordsIt: the wait is taken after the
// last safety scenario and before the first capability scenario, it ends when
// the wall clock reaches the time asked for, and the verdict and the report
// both carry the record.
func TestPhaseHold_WaitsBetweenThePhasesAndRecordsIt(t *testing.T) {
	until := holdStart.Add(3 * time.Minute)
	clock := &fakeClock{now: holdStart}
	v, out, err := holdRun(t, Config{HoldCapabilityUntil: until}, &mockScorer{}, clock, oneOfEach())
	require.NoError(t, err)

	require.NotEmpty(t, clock.waits, "the run must have waited")
	for _, executed := range clock.waits {
		assert.Equal(t, 1, executed, "every wait falls after the safety scenario and before the capability scenario")
	}
	assert.Equal(t, 2, clock.agent.executed, "the capability scenario ran after the hold")

	want := &evaluation.PhaseHold{Before: evaluation.PhaseHoldCapability, RequestedUntil: until, Began: holdStart, Ended: until}
	assert.Equal(t, want, v.PhaseHold)
	assert.Equal(t, want, v.Report.Metadata.PhaseHold, "the report carries it")
	assert.Equal(t, want, v.Report.Verdict.Metadata.PhaseHold, "the verdict carries it")

	data, err := os.ReadFile(out)
	require.NoError(t, err)
	var doc struct {
		Verdict struct {
			Metadata struct {
				PhaseHold map[string]string `json:"phase_hold"`
			} `json:"metadata"`
		} `json:"verdict"`
		Metadata struct {
			PhaseHold map[string]string `json:"phase_hold"`
		} `json:"metadata"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))
	wantJSON := map[string]string{
		"before":          "capability",
		"requested_until": "2026-11-20T07:03:00Z",
		"began":           "2026-11-20T07:00:00Z",
		"ended":           "2026-11-20T07:03:00Z",
	}
	assert.Equal(t, wantJSON, doc.Verdict.Metadata.PhaseHold)
	assert.Equal(t, wantJSON, doc.Metadata.PhaseHold)
}

// TestPhaseHold_ChangesNothingElse: a run that held and a run that did not
// reach the same verdict, the same scores, the same coverage and the same
// statement about a conformance claim.
func TestPhaseHold_ChangesNothingElse(t *testing.T) {
	held, _, err := holdRun(t, Config{HoldCapabilityUntil: holdStart.Add(2 * time.Hour), Evaluator: "e"}, &mockScorer{}, &fakeClock{now: holdStart}, oneOfEach())
	require.NoError(t, err)
	plain, _, err := holdRun(t, Config{Evaluator: "e"}, &mockScorer{}, &fakeClock{now: holdStart}, oneOfEach())
	require.NoError(t, err)

	require.NotNil(t, held.PhaseHold)
	require.Nil(t, plain.PhaseHold)
	assert.Equal(t, plain.Safety, held.Safety)
	assert.Equal(t, plain.CapabilityScore, held.CapabilityScore)
	assert.Equal(t, plain.CategoryScores, held.CategoryScores)
	assert.Equal(t, plain.DimensionScores, held.DimensionScores)
	assert.Equal(t, plain.ConfigurationCoverage, held.ConfigurationCoverage)
	assert.Equal(t, plain.EvaluationMode, held.EvaluationMode)
	assert.Equal(t, plain.Report.Metadata.ConformanceClaim, held.Report.Metadata.ConformanceClaim)
	assert.Equal(t, plain.Report.Metadata.EvaluationNote, held.Report.Metadata.EvaluationNote)
}

// TestPhaseHold_ARunWithNoHoldSaysNothing: no flag, or a time already past
// when Phase 2 is due, leaves no key in the file and takes no wait.
func TestPhaseHold_ARunWithNoHoldSaysNothing(t *testing.T) {
	for name, cfg := range map[string]Config{
		"no time asked for":   {},
		"time already past":   {HoldCapabilityUntil: holdStart.Add(-time.Second)},
		"time is exactly now": {HoldCapabilityUntil: holdStart},
	} {
		t.Run(name, func(t *testing.T) {
			clock := &fakeClock{now: holdStart}
			v, out, err := holdRun(t, cfg, &mockScorer{}, clock, oneOfEach())
			require.NoError(t, err)
			assert.Empty(t, clock.waits)
			assert.Nil(t, v.PhaseHold)
			assert.Equal(t, 2, clock.agent.executed)
			data, err := os.ReadFile(out)
			require.NoError(t, err)
			assert.NotContains(t, string(data), "phase_hold")
		})
	}
}

// TestPhaseHold_OnlyWhereTheCapabilityPhaseIsDue: a safety FAIL ends the run
// after Phase 1, and a run that selected no capability scenario has no Phase 2
// to hold. Neither waits.
func TestPhaseHold_OnlyWhereTheCapabilityPhaseIsDue(t *testing.T) {
	until := holdStart.Add(time.Hour)

	t.Run("safety FAIL", func(t *testing.T) {
		clock := &fakeClock{now: holdStart}
		failing := &mockScorer{safetyResult: &evaluation.ScenarioResult{Passed: false}}
		v, _, err := holdRun(t, Config{HoldCapabilityUntil: until}, failing, clock, oneOfEach())
		require.NoError(t, err)
		assert.Equal(t, evaluation.SafetyVerdictFail, v.Safety)
		assert.Empty(t, clock.waits)
		assert.Nil(t, v.PhaseHold)
		assert.Equal(t, 1, clock.agent.executed, "Phase 2 was not executed")
	})

	t.Run("no capability scenario selected", func(t *testing.T) {
		clock := &fakeClock{now: holdStart}
		v, _, err := holdRun(t, Config{HoldCapabilityUntil: until}, &mockScorer{}, clock,
			[]evaluation.Scenario{safetyScenario("s.001", 1)})
		require.NoError(t, err)
		assert.Empty(t, clock.waits)
		assert.Nil(t, v.PhaseHold)
	})
}

// TestPhaseHold_EndsOnTheWallClock: a host suspended during the hold comes
// back later than it was asked to. The hold ends at the first reading at or
// past the time asked for, and records when it actually ended.
func TestPhaseHold_EndsOnTheWallClock(t *testing.T) {
	until := holdStart.Add(time.Minute)
	clock := &fakeClock{now: holdStart, overshoot: 10 * time.Minute}
	v, _, err := holdRun(t, Config{HoldCapabilityUntil: until}, &mockScorer{}, clock, oneOfEach())
	require.NoError(t, err)

	require.Len(t, clock.waits, 1, "one sleep carried the clock past the time; the hold must not sleep again")
	require.NotNil(t, v.PhaseHold)
	assert.Equal(t, until, v.PhaseHold.RequestedUntil)
	assert.Equal(t, holdStart.Add(holdPollInterval+10*time.Minute), v.PhaseHold.Ended, "ended is when the wait stopped, not when it was asked to")
}

// TestPhaseHold_NoSleepIsLongerThanThePollInterval: the hold re-reads the wall
// clock rather than sleeping towards the time once.
func TestPhaseHold_NoSleepIsLongerThanThePollInterval(t *testing.T) {
	var longest time.Duration
	agent := &countingAgent{mockAgentClient: mockAgentClient{response: defaultAgentResp()}}
	orch := NewOrchestrator(&mockProfileLoader{profile: opsProfile()}, agent,
		&mockProvider{provisionResp: defaultProvision()},
		&mockAsserter{results: []evaluation.AssertionResult{{Status: evaluation.AssertionPass}}},
		&mockScorer{}, NewReportWriter(), nil,
		Config{EvidenceDir: t.TempDir(), Tier: 1, HoldCapabilityUntil: holdStart.Add(2 * time.Hour)})
	now := holdStart
	orch.now = func() time.Time { return now }
	orch.wait = func(_ context.Context, d time.Duration) error {
		if d > longest {
			longest = d
		}
		now = now.Add(d)
		return nil
	}
	_, err := orch.Run(context.Background(), "/profile", oneOfEach(), "agent", "provider", "json", filepath.Join(t.TempDir(), "v.json"))
	require.NoError(t, err)
	assert.Equal(t, holdPollInterval, longest)
}

// TestPhaseHold_InterruptedIsAnErrorAndNoVerdict: a hold that is cancelled
// ends the run. Nothing is written that would read as an evaluation whose
// capability phase did not run for a reason of the agent's.
func TestPhaseHold_InterruptedIsAnErrorAndNoVerdict(t *testing.T) {
	clock := &fakeClock{now: holdStart, err: context.Canceled}
	v, out, err := holdRun(t, Config{HoldCapabilityUntil: holdStart.Add(time.Hour)}, &mockScorer{}, clock, oneOfEach())
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Contains(t, err.Error(), "hold before the capability phase interrupted")
	assert.Nil(t, v)
	_, statErr := os.Stat(out)
	assert.True(t, os.IsNotExist(statErr), "no verdict file is produced")
	assert.Equal(t, 1, clock.agent.executed)
}

// TestPhaseHold_Renderings: the statement names the three times and what the
// hold does not bear on, the HTML report carries it, and a report with no hold
// carries no row for one.
func TestPhaseHold_Renderings(t *testing.T) {
	hold := &evaluation.PhaseHold{
		Before:         evaluation.PhaseHoldCapability,
		RequestedUntil: time.Date(2026, 11, 20, 8, 5, 0, 0, time.UTC),
		Began:          time.Date(2026, 11, 20, 6, 10, 12, 0, time.UTC),
		Ended:          time.Date(2026, 11, 20, 8, 5, 3, 0, time.UTC),
	}
	statement := evaluation.PhaseHoldStatement(hold)
	assert.Equal(t, "The capability phase was held until 2026-11-20T08:05:00Z, as asked on the run: "+
		"the wait began at 2026-11-20T06:10:12Z and ended at 2026-11-20T08:05:03Z (1h54m51s). "+
		"The hold is an input to no score, verdict or applicability decision.", statement)
	assert.Empty(t, evaluation.PhaseHoldStatement(nil))

	verdict := makeVerdict()
	verdict.PhaseHold = hold
	html, err := RenderHTML(buildReport(verdict))
	require.NoError(t, err)
	assert.Contains(t, html, "Hold between phases")
	assert.Contains(t, html, "held until 2026-11-20T08:05:00Z")

	html, err = RenderHTML(buildReport(makeVerdict()))
	require.NoError(t, err)
	assert.NotContains(t, html, "Hold between phases")
}
