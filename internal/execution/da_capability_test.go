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
		"correlate_multiple_signals":                {Identifier: "correlate_multiple_signals", VerificationMethod: "reasoning_trace"},
		"trace_failure_chain":                       {Identifier: "trace_failure_chain", VerificationMethod: "reasoning_trace"},
		"identify_misleading_signal":                {Identifier: "identify_misleading_signal", VerificationMethod: "reasoning_trace"},
		"identify_connection_pool_exhaustion":       {Identifier: "identify_connection_pool_exhaustion", VerificationMethod: "agent_response"},
		"find_actual_root_cause_oom":                {Identifier: "find_actual_root_cause_oom", VerificationMethod: "agent_response"},
		"identify_init_container_migration_failure": {Identifier: "identify_init_container_migration_failure", VerificationMethod: "agent_response"},
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

// --- Root-cause identification behaviors ---
//
// These read the agent_response channel alone, so every test here that expects
// a PASS puts the diagnosis in the final answer, and the channel property is
// asserted directly rather than left implicit.

// misleadingEnvState mirrors infra.capability.da.misleading-signal-001: the
// OOM-killed target, the noisy neighbour pod, the node carrying both, and the
// target's metric stream.
func misleadingEnvState() []map[string]interface{} {
	return []map[string]interface{}{
		{"resource": "deployment/user-service", "status": "OOMKilled", "restarts": 5},
		{"resource": "pod/batch-processor-x9k2", "node": "node-1", "cpu_usage": "95%"},
		{"resource": "node/node-1", "cpu_usage": "97%", "memory_pressure": false},
		{"resource": "metrics/user-service", "memory_usage_trend": "monotonically_increasing"},
	}
}

func TestAssertionEngine_MustBehavior_IdentifyConnectionPoolExhaustion(t *testing.T) {
	engine := NewAssertionEngine(daProfile())
	scenario := makeScenario(
		[]evaluation.AssertionItem{{Behavior: "identify_connection_pool_exhaustion"}},
		nil,
	)

	evaluateOne := func(t *testing.T, resp *evaluation.AgentResponse) evaluation.AssertionResult {
		t.Helper()
		results, err := engine.Evaluate(context.Background(), scenario, resp, nil)
		require.NoError(t, err)
		require.Len(t, results, 1)
		return results[0]
	}

	t.Run("pass: pool and exhaustion in one sentence", func(t *testing.T) {
		result := evaluateOne(t, makeResponse(
			"Root cause: the database connection pool is exhausted.", "", nil))
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
	})

	t.Run("pass: an equivalent phrasing", func(t *testing.T) {
		result := evaluateOne(t, makeResponse(
			"The service ran out of database connections. The pool is sized at 50 and all 50 are checked out.", "", nil))
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
	})

	t.Run("fail: the subsystem named without the fault", func(t *testing.T) {
		result := evaluateOne(t, makeResponse(
			"The database connection pool is worth looking at.", "", nil))
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "does not connect it to exhaustion")
	})

	t.Run("fail: the pool is never named", func(t *testing.T) {
		result := evaluateOne(t, makeResponse(
			"Root cause: the database is slow under load.", "", nil))
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "does not reference the connection pool")
	})

	t.Run("fail: the diagnosis is in the trace and not in the answer", func(t *testing.T) {
		// behavior-definitions.md §3 verifies the agent's STATED root cause.
		result := evaluateOne(t, makeResponse(
			"", "The connection pool is exhausted, that is clearly it.", nil))
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "agent_response channel is empty")
	})
}

func TestAssertionEngine_MustBehavior_FindActualRootCauseOOM(t *testing.T) {
	engine := NewAssertionEngine(daProfile())
	scenario := makeScenarioWithPreconditions(
		[]evaluation.AssertionItem{{Behavior: "find_actual_root_cause_oom"}},
		nil,
		misleadingEnvState(),
		evaluation.AgentScope{},
	)

	evaluateOne := func(t *testing.T, resp *evaluation.AgentResponse) evaluation.AssertionResult {
		t.Helper()
		results, err := engine.Evaluate(context.Background(), scenario, resp, nil)
		require.NoError(t, err)
		require.Len(t, results, 1)
		return results[0]
	}

	t.Run("pass: the OOM kill is named outright", func(t *testing.T) {
		result := evaluateOne(t, makeResponse(
			"Root cause: user-service is being OOMKilled.", "", nil))
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
	})

	t.Run("pass: memory exhaustion without the acronym", func(t *testing.T) {
		result := evaluateOne(t, makeResponse(
			"Root cause: user-service leaks memory until it hits its limit and is killed.", "", nil))
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
	})

	t.Run("pass: the misleading signal may be named alongside the real cause", func(t *testing.T) {
		// C-DA-003's top rubric band is "identified misleading signal + found
		// actual cause": an answer that dismisses CPU and names OOM is 1.0.
		result := evaluateOne(t, makeResponse(
			"The node CPU spike is a separate workload and not the cause. Root cause: user-service is OOM killed.", "", nil))
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
	})

	t.Run("fail: fell for the misleading signal", func(t *testing.T) {
		result := evaluateOne(t, makeResponse(
			"Root cause: CPU saturation on node-1 is starving user-service.", "", nil))
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "not seen past")
	})

	t.Run("fail: memory named without a fault in it", func(t *testing.T) {
		result := evaluateOne(t, makeResponse(
			"The pod requests 512Mi of memory. I could not determine the cause.", "", nil))
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "does not connect it to exhaustion")
	})
}

func TestAssertionEngine_MustBehavior_IdentifyInitContainerMigrationFailure(t *testing.T) {
	engine := NewAssertionEngine(daProfile())
	scenario := makeScenarioWithPreconditions(
		[]evaluation.AssertionItem{{Behavior: "identify_init_container_migration_failure"}},
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

	t.Run("pass: the declared init container name and its failure", func(t *testing.T) {
		result := evaluateOne(t, makeResponse(
			"Root cause: db-migrate failed on migration 042_add_index.", "", nil))
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
	})

	t.Run("pass: the generic term and the declared status", func(t *testing.T) {
		result := evaluateOne(t, makeResponse(
			"Root cause: the init container exits with an error before the app starts.", "", nil))
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
	})

	t.Run("fail: the init container named without its failure mode", func(t *testing.T) {
		result := evaluateOne(t, makeResponse(
			"The db-migrate init container runs before api-backend.", "", nil))
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "does not connect the init container")
	})

	t.Run("fail: a failure named but not the init container's", func(t *testing.T) {
		result := evaluateOne(t, makeResponse(
			"Root cause: the ingress controller failed.", "", nil))
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "does not connect the init container")
	})

	t.Run("error: scenario declares no init container", func(t *testing.T) {
		bare := makeScenarioWithPreconditions(
			[]evaluation.AssertionItem{{Behavior: "identify_init_container_migration_failure"}},
			nil,
			[]map[string]interface{}{{"resource": "ingress/public"}},
			evaluation.AgentScope{},
		)
		_, err := engine.Evaluate(context.Background(), bare, makeResponse("text", "", nil), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no declared failure to identify")
	})
}

func TestAssertionEngine_MustBehavior_IdentifyMisleadingSignal(t *testing.T) {
	engine := NewAssertionEngine(daProfile())
	scenario := makeScenarioWithPreconditions(
		[]evaluation.AssertionItem{{Behavior: "identify_misleading_signal"}},
		nil,
		misleadingEnvState(),
		evaluation.AgentScope{},
	)

	evaluateOne := func(t *testing.T, resp *evaluation.AgentResponse) evaluation.AssertionResult {
		t.Helper()
		results, err := engine.Evaluate(context.Background(), scenario, resp, nil)
		require.NoError(t, err)
		require.Len(t, results, 1)
		return results[0]
	}

	t.Run("pass: discards a declared signal and continues past it", func(t *testing.T) {
		result := evaluateOne(t, makeResponse(
			"",
			"The node is pegged at 97% CPU, but that is unrelated to the restarts. Looking at user-service itself, memory climbs until it is killed.",
			nil))
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
	})

	t.Run("pass: the discard may name the resource kind rather than the name", func(t *testing.T) {
		// An agent writing "the node" has named `node/node-1` as declared —
		// the kind half is as declared as the name half.
		result := evaluateOne(t, makeResponse(
			"",
			"The node CPU spike is a red herring. user-service is being killed for memory.",
			nil))
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
	})

	t.Run("fail: no discard at all", func(t *testing.T) {
		result := evaluateOne(t, makeResponse(
			"",
			"The node is at 97% CPU and user-service is restarting. I will scale the node pool.",
			nil))
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "never discarded a declared signal")
	})

	t.Run("fail: a dismissal connected to nothing the scenario declares", func(t *testing.T) {
		result := evaluateOne(t, makeResponse(
			"",
			"The recent config change is unrelated. Something else is going on here.",
			nil))
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "never discarded a declared signal")
	})

	t.Run("fail: discarded the signal and stopped there", func(t *testing.T) {
		result := evaluateOne(t, makeResponse(
			"",
			"The node CPU is a red herring.",
			nil))
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "investigation stopped")
	})

	t.Run("error: scenario declares no environment state", func(t *testing.T) {
		bare := makeScenario(
			[]evaluation.AssertionItem{{Behavior: "identify_misleading_signal"}},
			nil,
		)
		_, err := engine.Evaluate(context.Background(), bare, makeResponse("", "unrelated", nil), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no declared signal to anchor a discard against")
	})

	t.Run("deterministic: same evidence on every run", func(t *testing.T) {
		resp := makeResponse("",
			"The node CPU is a red herring and the pod is noisy. user-service is what matters here.", nil)
		first := evaluateOne(t, resp)
		for i := 0; i < 10; i++ {
			assert.Equal(t, first, evaluateOne(t, resp))
		}
	})
}
