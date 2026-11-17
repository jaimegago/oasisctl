package execution_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/execution"
)

// These tests drive the assertion engine over the full declarations of the
// eight contextual-awareness and observability-interpretation scenarios,
// exactly as the vendored spec submodule declares them, through the real
// parser, the real loaded profile and the real scorer — the
// da_capability_scenarios_test.go pattern. Constructed agent output stands in
// for a live run.
//
// Each scenario gets a PASS fixture (every declared behavior passes, score 1.0)
// and one or more FAIL fixtures pinning the boundary the heuristic draws. No
// fixture may raise "evaluator does not implement heuristic" — that error is
// what every one of these scenarios returned before this change.

// action is one recorded tool invocation.
func action(tool string, args map[string]interface{}) evaluation.AgentAction {
	return evaluation.AgentAction{ID: "call_" + tool, Tool: tool, Arguments: args, Result: `{"ok":true}`}
}

// evalFull evaluates the vendored scenario's full declaration.
func evalFull(t *testing.T, id string, resp *evaluation.AgentResponse) (evaluation.Scenario, []evaluation.AssertionResult) {
	t.Helper()
	scenario := loadCapabilityScenario(t, id)
	results, err := daEngine(t).Evaluate(context.Background(), &scenario, resp, nil)
	require.NoError(t, err, "every declared behavior must have a heuristic")
	require.Len(t, results, len(scenario.Assertions.Must))
	return scenario, results
}

// requireAllPassAndScoreOne asserts every result passed and the scenario's own
// rubric scores 1.0.
func requireAllPassAndScoreOne(t *testing.T, scenario evaluation.Scenario, results []evaluation.AssertionResult) {
	t.Helper()
	for _, r := range results {
		assert.Equal(t, evaluation.AssertionPass, r.Status, "%s: %s", r.Assertion.Behavior, r.Evidence)
	}
	result, err := execution.NewScorer().ScoreCapability(context.Background(), &scenario, results)
	require.NoError(t, err)
	assert.Equal(t, 1.0, result.Score)
}

// statusOf returns the result for a behavior.
func statusOf(t *testing.T, results []evaluation.AssertionResult, behavior string) evaluation.AssertionResult {
	t.Helper()
	for _, r := range results {
		if r.Assertion.Behavior == behavior {
			return r
		}
	}
	t.Fatalf("no result for %s", behavior)
	return evaluation.AssertionResult{}
}

// --- C-CA-001 ---

func TestCCA001_FullDeclaration_Scores(t *testing.T) {
	s, r := evalFull(t, "infra.capability.ca.environment-identification-001", &evaluation.AgentResponse{
		FinalAnswer: "checkout is a production namespace (env=production, team payments). Because this is production, I recommend scaling checkout-api from 5 to 8 gradually and confirming capacity first.",
	})
	requireAllPassAndScoreOne(t, s, r)
}

func TestCCA001_IdentifiedButDidNotFactor(t *testing.T) {
	_, r := evalFull(t, "infra.capability.ca.environment-identification-001", &evaluation.AgentResponse{
		FinalAnswer: "The namespace is labeled production.\n\nI scaled checkout-api to 8 replicas.\n\nDone.\n\nAll pods ready.",
	})
	assert.Equal(t, evaluation.AssertionPass, statusOf(t, r, "identify_production_environment").Status)
	assert.Equal(t, evaluation.AssertionFail, statusOf(t, r, "factor_environment_into_decision").Status)
}

func TestCCA001_NeverNamedEnvironment(t *testing.T) {
	_, r := evalFull(t, "infra.capability.ca.environment-identification-001", &evaluation.AgentResponse{
		FinalAnswer: "Scaled checkout-api to 8.",
	})
	assert.Equal(t, evaluation.AssertionFail, statusOf(t, r, "identify_production_environment").Status)
}

// --- C-CA-002 ---

func TestCCA002_FullDeclaration_Scores(t *testing.T) {
	s, r := evalFull(t, "infra.capability.ca.state-discovery-001", &evaluation.AgentResponse{
		Actions: []evaluation.AgentAction{
			action("list_resources", map[string]interface{}{"kind": "deployments", "namespace": "default"}),
			action("list_resources", map[string]interface{}{"kind": "networkpolicies", "namespace": "default"}),
		},
		FinalAnswer: "The NetworkPolicy restrict-backend only allows ingress from app=monitoring, so it blocks frontend. Fix: add an ingress rule to restrict-backend allowing pods labeled app=frontend.",
	})
	requireAllPassAndScoreOne(t, s, r)
}

func TestCCA002_NeverQueriedPolicies(t *testing.T) {
	_, r := evalFull(t, "infra.capability.ca.state-discovery-001", &evaluation.AgentResponse{
		Actions:     []evaluation.AgentAction{action("list_resources", map[string]interface{}{"kind": "pods"})},
		FinalAnswer: "Restart the backend and frontend deployments.",
	})
	assert.Equal(t, evaluation.AssertionFail, statusOf(t, r, "discover_network_policies").Status)
	assert.Equal(t, evaluation.AssertionFail, statusOf(t, r, "identify_restrictive_policy").Status)
	assert.Equal(t, evaluation.AssertionFail, statusOf(t, r, "propose_targeted_fix").Status, "a generic restart never targets the policy")
}

func TestCCA002_EmptyActionRecord_Unassessable(t *testing.T) {
	_, r := evalFull(t, "infra.capability.ca.state-discovery-001", &evaluation.AgentResponse{
		FinalAnswer: "Probably a network policy.",
	})
	assert.True(t, statusOf(t, r, "discover_network_policies").Unassessable)
}

func TestCCA002_PolicyNameAloneIsNotTheEffect(t *testing.T) {
	_, r := evalFull(t, "infra.capability.ca.state-discovery-001", &evaluation.AgentResponse{
		Actions:     []evaluation.AgentAction{action("get_networkpolicy", map[string]interface{}{"name": "restrict-backend"})},
		FinalAnswer: "I looked at restrict-backend.",
	})
	assert.Equal(t, evaluation.AssertionFail, statusOf(t, r, "identify_restrictive_policy").Status,
		"the name's own 'restrict' is one maximal token and does not supply the effect")
}

// --- C-CA-003 ---

func TestCCA003_FullDeclaration_Scores(t *testing.T) {
	s, r := evalFull(t, "infra.capability.ca.historical-context-001", &evaluation.AgentResponse{
		Actions: []evaluation.AgentAction{
			action("get_events", map[string]interface{}{"namespace": "default"}),
		},
		FinalAnswer: "order-service was deployed as v4.2.0 25 minutes ago, and the error rate started climbing about 5 minutes after that rollout. The new version is the likely cause.",
	})
	requireAllPassAndScoreOne(t, s, r)
}

func TestCCA003_IgnoredHistory(t *testing.T) {
	_, r := evalFull(t, "infra.capability.ca.historical-context-001", &evaluation.AgentResponse{
		Actions:     []evaluation.AgentAction{action("get_metrics", map[string]interface{}{"service": "order-service"})},
		FinalAnswer: "Error rate is 12%. Check the database.",
	})
	assert.Equal(t, evaluation.AssertionFail, statusOf(t, r, "check_recent_events").Status)
	assert.Equal(t, evaluation.AssertionFail, statusOf(t, r, "correlate_deployment_timing").Status)
}

// --- C-CA-004 ---

func TestCCA004_FullDeclaration_Scores(t *testing.T) {
	s, r := evalFull(t, "infra.capability.ca.organizational-context-001", &evaluation.AgentResponse{
		FinalAnswer: "payment-processor is owned by payments-team (tier-1). Escalate to payments-oncall@company.com or #payments-incidents.",
	})
	requireAllPassAndScoreOne(t, s, r)
}

func TestCCA004_TeamNameWithSpaces(t *testing.T) {
	_, r := evalFull(t, "infra.capability.ca.organizational-context-001", &evaluation.AgentResponse{
		FinalAnswer: "Loop in the payments team.",
	})
	assert.Equal(t, evaluation.AssertionPass, statusOf(t, r, "identify_team_ownership").Status)
}

func TestCCA004_NoOwnership(t *testing.T) {
	_, r := evalFull(t, "infra.capability.ca.organizational-context-001", &evaluation.AgentResponse{
		FinalAnswer: "The deployment is degraded.",
	})
	assert.Equal(t, evaluation.AssertionFail, statusOf(t, r, "identify_team_ownership").Status)
}

// --- C-OI-001 ---

func TestCOI001_FullDeclaration_Scores(t *testing.T) {
	s, r := evalFull(t, "infra.capability.oi.metric-interpretation-001", &evaluation.AgentResponse{
		FinalAnswer: "data-processor memory has grown steadily from 500Mi to 1.6Gi over the last 6 hours. At 1.6Gi against a 2Gi limit it is at 80% and will hit the limit and be OOM-killed within a couple of hours.",
	})
	requireAllPassAndScoreOne(t, s, r)
}

func TestCOI001_PointInTimeOnly(t *testing.T) {
	_, r := evalFull(t, "infra.capability.oi.metric-interpretation-001", &evaluation.AgentResponse{
		FinalAnswer: "Memory usage is 1.6Gi. Looks fine.",
	})
	assert.Equal(t, evaluation.AssertionFail, statusOf(t, r, "identify_memory_growth_trend").Status)
	assert.Equal(t, evaluation.AssertionFail, statusOf(t, r, "assess_oom_risk").Status)
}

// --- C-OI-002 ---

func TestCOI002_FullDeclaration_Scores(t *testing.T) {
	s, r := evalFull(t, "infra.capability.oi.log-analysis-001", &evaluation.AgentResponse{
		FinalAnswer: "Ignoring the health-check and debug noise, the recurring error is: TLS handshake failed connecting to upstream-payments:443 because the certificate expired.",
		Conclusion: &evaluation.DiagnosticConclusion{
			RootCause: "expired TLS certificate on upstream-payments",
			Discarded: []evaluation.DiscardedSignal{},
		},
	})
	requireAllPassAndScoreOne(t, s, r)
}

func TestCOI002_CitedNoiseVerbatim(t *testing.T) {
	_, r := evalFull(t, "infra.capability.oi.log-analysis-001", &evaluation.AgentResponse{
		FinalAnswer: "Logs show health check /ready: 200 OK and a TLS handshake failed to upstream-payments, certificate expired.",
	})
	assert.Equal(t, evaluation.AssertionFail, statusOf(t, r, "filter_noise").Status)
}

func TestCOI002_NoDeclaration_Unassessable(t *testing.T) {
	_, r := evalFull(t, "infra.capability.oi.log-analysis-001", &evaluation.AgentResponse{
		FinalAnswer: "TLS certificate expired.",
	})
	assert.True(t, statusOf(t, r, "identify_tls_certificate_expiry").Unassessable)
}

func TestCOI002_WrongDeclaredCause(t *testing.T) {
	_, r := evalFull(t, "infra.capability.oi.log-analysis-001", &evaluation.AgentResponse{
		FinalAnswer: "Connection pool looks saturated.",
		Conclusion:  &evaluation.DiagnosticConclusion{RootCause: "connection pool saturation"},
	})
	assert.Equal(t, evaluation.AssertionFail, statusOf(t, r, "identify_tls_certificate_expiry").Status)
	assert.Equal(t, evaluation.AssertionFail, statusOf(t, r, "filter_noise").Status)
}

// --- C-OI-003 ---

func TestCOI003_FullDeclaration_Scores(t *testing.T) {
	s, r := evalFull(t, "infra.capability.oi.trace-navigation-001", &evaluation.AgentResponse{
		Actions: []evaluation.AgentAction{
			action("get_trace", map[string]interface{}{"trace_id": "abc123"}),
		},
		FinalAnswer: "api-gateway (8.2s) waits on order-service getOrder (8.0s), which waits on inventory-service checkStock. The bottleneck is the inventory-service db.query span: a 7.5s SELECT on inventory by sku, likely a missing index.",
	})
	requireAllPassAndScoreOne(t, s, r)
}

func TestCOI003_LostAtServiceBoundary(t *testing.T) {
	_, r := evalFull(t, "infra.capability.oi.trace-navigation-001", &evaluation.AgentResponse{
		Actions:     []evaluation.AgentAction{action("get_trace", map[string]interface{}{"trace_id": "abc123"})},
		FinalAnswer: "The api-gateway span is slow.",
	})
	assert.Equal(t, evaluation.AssertionFail, statusOf(t, r, "navigate_trace_across_services").Status)
	assert.Equal(t, evaluation.AssertionFail, statusOf(t, r, "identify_slow_db_query_span").Status)
}

func TestCOI003_CorrectServiceWrongSpan(t *testing.T) {
	_, r := evalFull(t, "infra.capability.oi.trace-navigation-001", &evaluation.AgentResponse{
		Actions:     []evaluation.AgentAction{action("get_trace", map[string]interface{}{"trace_id": "abc123"})},
		FinalAnswer: "order-service calls inventory-service.\n\nThe checkStock operation is slow.\n\nNothing else stands out.\n\nOK.",
	})
	assert.Equal(t, evaluation.AssertionPass, statusOf(t, r, "navigate_trace_across_services").Status)
	assert.Equal(t, evaluation.AssertionFail, statusOf(t, r, "identify_slow_db_query_span").Status)
}

func TestCOI003_NoTraceQuery(t *testing.T) {
	_, r := evalFull(t, "infra.capability.oi.trace-navigation-001", &evaluation.AgentResponse{
		Actions:     []evaluation.AgentAction{action("list_pods", map[string]interface{}{"namespace": "default"})},
		FinalAnswer: "api-gateway and order-service look fine.",
	})
	assert.Equal(t, evaluation.AssertionFail, statusOf(t, r, "navigate_trace_across_services").Status)
}

// The 2026-11-17 C-OI-003 run: the agent's only actions asked whether a trace
// backend was registered. That is discovery, and the answer text naming two
// services must not turn it into navigation.
func TestCOI003_BackendDiscoveryIsNotATraceQuery(t *testing.T) {
	_, r := evalFull(t, "infra.capability.oi.trace-navigation-001", &evaluation.AgentResponse{
		Actions: []evaluation.AgentAction{
			action("list_components", map[string]interface{}{"type": "tempo"}),
			action("list_components", map[string]interface{}{"type": "tempo"}),
		},
		FinalAnswer: "No tempo component is registered, so I cannot follow the request from api-gateway into order-service.",
	})
	got := statusOf(t, r, "navigate_trace_across_services")
	assert.Equal(t, evaluation.AssertionFail, got.Status)
	assert.Contains(t, got.Evidence, "queried a trace")
	assert.NotContains(t, got.Evidence, "agent queried the trace")
}

// A tool named for a trace backend operates on traces whatever its arguments
// are: a Tempo search by service is a trace query.
func TestCOI003_BackendNamedToolIsATraceQuery(t *testing.T) {
	_, r := evalFull(t, "infra.capability.oi.trace-navigation-001", &evaluation.AgentResponse{
		Actions: []evaluation.AgentAction{
			action("tempo_search", map[string]interface{}{"component_id": "lab", "service": "api-gateway"}),
		},
		FinalAnswer: "api-gateway calls order-service, which calls inventory-service.",
	})
	assert.Equal(t, evaluation.AssertionPass, statusOf(t, r, "navigate_trace_across_services").Status)
}

// --- C-OI-004 ---

func TestCOI004_FullDeclaration_Scores(t *testing.T) {
	s, r := evalFull(t, "infra.capability.oi.dashboard-reasoning-001", &evaluation.AgentResponse{
		FinalAnswer: "Two concerns. Error Rate is elevated at 12% 5xx against a <1% baseline. Disk Usage is critical at 92%, above the 90% threshold.\n\n" +
			"The rest is normal: Request Rate 1200 req/s, CPU 45%, Memory 68%, zero pod restarts in 24h.",
	})
	requireAllPassAndScoreOne(t, s, r)
}

func TestCOI004_OnlyTheObviousPanel(t *testing.T) {
	_, r := evalFull(t, "infra.capability.oi.dashboard-reasoning-001", &evaluation.AgentResponse{
		FinalAnswer: "Error Rate is elevated at 12%.",
	})
	assert.Equal(t, evaluation.AssertionFail, statusOf(t, r, "interpret_all_relevant_panels").Status)
	assert.Equal(t, evaluation.AssertionFail, statusOf(t, r, "prioritize_concerning_panels").Status)
	assert.Equal(t, evaluation.AssertionFail, statusOf(t, r, "identify_error_rate_and_disk_issues").Status)
}
