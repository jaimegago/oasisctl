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
// What they prove, and the boundary of the proof: the ordered behaviors
// (`correlate_multiple_signals`, `trace_failure_chain`) no longer raise
// "evaluator does not implement heuristic", and a score is produced for them.
// Each scenario also declares a SECOND must behavior
// (`identify_connection_pool_exhaustion`, respectively
// `identify_init_container_migration_failure`) that remains unimplemented, so
// evaluating the full declaration still errors — now on that second behavior.
// The *_RemainingGap tests pin that boundary deliberately: when those
// heuristics land, they will fail and be replaced by full-declaration scoring
// assertions.

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

	resp := &evaluation.AgentResponse{
		Reasoning:   "The public ingress returns 503 because the api-backend service has no ready endpoints. The api-backend deployment crashloops because its db-migrate init container fails on migration 042_add_index.",
		FinalAnswer: "Root cause: the db-migrate init container of api-backend fails its migration, so the backend never becomes ready and the public ingress serves 503.",
	}

	results, err := engine.Evaluate(context.Background(), &implemented, resp, nil)
	require.NoError(t, err, "the ordered heuristic must not raise the does-not-implement error")
	require.Len(t, results, 1)
	assert.Equal(t, evaluation.AssertionPass, results[0].Status, "evidence: %s", results[0].Evidence)

	result, err := execution.NewScorer().ScoreCapability(context.Background(), &implemented, results)
	require.NoError(t, err)
	assert.Equal(t, 1.0, result.Score, "a score is produced from the scenario's declared rubric")

	// An agent that stops at the first symptom fails the assertion — it does
	// not error.
	stopped := &evaluation.AgentResponse{
		FinalAnswer: "The public API is returning 503 errors. Something upstream must be broken.",
	}
	results, err = engine.Evaluate(context.Background(), &implemented, stopped, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, evaluation.AssertionFail, results[0].Status)
}

// TestCDA002_FullDeclaration_RemainingGap and its C-DA-004 twin pin the
// finding this thread's enumeration made: implementing the ordered behavior
// moves the does-not-implement error from the scenario's first must assertion
// to its second, and the scenario as declared still cannot be scored until
// that second heuristic exists. The error naming the second behavior — not
// the first — is precisely what proves the ordered heuristic now evaluates.
func TestCDA002_FullDeclaration_RemainingGap(t *testing.T) {
	scenario := loadCapabilityScenario(t, "infra.capability.da.multi-signal-correlation-001")
	resp := &evaluation.AgentResponse{Reasoning: "The metric and the logs agree."}

	_, err := daEngine(t).Evaluate(context.Background(), &scenario, resp, nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), `"correlate_multiple_signals"`)
	assert.Contains(t, err.Error(), `"identify_connection_pool_exhaustion"`)
	assert.Contains(t, err.Error(), "does not implement heuristic")
}

func TestCDA004_FullDeclaration_RemainingGap(t *testing.T) {
	scenario := loadCapabilityScenario(t, "infra.capability.da.cascading-diagnosis-001")
	resp := &evaluation.AgentResponse{Reasoning: "The public ingress fails because api-backend is down."}

	_, err := daEngine(t).Evaluate(context.Background(), &scenario, resp, nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), `"trace_failure_chain"`)
	assert.Contains(t, err.Error(), `"identify_init_container_migration_failure"`)
	assert.Contains(t, err.Error(), "does not implement heuristic")
}
