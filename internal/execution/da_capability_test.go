package execution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// The C-DA detection heuristics (da_capability.go) read the agent's text under
// the profile's ratified scoring constants: §3.1 maximal-token identifier
// matching, §3.2 sentence splitting, and the §2.2 radius-one co-occurrence
// window that FactorIdentified established as this profile's reading of
// "connecting" two things. These tests exercise both verdict clauses of each
// behavior — reference and connection — and the token-boundary and determinism
// properties the constants promise.

func daProfile() *evaluation.Profile {
	return makeProfile(map[string]evaluation.BehaviorDefinition{
		"correlate_multiple_signals": {Identifier: "correlate_multiple_signals", VerificationMethod: "reasoning_trace"},
		"trace_failure_chain":        {Identifier: "trace_failure_chain", VerificationMethod: "reasoning_trace"},
	})
}

// cascadeEnvState mirrors the shape infra.capability.da.cascading-diagnosis-001
// declares: an ingress, a deployment carrying a failed init container, the
// service in front of it, and the mesh sidecar's log stream.
func cascadeEnvState() []map[string]interface{} {
	return []map[string]interface{}{
		{"resource": "ingress/public", "status": "returning_503"},
		{
			"resource": "deployment/api-backend",
			"status":   "CrashLoopBackOff",
			"init_containers": []interface{}{
				map[string]interface{}{"name": "db-migrate", "status": "error"},
			},
		},
		{"resource": "service/api-backend", "endpoints": []interface{}{}},
		{"resource": "logs/service-mesh-sidecar"},
	}
}

func TestAssertionEngine_MustBehavior_CorrelateMultipleSignals(t *testing.T) {
	engine := NewAssertionEngine(daProfile())
	scenario := makeScenario(
		[]evaluation.AssertionItem{{Behavior: "correlate_multiple_signals"}},
		nil,
	)

	evaluateOne := func(t *testing.T, resp *evaluation.AgentResponse) evaluation.AssertionResult {
		t.Helper()
		results, err := engine.Evaluate(context.Background(), scenario, resp, nil)
		require.NoError(t, err)
		require.Len(t, results, 1)
		return results[0]
	}

	t.Run("pass: two pillars connected in one sentence", func(t *testing.T) {
		resp := makeResponse(
			"Root cause: database connection pool exhaustion.",
			"The p99 latency metric is at 4500ms and the logs show the pool exhausted. Both point at the database.",
			nil,
		)
		result := evaluateOne(t, resp)
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
		assert.Contains(t, result.Evidence, "metrics")
		assert.Contains(t, result.Evidence, "logs")
	})

	t.Run("pass: two pillars in adjacent sentences", func(t *testing.T) {
		resp := makeResponse(
			"",
			"The latency metric spiked at 09:14. The trace shows the db.query span consuming almost the whole request.",
			nil,
		)
		result := evaluateOne(t, resp)
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
	})

	t.Run("fail: pillars referenced but never within the window", func(t *testing.T) {
		resp := makeResponse(
			"",
			"The metrics look degraded. The service restarted twice. The node seems fine. Nothing else stands out. The logs are noisy as usual.",
			nil,
		)
		result := evaluateOne(t, resp)
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "treated independently")
	})

	t.Run("fail: only one pillar referenced", func(t *testing.T) {
		resp := makeResponse("The logs show connection timeouts.", "", nil)
		result := evaluateOne(t, resp)
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "requires at least two")
	})

	t.Run("fail: pillar tokens inside larger words do not count", func(t *testing.T) {
		// §3.1 maximal-token: "backlog" is not "log", "spanning" is not "span",
		// "retraces" is not "traces".
		resp := makeResponse(
			"The backlog of work is spanning several days and the agent retraces its steps.",
			"",
			nil,
		)
		result := evaluateOne(t, resp)
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "referenced 0")
	})

	t.Run("fail: empty transcript", func(t *testing.T) {
		result := evaluateOne(t, makeResponse("", "", nil))
		assert.Equal(t, evaluation.AssertionFail, result.Status)
	})

	t.Run("pair may not straddle the channel boundary", func(t *testing.T) {
		// One pillar at the end of FinalAnswer, the other at the start of
		// Reasoning: referenced, but never connected within one channel.
		resp := makeResponse("I looked at the metrics", "the logs were next", nil)
		result := evaluateOne(t, resp)
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "treated independently")
	})

	t.Run("deterministic: same evidence pair on every run", func(t *testing.T) {
		resp := makeResponse(
			"",
			"The trace and the logs agree. The metric agrees too.",
			nil,
		)
		first := evaluateOne(t, resp)
		for i := 0; i < 10; i++ {
			assert.Equal(t, first, evaluateOne(t, resp))
		}
	})
}

func TestAssertionEngine_MustBehavior_TraceFailureChain(t *testing.T) {
	engine := NewAssertionEngine(daProfile())
	scenario := makeScenarioWithPreconditions(
		[]evaluation.AssertionItem{{Behavior: "trace_failure_chain"}},
		nil,
		cascadeEnvState(),
		evaluation.AgentScope{},
	)

	evaluateOne := func(t *testing.T, resp *evaluation.AgentResponse) evaluation.AssertionResult {
		t.Helper()
		results, err := engine.Evaluate(context.Background(), scenario, resp, nil)
		require.NoError(t, err)
		require.Len(t, results, 1)
		return results[0]
	}

	t.Run("pass: trace connects components hop by hop", func(t *testing.T) {
		resp := makeResponse(
			"Root cause: the db-migrate init container of api-backend fails, so the pods never become ready.",
			"The public ingress returns 503 because api-backend has no ready endpoints. The api-backend pods crashloop because db-migrate fails on migration 042.",
			nil,
		)
		result := evaluateOne(t, resp)
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
	})

	t.Run("fail: stopped at the first symptom", func(t *testing.T) {
		resp := makeResponse(
			"The public API is returning 503 errors and I could not determine why.",
			"",
			nil,
		)
		result := evaluateOne(t, resp)
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "stopped")
	})

	t.Run("fail: components referenced but never connected", func(t *testing.T) {
		resp := makeResponse(
			"",
			"The public ingress is down. Several unrelated things were checked. The cluster looks healthy otherwise. Restarting rarely helps in these cases. Separately, db-migrate exists.",
			nil,
		)
		result := evaluateOne(t, resp)
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "no traversal shown")
	})

	t.Run("error: scenario declares fewer than two components", func(t *testing.T) {
		bare := makeScenarioWithPreconditions(
			[]evaluation.AssertionItem{{Behavior: "trace_failure_chain"}},
			nil,
			[]map[string]interface{}{{"resource": "ingress/public"}},
			evaluation.AgentScope{},
		)
		_, err := engine.Evaluate(context.Background(), bare, makeResponse("text", "", nil), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "trace_failure_chain needs a chain of at least two")
	})
}

func TestChainComponents(t *testing.T) {
	t.Run("resource names, container names, case-insensitive dedupe", func(t *testing.T) {
		scenario := makeScenarioWithPreconditions(nil, nil, cascadeEnvState(), evaluation.AgentScope{})
		components := chainComponents(scenario)

		var labels []string
		for _, c := range components {
			labels = append(labels, c.label)
		}
		// deployment/api-backend and service/api-backend collapse to one
		// component; db-migrate joins from init_containers.
		assert.Equal(t, []string{"public", "api-backend", "db-migrate", "service-mesh-sidecar"}, labels)
	})

	t.Run("entries without a name segment contribute nothing", func(t *testing.T) {
		scenario := makeScenarioWithPreconditions(nil, nil, []map[string]interface{}{
			{"resource": "nodes"},
			{"status": "running"},
		}, evaluation.AgentScope{})
		assert.Empty(t, chainComponents(scenario))
	})
}
