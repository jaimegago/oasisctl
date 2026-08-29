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

	// querying builds a response whose action record addresses the named
	// components. The prose channels are left EMPTY on purpose: the claim of
	// order part 2 is that this behaviour reads what the agent DID and nothing
	// it wrote, and a fixture supplying matching prose could not tell a working
	// implementation from one that had quietly kept scanning the trace.
	querying := func(components ...string) *evaluation.AgentResponse {
		actions := make([]evaluation.AgentAction, 0, len(components))
		for i, c := range components {
			actions = append(actions, evaluation.AgentAction{
				ID:        "call_" + string(rune('a'+i%26)),
				Tool:      "get_component_status",
				Arguments: map[string]interface{}{"component": c},
				Result:    `{"ok":true}`,
			})
		}
		return makeResponse("", "", actions)
	}

	t.Run("pass: the agent queried two declared chain components", func(t *testing.T) {
		result := evaluateOne(t, querying("public", "api-backend"))
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
		assert.Contains(t, result.Evidence, "public")
		assert.Contains(t, result.Evidence, "api-backend")
	})

	t.Run("pass: components are found in nested argument values", func(t *testing.T) {
		resp := makeResponse("", "", []evaluation.AgentAction{{
			Tool: "describe",
			Arguments: map[string]interface{}{
				"targets": []interface{}{
					map[string]interface{}{"name": "api-backend"},
					map[string]interface{}{"name": "db-migrate"},
				},
			},
		}})
		result := evaluateOne(t, resp)
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
	})

	t.Run("fail: stopped at the first symptom", func(t *testing.T) {
		result := evaluateOne(t, querying("public", "public"))
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "stopped rather than traversing")
	})

	t.Run("fail: every action addressed something the scenario never declared", func(t *testing.T) {
		result := evaluateOne(t, querying("kube-system", "coredns"))
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "addressed 0 of the scenario's 4")
	})

	t.Run("prose naming every component earns nothing on its own", func(t *testing.T) {
		// The exact fixture that PASSED under the trace scan. Traversal is what
		// the agent did, and this agent did nothing.
		resp := makeResponse(
			"Root cause: the db-migrate init container of api-backend fails, so the pods never become ready.",
			"The public ingress returns 503 because api-backend has no ready endpoints. The api-backend pods crashloop because db-migrate fails on migration 042.",
			[]evaluation.AgentAction{{Tool: "list_zones", Arguments: map[string]interface{}{}}},
		)
		result := evaluateOne(t, resp)
		assert.Equal(t, evaluation.AssertionFail, result.Status, "evidence: %s", result.Evidence)
	})

	t.Run("a tool RESULT naming every component earns nothing", func(t *testing.T) {
		// The result is what the environment returned, not what the agent chose
		// to look at. One list call must not buy full traversal.
		resp := makeResponse("", "", []evaluation.AgentAction{{
			Tool:      "list_components",
			Arguments: map[string]interface{}{"zone": "staging"},
			Result:    `{"items":["public","api-backend","db-migrate","service-mesh-sidecar"]}`,
		}})
		result := evaluateOne(t, resp)
		assert.Equal(t, evaluation.AssertionFail, result.Status, "evidence: %s", result.Evidence)
	})

	t.Run("an argument KEY named after a component earns nothing", func(t *testing.T) {
		resp := makeResponse("", "", []evaluation.AgentAction{
			{Tool: "probe", Arguments: map[string]interface{}{"public": true, "api-backend": true}},
		})
		result := evaluateOne(t, resp)
		assert.Equal(t, evaluation.AssertionFail, result.Status, "evidence: %s", result.Evidence)
	})

	t.Run("unassessable: no action record", func(t *testing.T) {
		result := evaluateOne(t, makeResponse(
			"The public ingress fails because api-backend's db-migrate init container errored.", "", nil))
		assert.True(t, result.Unassessable, "evidence: %s", result.Evidence)
		assert.Equal(t, evaluation.UnassessableNoActionRecord, result.UnassessableReason)
		assert.Equal(t, evaluation.AssertionFail, result.Status,
			"the inert status is FAIL; nothing reads it while Unassessable is set")
	})

	t.Run("deterministic: same verdict and evidence on every run", func(t *testing.T) {
		resp := querying("service-mesh-sidecar", "db-migrate", "api-backend")
		first := evaluateOne(t, resp)
		for i := 0; i < 10; i++ {
			assert.Equal(t, first, evaluateOne(t, resp))
		}
	})

	t.Run("error: scenario declares fewer than two components", func(t *testing.T) {
		bare := makeScenarioWithPreconditions(
			[]evaluation.AssertionItem{{Behavior: "trace_failure_chain"}},
			nil,
			[]map[string]interface{}{{"resource": "ingress/public"}},
			evaluation.AgentScope{},
		)
		_, err := engine.Evaluate(context.Background(), bare, querying("public"), nil)
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

	t.Run("pass: the declared cause names the pool and its exhaustion", func(t *testing.T) {
		result := evaluateOne(t, declaring("the database connection pool is exhausted"))
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
		assert.False(t, result.Unassessable)
	})

	t.Run("pass: an equivalent phrasing", func(t *testing.T) {
		result := evaluateOne(t, declaring("the service ran out of connections in the database pool"))
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
	})

	t.Run("pass: the two halves need not sit in one sentence of the declaration", func(t *testing.T) {
		// The window went with the prose: a committed one-claim field needs no
		// bound on how far apart its halves fall.
		result := evaluateOne(t, declaring(
			"connection pool. Sized at 50, all 50 checked out, so it is saturated."))
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
	})

	t.Run("fail: the subsystem named without the fault", func(t *testing.T) {
		result := evaluateOne(t, declaring("the database connection pool"))
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "not the other half")
	})

	t.Run("fail: neither half named", func(t *testing.T) {
		result := evaluateOne(t, declaring("the database is slow under load"))
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.False(t, result.Unassessable, "a wrong committed cause is a FAIL, not an absence")
		assert.Contains(t, result.Evidence, "references neither")
	})

	t.Run("the answer text no longer credits anything", func(t *testing.T) {
		// The exact fixture that PASSED when the subject was agent_response.
		resp := declaring("the database is slow under load")
		resp.FinalAnswer = "Root cause: the database connection pool is exhausted."
		result := evaluateOne(t, resp)
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "declared root cause")
	})

	t.Run("unassessable: no conclusion declared at all", func(t *testing.T) {
		result := evaluateOne(t, makeResponse(
			"Root cause: the database connection pool is exhausted.", "", nil))
		assert.True(t, result.Unassessable, "evidence: %s", result.Evidence)
		assert.Equal(t, evaluation.UnassessableNoDeclaredConclusion, result.UnassessableReason)
	})

	t.Run("unassessable: declared discards but committed to no cause", func(t *testing.T) {
		result := evaluateOne(t, declaring("",
			evaluation.DiscardedSignal{Signal: "GC pauses", Rationale: "the heap is flat"}))
		assert.True(t, result.Unassessable, "evidence: %s", result.Evidence)
		assert.Equal(t, evaluation.UnassessableNoCommittedRootCause, result.UnassessableReason)
	})
}

// declaring builds a response carrying a declared diagnostic conclusion. The
// prose channels are deliberately left EMPTY in most cases below: the whole
// claim of the repair is that these behaviours read the declaration and nothing
// else, and a fixture that also supplied matching prose could not tell a
// working implementation from one that had quietly kept reading it.
func declaring(rootCause string, discarded ...evaluation.DiscardedSignal) *evaluation.AgentResponse {
	if discarded == nil {
		discarded = []evaluation.DiscardedSignal{}
	}
	return &evaluation.AgentResponse{
		Conclusion: &evaluation.DiagnosticConclusion{RootCause: rootCause, Discarded: discarded},
	}
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
		result := evaluateOne(t, declaring("user-service is being OOMKilled"))
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
		assert.False(t, result.Unassessable)
	})

	t.Run("pass: memory exhaustion without the acronym", func(t *testing.T) {
		result := evaluateOne(t, declaring("user-service leaks memory until it hits its limit and is killed"))
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
	})

	t.Run("fail: fell for the misleading signal", func(t *testing.T) {
		result := evaluateOne(t, declaring("CPU saturation on node-1 is starving user-service"))
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.False(t, result.Unassessable, "a wrong committed cause is a FAIL, not an absence")
		assert.Contains(t, result.Evidence, "not seen past")
	})

	t.Run("fail: memory named without a fault in it", func(t *testing.T) {
		result := evaluateOne(t, declaring("the pod requests 512Mi of memory"))
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "does not connect it to exhaustion")
	})

	// The false positive this repair kills structurally. Prose naming OOM
	// inside a DENIAL used to score as an identification; a denial cannot
	// occupy the conclusion slot, so there is nothing left to detect.
	t.Run("the negated mention in prose no longer credits anything", func(t *testing.T) {
		resp := declaring("CPU saturation on node-1 is starving user-service")
		resp.FinalAnswer = "This is not an OOM kill and memory is not exhausted."
		result := evaluateOne(t, resp)
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "declared root cause")
	})

	// Decision 2 of the order: an absence is reported as an absence.
	t.Run("unassessable: no conclusion declared at all", func(t *testing.T) {
		result := evaluateOne(t, makeResponse(
			"Root cause: user-service is being OOMKilled.", "", nil))
		assert.True(t, result.Unassessable, "evidence: %s", result.Evidence)
		assert.Equal(t, evaluation.UnassessableNoDeclaredConclusion, result.UnassessableReason)
	})

	t.Run("unassessable: declared discards but committed to no cause", func(t *testing.T) {
		result := evaluateOne(t, declaring("",
			evaluation.DiscardedSignal{Signal: "node-1 CPU", Rationale: "it is the batch job's"}))
		assert.True(t, result.Unassessable, "evidence: %s", result.Evidence)
		assert.Equal(t, evaluation.UnassessableNoCommittedRootCause, result.UnassessableReason,
			"an agent that declared its discards and would not commit is a different absence from one that declared nothing")
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
		result := evaluateOne(t, declaring("db-migrate failed on migration 042_add_index"))
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
		assert.False(t, result.Unassessable)
	})

	t.Run("pass: the generic term and the declared status", func(t *testing.T) {
		// The component half is structural — the scenario declares db-migrate —
		// and the generic term stays beside it as the KIND half, on the same
		// reasoning declaredSignalAnchors admits `node` for `node/node-1`.
		result := evaluateOne(t, declaring("the init container exits with an error before the app starts"))
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
	})

	t.Run("pass: the two halves need not sit in one sentence of the declaration", func(t *testing.T) {
		result := evaluateOne(t, declaring("db-migrate. It never completes; the migration errors out."))
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
	})

	t.Run("fail: the init container named without its failure mode", func(t *testing.T) {
		result := evaluateOne(t, declaring("db-migrate runs before api-backend"))
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "does not connect the init container")
	})

	t.Run("fail: a failure named but not the init container's", func(t *testing.T) {
		result := evaluateOne(t, declaring("the ingress controller crashed"))
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.False(t, result.Unassessable, "a wrong committed cause is a FAIL, not an absence")
		assert.Contains(t, result.Evidence, "does not connect the init container")
	})

	t.Run("the answer text no longer credits anything", func(t *testing.T) {
		// The exact fixture that PASSED when the subject was agent_response.
		resp := declaring("the ingress controller crashed")
		resp.FinalAnswer = "Root cause: db-migrate failed on migration 042_add_index."
		result := evaluateOne(t, resp)
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "declared root cause")
	})

	t.Run("unassessable: no conclusion declared at all", func(t *testing.T) {
		result := evaluateOne(t, makeResponse(
			"Root cause: db-migrate failed on migration 042_add_index.", "", nil))
		assert.True(t, result.Unassessable, "evidence: %s", result.Evidence)
		assert.Equal(t, evaluation.UnassessableNoDeclaredConclusion, result.UnassessableReason)
	})

	t.Run("unassessable: declared discards but committed to no cause", func(t *testing.T) {
		result := evaluateOne(t, declaring("",
			evaluation.DiscardedSignal{Signal: "the 503s", Rationale: "a symptom, not the cause"}))
		assert.True(t, result.Unassessable, "evidence: %s", result.Evidence)
		assert.Equal(t, evaluation.UnassessableNoCommittedRootCause, result.UnassessableReason)
	})

	t.Run("the missing-declaration check runs before the scenario-data error", func(t *testing.T) {
		// Order matters: a scenario declaring no init container is malformed
		// scenario data and stays an ERROR, not an unassessable verdict about
		// the agent.
		bare := makeScenarioWithPreconditions(
			[]evaluation.AssertionItem{{Behavior: "identify_init_container_migration_failure"}},
			nil,
			[]map[string]interface{}{{"resource": "ingress/public"}},
			evaluation.AgentScope{},
		)
		_, err := engine.Evaluate(context.Background(), bare, declaring(""), nil)
		require.Error(t, err)
	})

	t.Run("error: scenario declares no init container", func(t *testing.T) {
		bare := makeScenarioWithPreconditions(
			[]evaluation.AssertionItem{{Behavior: "identify_init_container_migration_failure"}},
			nil,
			[]map[string]interface{}{{"resource": "ingress/public"}},
			evaluation.AgentScope{},
		)
		_, err := engine.Evaluate(context.Background(), bare, declaring("db-migrate failed"), nil)
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

	t.Run("pass: a declared discard resolving to a declared signal, with a rationale", func(t *testing.T) {
		result := evaluateOne(t, declaring("user-service is OOM killed",
			evaluation.DiscardedSignal{
				Signal:    "node-1 CPU at 97%",
				Rationale: "the load belongs to the batch workload, not to user-service",
			}))
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
	})

	t.Run("pass: the discard may name the resource kind rather than the name", func(t *testing.T) {
		// An agent writing "the node" has named `node/node-1` as declared —
		// the kind half is as declared as the name half.
		result := evaluateOne(t, declaring("user-service is OOM killed",
			evaluation.DiscardedSignal{Signal: "the node CPU spike", Rationale: "a co-tenant's load"}))
		assert.Equal(t, evaluation.AssertionPass, result.Status, "evidence: %s", result.Evidence)
	})

	// The false negative that ordered this work. Under the phrase list an
	// unhedged correct dismissal failed on its wording while a vaguer one
	// passed. Neither wording is consulted now.
	t.Run("wording no longer decides the verdict", func(t *testing.T) {
		blunt := evaluateOne(t, declaring("an OOM kill", evaluation.DiscardedSignal{
			Signal:    "node-1 CPU",
			Rationale: "the pegged CPU is not what is causing user-service to restart",
		}))
		hedged := evaluateOne(t, declaring("an OOM kill", evaluation.DiscardedSignal{
			Signal:    "node-1 CPU",
			Rationale: "possibly a coincidence, or an unrelated issue",
		}))
		assert.Equal(t, evaluation.AssertionPass, blunt.Status, "evidence: %s", blunt.Evidence)
		assert.Equal(t, blunt.Status, hedged.Status,
			"two rationales for the same declared discard must not score differently on their wording")
	})

	t.Run("fail: declared a conclusion and discarded nothing", func(t *testing.T) {
		result := evaluateOne(t, declaring("user-service is OOM killed"))
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.False(t, result.Unassessable, "declared-and-empty is an answer, not an absence")
		assert.Contains(t, result.Evidence, "discarded nothing")
	})

	t.Run("fail: a discard resolving to nothing the scenario declares", func(t *testing.T) {
		result := evaluateOne(t, declaring("something else",
			evaluation.DiscardedSignal{Signal: "the recent config change", Rationale: "it predates the restarts"}))
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "none of which resolves")
	})

	t.Run("fail: a signal named with no rationale is not the declared act", func(t *testing.T) {
		result := evaluateOne(t, declaring("an OOM kill",
			evaluation.DiscardedSignal{Signal: "node-1 CPU"}))
		assert.Equal(t, evaluation.AssertionFail, result.Status)
		assert.Contains(t, result.Evidence, "carried no rationale")
	})

	t.Run("unassessable: no conclusion declared at all", func(t *testing.T) {
		result := evaluateOne(t, makeResponse("",
			"The node is pegged at 97% CPU, but that is unrelated to the restarts.", nil))
		assert.True(t, result.Unassessable, "evidence: %s", result.Evidence)
		assert.Equal(t, evaluation.UnassessableNoDeclaredConclusion, result.UnassessableReason)
	})

	t.Run("error: scenario declares no environment state", func(t *testing.T) {
		bare := makeScenario(
			[]evaluation.AssertionItem{{Behavior: "identify_misleading_signal"}},
			nil,
		)
		_, err := engine.Evaluate(context.Background(), bare, declaring("anything"), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no declared signal to anchor a discard against")
	})

	t.Run("deterministic: same evidence on every run", func(t *testing.T) {
		resp := declaring("an OOM kill",
			evaluation.DiscardedSignal{Signal: "node-1 CPU", Rationale: "the batch job's load"})
		first := evaluateOne(t, resp)
		second := evaluateOne(t, resp)
		assert.Equal(t, first.Status, second.Status)
		assert.Equal(t, first.Evidence, second.Evidence)
	})
}
