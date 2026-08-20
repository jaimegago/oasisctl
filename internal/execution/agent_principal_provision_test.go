package execution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// The principal is a property of the deployment, not of the scenario, so it
// arrives as run configuration and is merged into the provision request. These
// tests hold the two halves of that: it reaches the provider when supplied, and
// the scenario's own agent precondition is not disturbed.

func runOneScenario(t *testing.T, cfg Config) *mockProvider {
	t.Helper()
	cfg.EvidenceDir = t.TempDir()
	if cfg.Tier == 0 {
		cfg.Tier = 2
	}
	prov := &mockProvider{provisionResp: defaultProvision()}
	orch := NewOrchestrator(
		&mockProfileLoader{profile: defaultProfile()},
		&mockAgentClient{response: defaultAgentResp()},
		prov,
		&mockAsserter{results: []evaluation.AssertionResult{{Status: evaluation.AssertionPass, Evidence: "ok"}}},
		&mockScorer{}, &mockReportWriter{}, nil, cfg,
	)
	_, err := orch.Run(context.Background(), "/profile", []evaluation.Scenario{safetyScenario("s.001", 1)}, "agent", "provider", "yaml", "")
	require.NoError(t, err)
	require.Len(t, prov.provisionRequests, 1)
	return prov
}

func TestProvision_CarriesTheDeclaredAgentPrincipal(t *testing.T) {
	const principal = "system:serviceaccount:default:joe-oasis-e2e"

	prov := runOneScenario(t, Config{AgentPrincipal: principal})

	assert.Equal(t, principal, prov.provisionRequests[0].Agent.Principal,
		"the provider cannot attribute audit evidence to an agent it was never told about")
}

func TestProvision_OmitsPrincipalWhenNoneDeclared(t *testing.T) {
	prov := runOneScenario(t, Config{})

	// No invention. An undeclared principal travels as empty, and the evaluator
	// marks audit-backed assertions vacuous rather than matching every
	// principal on the cluster.
	assert.Empty(t, prov.provisionRequests[0].Agent.Principal)
}

func TestAgentPrecondition_PrincipalIsNotScenarioDeclarable(t *testing.T) {
	// A scenario corpus must not be able to name an identity: it would only be
	// correct for the one harness it was written against. The yaml:"-" tag is
	// what enforces that, so it is asserted rather than assumed.
	var ap evaluation.AgentPrecondition
	require.NoError(t, yaml.Unmarshal([]byte("mode: autonomous\nprincipal: system:serviceaccount:default:injected\n"), &ap))
	assert.Empty(t, ap.Principal, "a scenario must not be able to declare the agent's principal")
	assert.Equal(t, "autonomous", ap.Mode, "the rest of the precondition still parses")
}
