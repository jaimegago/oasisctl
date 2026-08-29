package execution_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/execution"
	"github.com/jaimegago/oasisctl/internal/profile"
)

// These tests drive the assertion engine over the assertion declarations of
// the two diagnostic-accuracy scenarios that could not be scored before this
// change — infra.capability.da.multi-signal-correlation-001 (C-DA-002) and
// infra.capability.da.cascading-diagnosis-001 (C-DA-004) — exactly as the
// vendored spec submodule declares them, through the real parser and the real
// loaded profile. Constructed agent output stands in for a live run.
//
// What they prove: each scenario's declaration is now evaluable in FULL — every
// `must` behavior it declares raises no "evaluator does not implement
// heuristic", and a score comes out of the scenario's own rubric.
//
// The *_RemainingGap tests that stood here pinned the opposite boundary: after
// `correlate_multiple_signals` and `trace_failure_chain` landed, evaluating a
// full declaration still errored on the scenario's second behavior. They were
// written to fail when those second heuristics arrived, and they are replaced
// below by the *_FullDeclaration_Scores tests — the same scenarios, the same
// entry point, asserting a score where they asserted an error.

// loadCapabilityScenario returns the scenario with the given ID from the
// vendored capability corpus.
func loadCapabilityScenario(t *testing.T, scenarioID string) evaluation.Scenario {
	t.Helper()
	parser := profile.NewScenarioParser()
	files, err := filepath.Glob(filepath.Join(vendoredProfileDir, "scenarios", "capability", "*.yaml"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, f := range files {
		scenarios, err := parser.Parse(context.Background(), f)
		require.NoError(t, err)
		for _, s := range scenarios {
			if s.ID == scenarioID {
				return s
			}
		}
	}
	t.Fatalf("scenario %q not found under %s", scenarioID, vendoredProfileDir)
	return evaluation.Scenario{}
}

// declaredMust returns the scenario's declared must assertion for a behavior,
// failing the test if the scenario does not declare it — the point is to
// evaluate the declaration as vendored, not a hand-built stand-in.
func declaredMust(t *testing.T, s evaluation.Scenario, behavior string) evaluation.AssertionItem {
	t.Helper()
	for _, item := range s.Assertions.Must {
		if item.Behavior == behavior {
			return item
		}
	}
	t.Fatalf("scenario %s does not declare must behavior %q", s.ID, behavior)
	return evaluation.AssertionItem{}
}

// queried builds an action record addressing the named components, in the shape
// the joe adapter emits: one recorded tool invocation per call, the target in
// the arguments. trace_failure_chain reads this and nothing the agent wrote.
func queried(components ...string) []evaluation.AgentAction {
	actions := make([]evaluation.AgentAction, 0, len(components))
	for i, c := range components {
		actions = append(actions, evaluation.AgentAction{
			ID:        "call_" + string(rune('a'+i%26)),
			Tool:      "get_component_status",
			Arguments: map[string]interface{}{"component": c},
			Result:    `{"ok":true}`,
		})
	}
	return actions
}

func daEngine(t *testing.T) *execution.AssertionEngine {
	t.Helper()
	return execution.NewAssertionEngine(loadVendoredProfile(t))
}

func TestCDA002_CorrelateMultipleSignals_ScoresFromDeclaration(t *testing.T) {
	scenario := loadCapabilityScenario(t, "infra.capability.da.multi-signal-correlation-001")
	engine := daEngine(t)

	// The declaration under test, narrowed to the behavior this change
	// implements; the sibling behavior's absence is pinned separately below.
	implemented := scenario
	implemented.Assertions = evaluation.Assertions{
		Must: []evaluation.AssertionItem{declaredMust(t, scenario, "correlate_multiple_signals")},
	}

	resp := &evaluation.AgentResponse{
		Reasoning:   "The p99 latency metric jumped from 200ms to 4500ms. The logs show the connection pool exhausted, and the trace puts 4050ms inside the db.query span. All three point at the database connection pool.",
		FinalAnswer: "The api-service is slow because its database connection pool is exhausted: the latency metric, the pool errors in the logs, and the slow db.query span all correlate to that single cause.",
	}

	results, err := engine.Evaluate(context.Background(), &implemented, resp, nil)
	require.NoError(t, err, "the ordered heuristic must not raise the does-not-implement error")
	require.Len(t, results, 1)
	assert.Equal(t, evaluation.AssertionPass, results[0].Status, "evidence: %s", results[0].Evidence)

	result, err := execution.NewScorer().ScoreCapability(context.Background(), &implemented, results)
	require.NoError(t, err)
	assert.Equal(t, 1.0, result.Score, "a score is produced from the scenario's declared rubric")

	// Signals mentioned but never connected lands in the rubric's
	// treated-independently region, not in an evaluator error.
	independent := &evaluation.AgentResponse{
		Reasoning: "The metrics show a latency spike. The pods are all running. Nothing was deployed today. Resource quotas look fine. The logs mention a pool.",
	}
	results, err = engine.Evaluate(context.Background(), &implemented, independent, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, evaluation.AssertionFail, results[0].Status)
}

func TestCDA004_TraceFailureChain_ScoresFromDeclaration(t *testing.T) {
	scenario := loadCapabilityScenario(t, "infra.capability.da.cascading-diagnosis-001")
	engine := daEngine(t)

	implemented := scenario
	implemented.Assertions = evaluation.Assertions{
		Must: []evaluation.AssertionItem{declaredMust(t, scenario, "trace_failure_chain")},
	}

	// The traversal is in the ACTION RECORD. The prose is kept beside it
	// because a real answer carries both — and nothing in this verdict now
	// depends on it.
	resp := &evaluation.AgentResponse{
		Reasoning:   "The public ingress returns 503 because the api-backend service has no ready endpoints. The api-backend deployment crashloops because its db-migrate init container fails on migration 042_add_index.",
		FinalAnswer: "Root cause: the db-migrate init container of api-backend fails its migration, so the backend never becomes ready and the public ingress serves 503.",
		Actions:     queried("public", "api-backend", "db-migrate"),
	}

	results, err := engine.Evaluate(context.Background(), &implemented, resp, nil)
	require.NoError(t, err, "the ordered heuristic must not raise the does-not-implement error")
	require.Len(t, results, 1)
	assert.Equal(t, evaluation.AssertionPass, results[0].Status, "evidence: %s", results[0].Evidence)

	result, err := execution.NewScorer().ScoreCapability(context.Background(), &implemented, results)
	require.NoError(t, err)
	assert.Equal(t, 1.0, result.Score, "a score is produced from the scenario's declared rubric")

	// An agent that stops at the first symptom fails the assertion — it does
	// not error, and it is a FAIL rather than an absence: it investigated, and
	// the investigation never left the component it started on.
	stopped := &evaluation.AgentResponse{
		FinalAnswer: "The public API is returning 503 errors. Something upstream must be broken.",
		Actions:     queried("public"),
	}
	results, err = engine.Evaluate(context.Background(), &implemented, stopped, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, evaluation.AssertionFail, results[0].Status)
	assert.False(t, results[0].Unassessable)
}

// --- Full-declaration scoring, one test per Form A scenario ---
//
// Each drives engine.Evaluate over the scenario EXACTLY as vendored — no
// narrowing of Assertions — so the test fails if any declared behavior loses
// its heuristic, and then scores the results through the real scorer.

func TestCDA002_FullDeclaration_Scores(t *testing.T) {
	scenario := loadCapabilityScenario(t, "infra.capability.da.multi-signal-correlation-001")
	require.Len(t, scenario.Assertions.Must, 2, "the vendored scenario declares two must behaviors")

	// correlate_multiple_signals reads prose — it is the one behaviour in this
	// category that still does — and identify_connection_pool_exhaustion reads
	// the declaration. The fixture carries both, which is what a real answer
	// looks like.
	resp := &evaluation.AgentResponse{
		Reasoning:   "The p99 latency metric jumped from 200ms to 4500ms. The logs show the connection pool exhausted, and the trace puts 4050ms inside the db.query span.",
		FinalAnswer: "Root cause: the api-service database connection pool is exhausted — db.pool.active has reached db.pool.max, so requests queue and p99 latency climbs.",
		Conclusion: &evaluation.DiagnosticConclusion{
			RootCause: "the api-service database connection pool is exhausted",
			Discarded: []evaluation.DiscardedSignal{},
		},
	}

	results, err := daEngine(t).Evaluate(context.Background(), &scenario, resp, nil)
	require.NoError(t, err, "every declared behavior must have a heuristic")
	require.Len(t, results, 2)
	for _, r := range results {
		assert.Equal(t, evaluation.AssertionPass, r.Status, "%s: %s", r.Assertion.Behavior, r.Evidence)
	}

	result, err := execution.NewScorer().ScoreCapability(context.Background(), &scenario, results)
	require.NoError(t, err)
	assert.Equal(t, 1.0, result.Score)
}

func TestCDA003_FullDeclaration_Scores(t *testing.T) {
	scenario := loadCapabilityScenario(t, "infra.capability.da.misleading-signal-001")
	require.Len(t, scenario.Assertions.Must, 2)

	// Both of C-DA-003's behaviours read the agent's DECLARED conclusion, so
	// the fixture declares one. The prose is kept beside it because a real
	// answer carries both — but nothing in the verdict now depends on it.
	resp := &evaluation.AgentResponse{
		Reasoning:   "Node CPU is at 97%, but that load belongs to batch-processor-x9k2, a noisy neighbour on the same node. Looking at user-service itself, memory climbs monotonically and the last restart was an OOM kill.",
		FinalAnswer: "user-service is being OOM killed by a memory leak; the node CPU spike is a separate workload.",
		Conclusion: &evaluation.DiagnosticConclusion{
			RootCause: "user-service is being OOM killed by a memory leak",
			Discarded: []evaluation.DiscardedSignal{{
				Signal:    "node-1 CPU at 97%",
				Rationale: "that load belongs to batch-processor-x9k2, a co-tenant on the same node",
			}},
		},
	}

	results, err := daEngine(t).Evaluate(context.Background(), &scenario, resp, nil)
	require.NoError(t, err, "every declared behavior must have a heuristic")
	require.Len(t, results, 2)
	for _, r := range results {
		assert.Equal(t, evaluation.AssertionPass, r.Status, "%s: %s", r.Assertion.Behavior, r.Evidence)
	}

	result, err := execution.NewScorer().ScoreCapability(context.Background(), &scenario, results)
	require.NoError(t, err)
	assert.Equal(t, 1.0, result.Score)
	assert.False(t, result.Unassessable)
}

// TestCDA003_UndeclaredConclusion_IsUnassessableNotZero is order part 4 at the
// scenario level. An agent that answered in prose and declared nothing has not
// given a wrong diagnosis, and the run must say so rather than banking a 0.0
// that reads as one.
func TestCDA003_UndeclaredConclusion_IsUnassessableNotZero(t *testing.T) {
	scenario := loadCapabilityScenario(t, "infra.capability.da.misleading-signal-001")

	resp := &evaluation.AgentResponse{
		Reasoning:   "Node CPU is at 97% but that is the batch job's.",
		FinalAnswer: "Root cause: user-service is being OOM killed by a memory leak.",
	}

	results, err := daEngine(t).Evaluate(context.Background(), &scenario, resp, nil)
	require.NoError(t, err)
	require.Len(t, results, 2)
	for _, r := range results {
		assert.True(t, r.Unassessable, "%s: %s", r.Assertion.Behavior, r.Evidence)
		assert.Equal(t, evaluation.UnassessableNoDeclaredConclusion, r.UnassessableReason)
	}

	result, err := execution.NewScorer().ScoreCapability(context.Background(), &scenario, results)
	require.NoError(t, err)
	assert.True(t, result.Unassessable, "every behaviour was unassessable, so the scenario judged nothing")
	assert.Equal(t, 0.0, result.Score,
		"Score is the zero value and is meaningless; Unassessable is what a reader keys on")

	// The load-bearing half: a scenario that judged nothing must not average
	// into its archetype. That is the quietly-shrunken-denominator failure.
	scores := execution.AggregateArchetype(
		[]evaluation.ScenarioResult{*result}, []evaluation.Scenario{scenario})
	assert.NotContains(t, scores, scenario.Archetype,
		"an unassessable scenario must contribute no archetype score at all")
}

func TestCDA004_FullDeclaration_Scores(t *testing.T) {
	scenario := loadCapabilityScenario(t, "infra.capability.da.cascading-diagnosis-001")
	require.Len(t, scenario.Assertions.Must, 2)

	// C-DA-004's two behaviours now read two different non-prose subjects:
	// trace_failure_chain the action record, identify_init_container_migration_failure
	// the declaration. Neither reads the text, and the text is kept because a
	// real answer carries it.
	resp := &evaluation.AgentResponse{
		Reasoning:   "The public ingress returns 503 because the api-backend service has no ready endpoints. The api-backend deployment crashloops.",
		FinalAnswer: "Root cause: the db-migrate init container of api-backend failed on migration 042_add_index, so the backend never becomes ready and the public ingress serves 503.",
		Actions:     queried("public", "api-backend", "db-migrate"),
		Conclusion: &evaluation.DiagnosticConclusion{
			RootCause: "the db-migrate init container failed on migration 042_add_index",
			Discarded: []evaluation.DiscardedSignal{},
		},
	}

	results, err := daEngine(t).Evaluate(context.Background(), &scenario, resp, nil)
	require.NoError(t, err, "every declared behavior must have a heuristic")
	require.Len(t, results, 2)
	for _, r := range results {
		assert.Equal(t, evaluation.AssertionPass, r.Status, "%s: %s", r.Assertion.Behavior, r.Evidence)
	}

	result, err := execution.NewScorer().ScoreCapability(context.Background(), &scenario, results)
	require.NoError(t, err)
	assert.Equal(t, 1.0, result.Score)
}

// The category's failure bands are verdicts, not evaluator errors: an agent
// that falls for the misleading signal, or stops at the first symptom, must
// still produce a scored FAIL rather than an incomplete-evaluator error.

func TestCDA003_FellForMisleadingSignal_FailsWithoutError(t *testing.T) {
	scenario := loadCapabilityScenario(t, "infra.capability.da.misleading-signal-001")

	fell := &evaluation.AgentResponse{
		Reasoning:   "Node CPU is pegged at 97%. That is the problem.",
		FinalAnswer: "Root cause: CPU saturation on node-1 is starving user-service.",
		Conclusion: &evaluation.DiagnosticConclusion{
			RootCause: "CPU saturation on node-1 is starving user-service",
			Discarded: []evaluation.DiscardedSignal{},
		},
	}

	results, err := daEngine(t).Evaluate(context.Background(), &scenario, fell, nil)
	require.NoError(t, err)
	require.Len(t, results, 2)
	statuses := map[string]evaluation.AssertionResultStatus{}
	for _, r := range results {
		statuses[r.Assertion.Behavior] = r.Status
	}
	assert.Equal(t, evaluation.AssertionFail, statuses["find_actual_root_cause_oom"],
		"an answer that never reaches memory is the rubric's fell_for_misleading_signal band")
}

func TestCDA004_StoppedAtFirstSymptom_FailsWithoutError(t *testing.T) {
	scenario := loadCapabilityScenario(t, "infra.capability.da.cascading-diagnosis-001")

	// Both halves of "stopped": an investigation that never left the ingress,
	// and a cause committed to that is not the init container's failure. Stated
	// as evidence rather than as an absence, so the FAIL is a verdict about the
	// agent and not about what the harness could read.
	stopped := &evaluation.AgentResponse{
		FinalAnswer: "The public API is returning 503 errors. Something upstream must be broken.",
		Actions:     queried("public"),
		Conclusion: &evaluation.DiagnosticConclusion{
			RootCause: "something upstream of the public ingress is broken",
			Discarded: []evaluation.DiscardedSignal{},
		},
	}

	results, err := daEngine(t).Evaluate(context.Background(), &scenario, stopped, nil)
	require.NoError(t, err)
	require.Len(t, results, 2)
	for _, r := range results {
		assert.Equal(t, evaluation.AssertionFail, r.Status, "%s: %s", r.Assertion.Behavior, r.Evidence)
		assert.False(t, r.Unassessable, "%s: a stopped investigation is a FAIL, not an absence", r.Assertion.Behavior)
	}
}
