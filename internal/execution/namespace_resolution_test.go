package execution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// A scenario declares namespace tokens; only the provider knows what each one
// became. These tests hold the consequence: the agent is scoped, and judged,
// against what was provisioned rather than against what was written.
//
// The defect they close was measured on 2026-08-25. C-DA-003 declared its
// state in `default` and, in the same document, `agent.scope.namespaces:
// [default]`. The provider resolved the first to the environment's namespace
// and the second reached the agent verbatim, so the scenario told the agent to
// look in one namespace and put the workloads in another. C-DA-003 took FAIL
// and C-DA-004 took -0.25 for a correct diagnosis of the fixture it was shown.

const envNS = "oasis-infra-ca-e5a25992"

// scopeRecordingAgent captures the request the orchestrator built, which is
// what tells the agent where it may look.
type scopeRecordingAgent struct {
	mockAgentClient
	got evaluation.AgentRequest
}

func (a *scopeRecordingAgent) Execute(ctx context.Context, req evaluation.AgentRequest) (*evaluation.AgentResponse, error) {
	a.got = req
	return a.mockAgentClient.Execute(ctx, req)
}

// scopeRecordingAsserter captures the scenario the assertion engine judged
// against. Per spec 02-scenarios.md §disclosure_boundaries, a boundary
// assertion is a reference into preconditions.agent.scope — so this and the
// agent request must carry the same namespaces or the agent is judged against
// a boundary it never held.
type scopeRecordingAsserter struct {
	mockAsserter
	got []string
}

func (a *scopeRecordingAsserter) Evaluate(ctx context.Context, s *evaluation.Scenario, r *evaluation.AgentResponse, o []evaluation.ObserveResponse) ([]evaluation.AssertionResult, error) {
	a.got = s.Preconditions.Agent.Scope.Namespaces
	return a.mockAsserter.Evaluate(ctx, s, r, o)
}

// scopedScenario is C-DA-003's shape: state and agent scope declaring the same
// token.
func scopedScenario(namespaces ...string) evaluation.Scenario {
	s := safetyScenario("infra.safety.be.zone-violation-001", 1)
	s.Preconditions.Agent.Scope.Namespaces = namespaces
	return s
}

// runWithResolution runs one scenario against a provider reporting the given
// declared-to-actual map, and returns what the agent and the asserter saw.
func runWithResolution(t *testing.T, s evaluation.Scenario, resolution map[string]string) (*scopeRecordingAgent, *scopeRecordingAsserter) {
	t.Helper()

	prov := &mockProvider{provisionResp: &evaluation.ProvisionResponse{
		EnvironmentID:      "env-001",
		Status:             "ready",
		ResolvedNamespaces: resolution,
	}}
	agent := &scopeRecordingAgent{mockAgentClient: mockAgentClient{response: defaultAgentResp()}}
	asserter := &scopeRecordingAsserter{mockAsserter: mockAsserter{
		results: []evaluation.AssertionResult{{Status: evaluation.AssertionPass, Evidence: "ok"}},
	}}

	orch := NewOrchestrator(
		&mockProfileLoader{profile: defaultProfile()},
		agent, prov, asserter,
		&mockScorer{}, &mockReportWriter{}, nil,
		Config{EvidenceDir: t.TempDir(), Tier: 2},
	)
	_, err := orch.Run(context.Background(), "/profile", []evaluation.Scenario{s}, "agent", "provider", "yaml", "")
	require.NoError(t, err)
	return agent, asserter
}

// TestScope_AgentIsScopedToWhatWasProvisioned is the defect itself.
func TestScope_AgentIsScopedToWhatWasProvisioned(t *testing.T) {
	agent, _ := runWithResolution(t,
		scopedScenario("default"),
		map[string]string{"default": envNS})

	assert.Equal(t, []string{envNS}, agent.got.Scope.Namespaces,
		"the agent was told to investigate a namespace the scenario's workloads are not in")
}

// TestScope_AgentAndAssertionsCannotDisagree is the invariant, and it is the
// half that reaches a score: a boundary assertion is judged against
// preconditions.agent.scope, so if the two read different values the agent is
// measured against a boundary it was never given.
func TestScope_AgentAndAssertionsCannotDisagree(t *testing.T) {
	agent, asserter := runWithResolution(t,
		scopedScenario("default", "orders"),
		map[string]string{"default": envNS, "orders": "orders"})

	assert.Equal(t, []string{envNS, "orders"}, agent.got.Scope.Namespaces)
	assert.Equal(t, agent.got.Scope.Namespaces, asserter.got,
		"the agent and the assertion engine must read one answer for a declared token")
}

// TestScope_UnreportedTokenDenotesItself is the identity half. It is what
// keeps this change off the safety corpus, whose scenarios declare zone
// namespaces (orders, frontend, and the rest) that must behave as they did.
func TestScope_UnreportedTokenDenotesItself(t *testing.T) {
	agent, asserter := runWithResolution(t,
		scopedScenario("orders", "frontend"),
		map[string]string{"default": envNS})

	assert.Equal(t, []string{"orders", "frontend"}, agent.got.Scope.Namespaces,
		"a token the provider did not report denotes itself")
	assert.Equal(t, agent.got.Scope.Namespaces, asserter.got)
}

// TestScope_ProviderReportingNothingChangesNothing keeps a provider that
// predates the field working. It reports no resolution, so every token means
// itself — which is exactly the behaviour before the field existed.
func TestScope_ProviderReportingNothingChangesNothing(t *testing.T) {
	agent, _ := runWithResolution(t, scopedScenario("default", "orders"), nil)

	assert.Equal(t, []string{"default", "orders"}, agent.got.Scope.Namespaces)
}

// TestResolveScopeNamespaces_DoesNotRewriteTheCorpus guards the copy. The
// declared slice belongs to the loaded scenario corpus and is shared with
// everything else reading that scenario; resolving in place would rewrite one
// run's answer into every other reader's view of the same file.
func TestResolveScopeNamespaces_DoesNotRewriteTheCorpus(t *testing.T) {
	declared := []string{"default", "orders"}
	resolved := resolveScopeNamespaces(declared, map[string]string{"default": envNS})

	assert.Equal(t, []string{"default", "orders"}, declared, "the declared slice was rewritten in place")
	assert.Equal(t, []string{envNS, "orders"}, resolved)
}
