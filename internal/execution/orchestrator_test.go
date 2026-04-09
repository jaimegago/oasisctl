package execution

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// --- Hand-coded mocks ---

type mockProfileLoader struct {
	profile *evaluation.Profile
	err     error
}

func (m *mockProfileLoader) Load(_ context.Context, _ string) (*evaluation.Profile, error) {
	return m.profile, m.err
}

type mockAgentClient struct {
	response *evaluation.AgentResponse
	err      error
	identity evaluation.AgentIdentity
	config   evaluation.AgentConfiguration
}

func (m *mockAgentClient) Execute(_ context.Context, _ evaluation.AgentRequest) (*evaluation.AgentResponse, error) {
	return m.response, m.err
}

func (m *mockAgentClient) ReportIdentityAndConfiguration(_ context.Context) (evaluation.AgentIdentity, evaluation.AgentConfiguration, error) {
	id := m.identity
	if id.Name == "" {
		id.Name = "test-agent"
		id.Version = "1.0.0"
	}
	cfg := m.config
	if cfg == nil {
		cfg = evaluation.AgentConfiguration{}
	}
	return id, cfg, nil
}

type mockProvider struct {
	conformanceResp *evaluation.ConformanceResponse
	conformanceErr  error
	provisionResp   *evaluation.ProvisionResponse
	provisionErr    error
	snapshotErr     error
	teardownErr     error
	injectErr       error
	observeResp     *evaluation.ObserveResponse
	observeErr      error
	observeByType   map[string]*evaluation.ObserveResponse
	observeRequests []evaluation.ObserveRequest
}

func (m *mockProvider) Conformance(_ context.Context, _ string) (*evaluation.ConformanceResponse, error) {
	if m.conformanceErr != nil {
		return nil, m.conformanceErr
	}
	if m.conformanceResp != nil {
		return m.conformanceResp, nil
	}
	return &evaluation.ConformanceResponse{
		Provider:        "mock-provider",
		ProviderVersion: "1.0.0",
		Supported:       true,
		Requirements: evaluation.ConformanceRequirements{
			EnvironmentType:          "kubernetes-cluster",
			ComplexityTierSupported:  3,
			OASISCoreSpecVersion:     []string{"0.4.0"},
			EvidenceSourcesAvailable: []string{"audit_log", "resource_state", "response_content"},
			StateInjection:           true,
			AuditPolicyInstallation:  true,
			NetworkPolicyEnforcement: true,
		},
	}, nil
}

func (m *mockProvider) Provision(_ context.Context, _ evaluation.ProvisionRequest) (*evaluation.ProvisionResponse, error) {
	return m.provisionResp, m.provisionErr
}

func (m *mockProvider) StateSnapshot(_ context.Context, _ evaluation.StateSnapshotRequest) (*evaluation.StateSnapshotResponse, error) {
	if m.snapshotErr != nil {
		return nil, m.snapshotErr
	}
	return &evaluation.StateSnapshotResponse{}, nil
}

func (m *mockProvider) Teardown(_ context.Context, _ evaluation.TeardownRequest) error {
	return m.teardownErr
}

func (m *mockProvider) InjectState(_ context.Context, _ evaluation.InjectStateRequest) error {
	return m.injectErr
}

func (m *mockProvider) Observe(_ context.Context, req evaluation.ObserveRequest) (*evaluation.ObserveResponse, error) {
	m.observeRequests = append(m.observeRequests, req)
	if m.observeByType != nil {
		if resp, ok := m.observeByType[req.ObservationType]; ok {
			return resp, nil
		}
	}
	return m.observeResp, m.observeErr
}

type mockAsserter struct {
	results []evaluation.AssertionResult
	err     error
}

func (m *mockAsserter) Evaluate(_ context.Context, _ *evaluation.Scenario, _ *evaluation.AgentResponse, _ []evaluation.ObserveResponse) ([]evaluation.AssertionResult, error) {
	return m.results, m.err
}

type mockScorer struct {
	safetyResult     *evaluation.ScenarioResult
	capabilityResult *evaluation.ScenarioResult
	err              error
}

func (m *mockScorer) ScoreSafety(_ context.Context, scenario *evaluation.Scenario, _ []evaluation.AssertionResult) (*evaluation.ScenarioResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.safetyResult != nil {
		m.safetyResult.ScenarioID = scenario.ID
		return m.safetyResult, nil
	}
	return &evaluation.ScenarioResult{ScenarioID: scenario.ID, Passed: true, Score: 1.0}, nil
}

func (m *mockScorer) ScoreCapability(_ context.Context, scenario *evaluation.Scenario, _ []evaluation.AssertionResult) (*evaluation.ScenarioResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.capabilityResult != nil {
		m.capabilityResult.ScenarioID = scenario.ID
		return m.capabilityResult, nil
	}
	return &evaluation.ScenarioResult{ScenarioID: scenario.ID, Passed: true, Score: 0.9}, nil
}

type mockReportWriter struct {
	called bool
	err    error
}

func (m *mockReportWriter) Write(_ context.Context, _ *evaluation.Verdict, _ string, _ string) error {
	m.called = true
	return m.err
}

// --- Helpers ---

func defaultProfile() *evaluation.Profile {
	return &evaluation.Profile{
		Metadata: evaluation.ProfileMetadata{
			Name:    "test-profile",
			Version: "0.1.0",
		},
		BehaviorDefinitions: map[string]evaluation.BehaviorDefinition{},
	}
}

func defaultProvision() *evaluation.ProvisionResponse {
	return &evaluation.ProvisionResponse{
		EnvironmentID: "env-001",
		Status:        "ready",
	}
}

func defaultAgentResp() *evaluation.AgentResponse {
	return &evaluation.AgentResponse{
		FinalAnswer: "Task completed",
		Reasoning:   "I analyzed the situation",
	}
}

func testSIConformanceRequirements() *evaluation.ProviderConformanceRequirements {
	return &evaluation.ProviderConformanceRequirements{
		EnvironmentType:          "kubernetes-cluster",
		MinComplexityTier:        1,
		OASISCoreSpecVersion:     "0.4.0",
		EvidenceSourcesRequired:  []string{"audit_log", "resource_state", "response_content"},
		StateInjection:           true,
		AuditPolicyInstallation:  true,
		NetworkPolicyEnforcement: true,
	}
}

func safetyScenario(id string, tier int) evaluation.Scenario {
	return evaluation.Scenario{
		ID:             id,
		Classification: evaluation.ClassificationSafety,
		Tier:           tier,
	}
}

func safetyScenarioWithCategorySub(id string, tier int, category, subcategory string) evaluation.Scenario {
	return evaluation.Scenario{
		ID:             id,
		Classification: evaluation.ClassificationSafety,
		Tier:           tier,
		Category:       category,
		Subcategory:    subcategory,
	}
}

func capabilityScenario(id string, tier int) evaluation.Scenario {
	return evaluation.Scenario{
		ID:             id,
		Classification: evaluation.ClassificationCapability,
		Tier:           tier,
		Archetype:      "deploy",
	}
}

func capabilityScenarioWithCategory(id string, tier int, category string) evaluation.Scenario {
	return evaluation.Scenario{
		ID:             id,
		Classification: evaluation.ClassificationCapability,
		Tier:           tier,
		Category:       category,
		Archetype:      "deploy",
	}
}

// --- Tests ---

func TestOrchestrator_DryRun(t *testing.T) {
	loader := &mockProfileLoader{profile: defaultProfile()}
	reporter := &mockReportWriter{}

	orch := NewOrchestrator(
		loader, nil, nil, nil, nil, reporter, nil,
		Config{Tier: 1, DryRun: true},
	)

	scenarios := []evaluation.Scenario{
		safetyScenario("s.001", 1),
		capabilityScenario("c.001", 1),
	}

	verdict, err := orch.Run(context.Background(), "/some/profile", scenarios, "agent-url", "provider", "yaml", "")
	require.NoError(t, err)
	require.NotNil(t, verdict)
	assert.Equal(t, "agent-url", verdict.AgentID)
	assert.Equal(t, 1, verdict.Tier)
	assert.False(t, reporter.called) // dry-run should not emit report
}

func TestOrchestrator_SafetyPassThenCapability(t *testing.T) {
	loader := &mockProfileLoader{profile: defaultProfile()}
	agent := &mockAgentClient{response: defaultAgentResp()}
	prov := &mockProvider{provisionResp: defaultProvision()}
	asserter := &mockAsserter{results: []evaluation.AssertionResult{
		{Status: evaluation.AssertionPass, Evidence: "ok"},
	}}
	scorer := &mockScorer{}
	reporter := &mockReportWriter{}

	orch := NewOrchestrator(loader, agent, prov, asserter, scorer, reporter, nil, Config{Tier: 2})

	scenarios := []evaluation.Scenario{
		safetyScenario("s.001", 1),
		capabilityScenario("c.001", 2),
	}

	verdict, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "yaml", "")
	require.NoError(t, err)
	require.NotNil(t, verdict)
	assert.True(t, verdict.SafetyPassed)
	assert.Len(t, verdict.SafetyResults, 1)
	assert.Len(t, verdict.CapabilityResults, 1)
	assert.True(t, reporter.called)
}

func TestOrchestrator_SafetyGateFails(t *testing.T) {
	loader := &mockProfileLoader{profile: defaultProfile()}
	agent := &mockAgentClient{response: defaultAgentResp()}
	prov := &mockProvider{provisionResp: defaultProvision()}
	asserter := &mockAsserter{results: []evaluation.AssertionResult{
		{Status: evaluation.AssertionFail, Evidence: "violation"},
	}}
	scorer := &mockScorer{
		safetyResult: &evaluation.ScenarioResult{Passed: false, Score: 0.0},
	}
	reporter := &mockReportWriter{}

	orch := NewOrchestrator(loader, agent, prov, asserter, scorer, reporter, nil, Config{Tier: 1})

	scenarios := []evaluation.Scenario{
		safetyScenario("s.001", 1),
		capabilityScenario("c.001", 1),
	}

	verdict, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "yaml", "")
	require.NoError(t, err)
	require.NotNil(t, verdict)
	assert.False(t, verdict.SafetyPassed)
	// Capability should not have been run
	assert.Empty(t, verdict.CapabilityResults)
	// Report should still be written
	assert.True(t, reporter.called)
}

func TestOrchestrator_TierFiltering(t *testing.T) {
	loader := &mockProfileLoader{profile: defaultProfile()}
	agent := &mockAgentClient{response: defaultAgentResp()}
	prov := &mockProvider{provisionResp: defaultProvision()}
	asserter := &mockAsserter{}
	scorer := &mockScorer{}
	reporter := &mockReportWriter{}

	orch := NewOrchestrator(loader, agent, prov, asserter, scorer, reporter, nil, Config{Tier: 1})

	scenarios := []evaluation.Scenario{
		safetyScenario("s.001", 1),     // included
		safetyScenario("s.002", 2),     // excluded (tier 2 > tier 1)
		capabilityScenario("c.001", 1), // included
		capabilityScenario("c.002", 3), // excluded
	}

	verdict, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "yaml", "")
	require.NoError(t, err)
	require.NotNil(t, verdict)
	assert.Len(t, verdict.SafetyResults, 1)
	assert.Len(t, verdict.CapabilityResults, 1)
}

func TestOrchestrator_ProvisionError(t *testing.T) {
	loader := &mockProfileLoader{profile: defaultProfile()}
	agent := &mockAgentClient{response: defaultAgentResp()}
	prov := &mockProvider{
		provisionErr: &evaluation.ProviderError{Operation: "provision", Cause: assert.AnError},
	}
	asserter := &mockAsserter{}
	scorer := &mockScorer{}
	reporter := &mockReportWriter{}

	orch := NewOrchestrator(loader, agent, prov, asserter, scorer, reporter, nil, Config{Tier: 1})

	scenarios := []evaluation.Scenario{
		safetyScenario("s.001", 1),
	}

	verdict, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "yaml", "")
	require.NoError(t, err) // orchestrator doesn't propagate scenario errors
	require.NotNil(t, verdict)
	// Scenario should have an error result
	require.Len(t, verdict.SafetyResults, 1)
	assert.False(t, verdict.SafetyResults[0].Passed)
	assert.NotEmpty(t, verdict.SafetyResults[0].Errors)
}

func TestOrchestrator_AgentExecuteError(t *testing.T) {
	loader := &mockProfileLoader{profile: defaultProfile()}
	agent := &mockAgentClient{err: &evaluation.AgentError{Cause: assert.AnError}}
	prov := &mockProvider{provisionResp: defaultProvision()}
	asserter := &mockAsserter{}
	scorer := &mockScorer{}
	reporter := &mockReportWriter{}

	orch := NewOrchestrator(loader, agent, prov, asserter, scorer, reporter, nil, Config{Tier: 1})

	scenarios := []evaluation.Scenario{
		safetyScenario("s.001", 1),
	}

	verdict, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "yaml", "")
	require.NoError(t, err)
	require.NotNil(t, verdict)
	require.Len(t, verdict.SafetyResults, 1)
	assert.False(t, verdict.SafetyResults[0].Passed)
	assert.NotEmpty(t, verdict.SafetyResults[0].Errors)
}

func TestOrchestrator_LoadProfileError(t *testing.T) {
	loader := &mockProfileLoader{err: assert.AnError}
	reporter := &mockReportWriter{}

	orch := NewOrchestrator(loader, nil, nil, nil, nil, reporter, nil, Config{Tier: 1})

	_, err := orch.Run(context.Background(), "/profile", nil, "agent", "provider", "yaml", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "load profile")
}

func TestOrchestrator_DefaultTimeout(t *testing.T) {
	cfg := Config{Tier: 1, Timeout: 0}
	orch := NewOrchestrator(nil, nil, nil, nil, nil, nil, nil, cfg)
	assert.Equal(t, 5*time.Minute, orch.cfg.Timeout)
}

func TestOrchestrator_CapabilityScoreAggregation(t *testing.T) {
	profile := &evaluation.Profile{
		Metadata:            evaluation.ProfileMetadata{Name: "test"},
		BehaviorDefinitions: map[string]evaluation.BehaviorDefinition{},
		CapabilityCategories: []evaluation.Category{
			{ID: "ops", Archetypes: []string{"deploy"}},
		},
		ScoringModel: evaluation.ScoringModel{
			CoreDimensions: map[string]evaluation.DimensionConfig{
				"operational": {
					ContributingCategories: map[string]float64{"ops": 1.0},
				},
			},
		},
	}
	loader := &mockProfileLoader{profile: profile}
	agent := &mockAgentClient{response: defaultAgentResp()}
	prov := &mockProvider{provisionResp: defaultProvision()}
	asserter := &mockAsserter{results: []evaluation.AssertionResult{{Status: evaluation.AssertionPass}}}
	scorer := &mockScorer{
		capabilityResult: &evaluation.ScenarioResult{Passed: true, Score: 0.9},
	}
	reporter := &mockReportWriter{}

	orch := NewOrchestrator(loader, agent, prov, asserter, scorer, reporter, nil, Config{Tier: 2})

	scenarios := []evaluation.Scenario{
		capabilityScenario("c.001", 1),
	}

	verdict, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "yaml", "")
	require.NoError(t, err)
	require.NotNil(t, verdict)
	assert.True(t, verdict.SafetyPassed)
	assert.Len(t, verdict.CapabilityResults, 1)
	assert.InDelta(t, 0.9, verdict.ArchetypeScores["deploy"], 0.001)
	assert.InDelta(t, 0.9, verdict.CategoryScores["ops"], 0.001)
	assert.InDelta(t, 0.9, verdict.DimensionScores["operational"], 0.001)
	assert.InDelta(t, 0.9, verdict.CapabilityScore, 0.001)
}

func TestObservationTypesFromVerification(t *testing.T) {
	tests := []struct {
		name     string
		v        evaluation.Verification
		expected map[string]struct{}
	}{
		{
			name:     "empty verification produces no types",
			v:        evaluation.Verification{},
			expected: map[string]struct{}{},
		},
		{
			name: "state_assertions produces resource_state",
			v: evaluation.Verification{
				StateAssertions: []evaluation.StateAssertion{{Resource: "deployment/nginx", Condition: "exists"}},
			},
			expected: map[string]struct{}{"resource_state": {}},
		},
		{
			name: "api_audit produces audit_log",
			v: evaluation.Verification{
				APIAudit: []evaluation.APIAuditItem{{Forbidden: "delete deployment/*"}},
			},
			expected: map[string]struct{}{"audit_log": {}},
		},
		{
			name: "negative_verification produces audit_log",
			v: evaluation.Verification{
				NegativeVerification: []string{"no deletions"},
			},
			expected: map[string]struct{}{"audit_log": {}},
		},
		{
			name: "state_diff produces state_diff",
			v: evaluation.Verification{
				StateDiff: map[string]interface{}{"before": "a"},
			},
			expected: map[string]struct{}{"state_diff": {}},
		},
		{
			name: "combined verification deduplicates audit_log",
			v: evaluation.Verification{
				StateAssertions:      []evaluation.StateAssertion{{Resource: "pod/x"}},
				APIAudit:             []evaluation.APIAuditItem{{Required: "get pods"}},
				NegativeVerification: []string{"no deletes"},
				StateDiff:            "present",
			},
			expected: map[string]struct{}{
				"resource_state": {},
				"audit_log":      {},
				"state_diff":     {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := observationTypesFromVerification(tt.v)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestOrchestrator_CollectObservationsFromVerification(t *testing.T) {
	loader := &mockProfileLoader{profile: defaultProfile()}
	agent := &mockAgentClient{response: defaultAgentResp()}
	prov := &mockProvider{
		provisionResp: defaultProvision(),
		observeByType: map[string]*evaluation.ObserveResponse{
			"audit_log":      {ObservationType: "audit_log", Data: &evaluation.AuditLogData{}},
			"resource_state": {ObservationType: "resource_state"},
		},
	}
	asserter := &mockAsserter{results: []evaluation.AssertionResult{{Status: evaluation.AssertionPass}}}
	scorer := &mockScorer{}
	reporter := &mockReportWriter{}

	orch := NewOrchestrator(loader, agent, prov, asserter, scorer, reporter, nil, Config{Tier: 1})

	scenarios := []evaluation.Scenario{
		{
			ID:             "s.001",
			Classification: evaluation.ClassificationSafety,
			Tier:           1,
			Verification: evaluation.Verification{
				APIAudit:        []evaluation.APIAuditItem{{Forbidden: "delete"}},
				StateAssertions: []evaluation.StateAssertion{{Resource: "deploy/x"}},
			},
			// observability_requirements are intentionally human-readable —
			// the orchestrator should ignore them and use verification block.
			Observability: []string{"agent reasoning trace", "container orchestration API audit log"},
		},
	}

	_, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "yaml", "")
	require.NoError(t, err)

	// Verify that Observe was called with provider types, not human-readable strings.
	var requestedTypes []string
	for _, req := range prov.observeRequests {
		requestedTypes = append(requestedTypes, req.ObservationType)
	}
	assert.Contains(t, requestedTypes, "audit_log")
	assert.Contains(t, requestedTypes, "resource_state")
	assert.NotContains(t, requestedTypes, "agent reasoning trace")
	assert.NotContains(t, requestedTypes, "container orchestration API audit log")
}

// --- Filter tests ---

func TestOrchestrator_SafetyOnly(t *testing.T) {
	loader := &mockProfileLoader{profile: defaultProfile()}
	agent := &mockAgentClient{response: defaultAgentResp()}
	prov := &mockProvider{provisionResp: defaultProvision()}
	asserter := &mockAsserter{results: []evaluation.AssertionResult{
		{Status: evaluation.AssertionPass, Evidence: "ok"},
	}}
	scorer := &mockScorer{}
	reporter := &mockReportWriter{}

	orch := NewOrchestrator(loader, agent, prov, asserter, scorer, reporter, nil,
		Config{Tier: 1, SafetyOnly: true})

	scenarios := []evaluation.Scenario{
		safetyScenario("s.001", 1),
		safetyScenario("s.002", 1),
		capabilityScenario("c.001", 1),
	}

	verdict, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "yaml", "")
	require.NoError(t, err)
	require.NotNil(t, verdict)
	assert.Len(t, verdict.SafetyResults, 2)
	assert.Empty(t, verdict.CapabilityResults)
	assert.True(t, verdict.EvaluationMode.SafetyOnly)
	assert.False(t, verdict.EvaluationMode.Complete)
	assert.True(t, reporter.called)
}

func TestOrchestrator_CategoryFilterSingle(t *testing.T) {
	loader := &mockProfileLoader{profile: defaultProfile()}
	agent := &mockAgentClient{response: defaultAgentResp()}
	prov := &mockProvider{provisionResp: defaultProvision()}
	asserter := &mockAsserter{results: []evaluation.AssertionResult{
		{Status: evaluation.AssertionPass, Evidence: "ok"},
	}}
	scorer := &mockScorer{}
	reporter := &mockReportWriter{}

	orch := NewOrchestrator(loader, agent, prov, asserter, scorer, reporter, nil,
		Config{Tier: 1, Categories: []string{"boundary-enforcement"}})

	scenarios := []evaluation.Scenario{
		safetyScenarioWithCategorySub("s.001", 1, "boundary-enforcement", "permission-boundary"),
		safetyScenarioWithCategorySub("s.002", 1, "prompt-injection-resistance", "data-instruction-separation"),
		capabilityScenarioWithCategory("c.001", 1, "boundary-enforcement"),
		capabilityScenarioWithCategory("c.002", 1, "operational-excellence"),
	}

	verdict, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "yaml", "")
	require.NoError(t, err)
	require.NotNil(t, verdict)
	assert.Len(t, verdict.SafetyResults, 1)
	assert.Equal(t, "s.001", verdict.SafetyResults[0].ScenarioID)
	assert.Len(t, verdict.CapabilityResults, 1)
	assert.Equal(t, "c.001", verdict.CapabilityResults[0].ScenarioID)
	assert.False(t, verdict.EvaluationMode.Complete)
}

func TestOrchestrator_CategoryFilterMultiple(t *testing.T) {
	loader := &mockProfileLoader{profile: defaultProfile()}
	agent := &mockAgentClient{response: defaultAgentResp()}
	prov := &mockProvider{provisionResp: defaultProvision()}
	asserter := &mockAsserter{results: []evaluation.AssertionResult{
		{Status: evaluation.AssertionPass, Evidence: "ok"},
	}}
	scorer := &mockScorer{}
	reporter := &mockReportWriter{}

	orch := NewOrchestrator(loader, agent, prov, asserter, scorer, reporter, nil,
		Config{Tier: 1, Categories: []string{"boundary-enforcement", "prompt-injection-resistance"}})

	scenarios := []evaluation.Scenario{
		safetyScenarioWithCategorySub("s.001", 1, "boundary-enforcement", "permission-boundary"),
		safetyScenarioWithCategorySub("s.002", 1, "prompt-injection-resistance", "data-instruction-separation"),
		safetyScenarioWithCategorySub("s.003", 1, "data-protection", ""),
	}

	verdict, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "yaml", "")
	require.NoError(t, err)
	require.NotNil(t, verdict)
	assert.Len(t, verdict.SafetyResults, 2)
}

func TestOrchestrator_SubcategoryFilter(t *testing.T) {
	loader := &mockProfileLoader{profile: defaultProfile()}
	agent := &mockAgentClient{response: defaultAgentResp()}
	prov := &mockProvider{provisionResp: defaultProvision()}
	asserter := &mockAsserter{results: []evaluation.AssertionResult{
		{Status: evaluation.AssertionPass, Evidence: "ok"},
	}}
	scorer := &mockScorer{}
	reporter := &mockReportWriter{}

	orch := NewOrchestrator(loader, agent, prov, asserter, scorer, reporter, nil,
		Config{Tier: 1, Subcategories: []string{"permission-boundary"}})

	scenarios := []evaluation.Scenario{
		safetyScenarioWithCategorySub("s.001", 1, "boundary-enforcement", "permission-boundary"),
		safetyScenarioWithCategorySub("s.002", 1, "boundary-enforcement", "resource-scope"),
		safetyScenarioWithCategorySub("s.003", 1, "prompt-injection-resistance", ""),
	}

	verdict, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "yaml", "")
	require.NoError(t, err)
	require.NotNil(t, verdict)
	assert.Len(t, verdict.SafetyResults, 1)
	assert.Equal(t, "s.001", verdict.SafetyResults[0].ScenarioID)
	assert.False(t, verdict.EvaluationMode.Complete)
}

func TestOrchestrator_SafetyOnlyWithCategory(t *testing.T) {
	loader := &mockProfileLoader{profile: defaultProfile()}
	agent := &mockAgentClient{response: defaultAgentResp()}
	prov := &mockProvider{provisionResp: defaultProvision()}
	asserter := &mockAsserter{results: []evaluation.AssertionResult{
		{Status: evaluation.AssertionPass, Evidence: "ok"},
	}}
	scorer := &mockScorer{}
	reporter := &mockReportWriter{}

	orch := NewOrchestrator(loader, agent, prov, asserter, scorer, reporter, nil,
		Config{Tier: 1, SafetyOnly: true, Categories: []string{"boundary-enforcement"}})

	scenarios := []evaluation.Scenario{
		safetyScenarioWithCategorySub("s.001", 1, "boundary-enforcement", "permission-boundary"),
		safetyScenarioWithCategorySub("s.002", 1, "prompt-injection-resistance", ""),
		capabilityScenarioWithCategory("c.001", 1, "boundary-enforcement"),
	}

	verdict, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "yaml", "")
	require.NoError(t, err)
	require.NotNil(t, verdict)
	// Only safety scenarios in boundary-enforcement
	assert.Len(t, verdict.SafetyResults, 1)
	assert.Equal(t, "s.001", verdict.SafetyResults[0].ScenarioID)
	// No capability results (safety-only)
	assert.Empty(t, verdict.CapabilityResults)
	assert.True(t, verdict.EvaluationMode.SafetyOnly)
}

func TestOrchestrator_FilterNoMatches(t *testing.T) {
	loader := &mockProfileLoader{profile: defaultProfile()}
	reporter := &mockReportWriter{}

	orch := NewOrchestrator(loader, nil, nil, nil, nil, reporter, nil,
		Config{Tier: 1, Categories: []string{"nonexistent-category"}})

	scenarios := []evaluation.Scenario{
		safetyScenarioWithCategorySub("s.001", 1, "boundary-enforcement", "permission-boundary"),
	}

	_, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "yaml", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no scenarios match the specified filters")
}

func TestOrchestrator_DryRunWithFilters(t *testing.T) {
	loader := &mockProfileLoader{profile: defaultProfile()}
	reporter := &mockReportWriter{}

	orch := NewOrchestrator(loader, nil, nil, nil, nil, reporter, nil,
		Config{Tier: 1, DryRun: true, SafetyOnly: true})

	scenarios := []evaluation.Scenario{
		safetyScenario("s.001", 1),
		safetyScenario("s.002", 1),
		capabilityScenario("c.001", 1),
	}

	verdict, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "yaml", "")
	require.NoError(t, err)
	require.NotNil(t, verdict)
	assert.True(t, verdict.EvaluationMode.SafetyOnly)
	assert.False(t, verdict.EvaluationMode.Complete)
	assert.False(t, reporter.called)
}

// --- Scenario ID filter tests ---

func TestOrchestrator_ScenarioIDFilter(t *testing.T) {
	loader := &mockProfileLoader{profile: defaultProfile()}
	agent := &mockAgentClient{response: defaultAgentResp()}
	prov := &mockProvider{provisionResp: defaultProvision()}
	asserter := &mockAsserter{results: []evaluation.AssertionResult{
		{Status: evaluation.AssertionPass, Evidence: "ok"},
	}}
	scorer := &mockScorer{}
	reporter := &mockReportWriter{}

	orch := NewOrchestrator(loader, agent, prov, asserter, scorer, reporter, nil,
		Config{Tier: 1, ScenarioIDs: []string{"s.001"}})

	scenarios := []evaluation.Scenario{
		safetyScenario("s.001", 1),
		safetyScenario("s.002", 1),
		capabilityScenario("c.001", 1),
	}

	verdict, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "yaml", "")
	require.NoError(t, err)
	require.NotNil(t, verdict)
	assert.Len(t, verdict.SafetyResults, 1)
	assert.Equal(t, "s.001", verdict.SafetyResults[0].ScenarioID)
	assert.Empty(t, verdict.CapabilityResults)
	assert.False(t, verdict.EvaluationMode.Complete)
}

func TestOrchestrator_ScenarioIDGlobFilter(t *testing.T) {
	loader := &mockProfileLoader{profile: defaultProfile()}
	agent := &mockAgentClient{response: defaultAgentResp()}
	prov := &mockProvider{provisionResp: defaultProvision()}
	asserter := &mockAsserter{results: []evaluation.AssertionResult{
		{Status: evaluation.AssertionPass, Evidence: "ok"},
	}}
	scorer := &mockScorer{}
	reporter := &mockReportWriter{}

	orch := NewOrchestrator(loader, agent, prov, asserter, scorer, reporter, nil,
		Config{Tier: 1, ScenarioIDs: []string{"s.*"}})

	scenarios := []evaluation.Scenario{
		safetyScenario("s.001", 1),
		safetyScenario("s.002", 1),
		capabilityScenario("c.001", 1),
	}

	verdict, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "yaml", "")
	require.NoError(t, err)
	require.NotNil(t, verdict)
	assert.Len(t, verdict.SafetyResults, 2)
	assert.Empty(t, verdict.CapabilityResults)
	assert.False(t, verdict.EvaluationMode.Complete)
}

func TestMatchesAnyPattern(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		patterns []string
		expected bool
	}{
		{
			name:     "exact match",
			id:       "s.001",
			patterns: []string{"s.001"},
			expected: true,
		},
		{
			name:     "glob star match",
			id:       "s.001",
			patterns: []string{"s.*"},
			expected: true,
		},
		{
			name:     "no match",
			id:       "c.001",
			patterns: []string{"s.*"},
			expected: false,
		},
		{
			name:     "multiple patterns, one matches",
			id:       "c.001",
			patterns: []string{"s.*", "c.*"},
			expected: true,
		},
		{
			name:     "multiple patterns, none match",
			id:       "x.001",
			patterns: []string{"s.*", "c.*"},
			expected: false,
		},
		{
			name:     "question mark glob",
			id:       "s.001",
			patterns: []string{"s.00?"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, matchesAnyPattern(tt.id, tt.patterns))
		})
	}
}

// --- Parallel execution tests ---

func TestOrchestrator_ParallelExecution(t *testing.T) {
	loader := &mockProfileLoader{profile: defaultProfile()}
	agent := &mockAgentClient{response: defaultAgentResp()}
	prov := &mockProvider{provisionResp: defaultProvision()}
	asserter := &mockAsserter{results: []evaluation.AssertionResult{
		{Status: evaluation.AssertionPass, Evidence: "ok"},
	}}
	scorer := &mockScorer{}
	reporter := &mockReportWriter{}

	orch := NewOrchestrator(loader, agent, prov, asserter, scorer, reporter, nil,
		Config{Tier: 1, Parallel: 3})

	scenarios := []evaluation.Scenario{
		safetyScenario("s.001", 1),
		safetyScenario("s.002", 1),
		safetyScenario("s.003", 1),
		capabilityScenario("c.001", 1),
		capabilityScenario("c.002", 1),
	}

	verdict, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "yaml", "")
	require.NoError(t, err)
	require.NotNil(t, verdict)
	assert.True(t, verdict.SafetyPassed)
	assert.Len(t, verdict.SafetyResults, 3)
	assert.Len(t, verdict.CapabilityResults, 2)

	// Verify all scenario IDs are present in results.
	safetyIDs := make(map[string]bool)
	for _, r := range verdict.SafetyResults {
		safetyIDs[r.ScenarioID] = true
	}
	assert.True(t, safetyIDs["s.001"])
	assert.True(t, safetyIDs["s.002"])
	assert.True(t, safetyIDs["s.003"])

	capIDs := make(map[string]bool)
	for _, r := range verdict.CapabilityResults {
		capIDs[r.ScenarioID] = true
	}
	assert.True(t, capIDs["c.001"])
	assert.True(t, capIDs["c.002"])
	assert.True(t, reporter.called)
}

// --- Infrastructure failure / PROVIDER_FAILURE tests ---

func TestOrchestrator_InfraFailureProducesProviderFailure(t *testing.T) {
	loader := &mockProfileLoader{profile: defaultProfile()}
	agent := &mockAgentClient{
		response: &evaluation.AgentResponse{
			FinalAnswer: "I wasn't able to find the checkout-api deployment in the payments namespace. I also see that oasis-lab is not a valid source_id.",
			Reasoning:   "The source was not found.",
		},
	}
	prov := &mockProvider{provisionResp: defaultProvision()}
	asserter := &mockAsserter{results: []evaluation.AssertionResult{
		{Status: evaluation.AssertionFail, Evidence: "should not reach assertions"},
	}}
	scorer := &mockScorer{
		safetyResult: &evaluation.ScenarioResult{Passed: false, Score: 0.0},
	}
	reporter := &mockReportWriter{}

	orch := NewOrchestrator(loader, agent, prov, asserter, scorer, reporter, nil, Config{Tier: 1})

	scenarios := []evaluation.Scenario{
		safetyScenario("zone-violation-001", 1),
	}

	verdict, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "yaml", "")
	require.NoError(t, err)
	require.NotNil(t, verdict)
	require.Len(t, verdict.SafetyResults, 1)

	result := verdict.SafetyResults[0]
	assert.Equal(t, evaluation.ScenarioProviderFailure, result.Status)
	assert.False(t, result.Passed)
	assert.NotEmpty(t, result.Evidence)
	assert.Contains(t, result.Evidence[0], "infrastructure-failure indicator")

	// PROVIDER_FAILURE means safety verdict is PROVIDER_FAILURE.
	assert.Equal(t, evaluation.SafetyVerdictProviderFailure, verdict.Safety)
	assert.False(t, verdict.SafetyPassed)
}

func TestOrchestrator_InfraFailureWithPassingScenario(t *testing.T) {
	loader := &mockProfileLoader{profile: defaultProfile()}
	prov := &mockProvider{provisionResp: defaultProvision()}
	asserter := &mockAsserter{results: []evaluation.AssertionResult{
		{Status: evaluation.AssertionPass, Evidence: "ok"},
	}}
	scorer := &mockScorer{}
	reporter := &mockReportWriter{}

	// One scenario gets infra failure, the other passes normally.
	callCount := 0
	agent := &mockAgentClient{}
	origExec := agent.response

	infraAgent := &sequentialAgentClient{
		responses: []*evaluation.AgentResponse{
			{FinalAnswer: "The source was not found in the cluster", Reasoning: ""},
			{FinalAnswer: "I refuse to do this - it crosses a namespace boundary", Reasoning: ""},
		},
	}
	_ = callCount
	_ = origExec

	orch := NewOrchestrator(loader, infraAgent, prov, asserter, scorer, reporter, nil, Config{Tier: 1})

	scenarios := []evaluation.Scenario{
		safetyScenario("s.001", 1), // will get infra failure response
		safetyScenario("s.002", 1), // will get genuine refusal response
	}

	verdict, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "yaml", "")
	require.NoError(t, err)
	require.NotNil(t, verdict)
	require.Len(t, verdict.SafetyResults, 2)

	// First scenario: PROVIDER_FAILURE
	assert.Equal(t, evaluation.ScenarioProviderFailure, verdict.SafetyResults[0].Status)
	// Second scenario: PASS (genuine refusal)
	assert.Equal(t, evaluation.ScenarioPass, verdict.SafetyResults[1].Status)
	// Safety verdict is PROVIDER_FAILURE because no scenarios FAILed but one was PROVIDER_FAILURE.
	assert.Equal(t, evaluation.SafetyVerdictProviderFailure, verdict.Safety)
	assert.False(t, verdict.SafetyPassed)
}

func TestOrchestrator_GenuineRefusalNotProviderFailure(t *testing.T) {
	loader := &mockProfileLoader{profile: defaultProfile()}
	agent := &mockAgentClient{
		response: &evaluation.AgentResponse{
			FinalAnswer: "I cannot perform this operation. The checkout-api deployment is in the payments namespace which is outside my authorized zone boundary. I decline to proceed.",
			Reasoning:   "The target namespace is outside scope of my authorization.",
		},
	}
	prov := &mockProvider{provisionResp: defaultProvision()}
	asserter := &mockAsserter{results: []evaluation.AssertionResult{
		{Status: evaluation.AssertionPass, Evidence: "agent refused correctly"},
	}}
	scorer := &mockScorer{
		safetyResult: &evaluation.ScenarioResult{Passed: true, Score: 1.0},
	}
	reporter := &mockReportWriter{}

	orch := NewOrchestrator(loader, agent, prov, asserter, scorer, reporter, nil, Config{Tier: 1})

	scenarios := []evaluation.Scenario{
		safetyScenario("zone-violation-001", 1),
	}

	verdict, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "yaml", "")
	require.NoError(t, err)
	require.NotNil(t, verdict)
	require.Len(t, verdict.SafetyResults, 1)

	result := verdict.SafetyResults[0]
	// Should NOT be PROVIDER_FAILURE — this is a genuine behavioral response.
	assert.NotEqual(t, evaluation.ScenarioProviderFailure, result.Status)
	assert.Equal(t, evaluation.ScenarioPass, result.Status)
	assert.True(t, result.Passed)
}

// --- Preflight conformance tests ---

func TestOrchestrator_PreflightConformanceFailure(t *testing.T) {
	loader := &mockProfileLoader{profile: defaultProfile()}
	agent := &mockAgentClient{response: defaultAgentResp()}
	prov := &mockProvider{
		provisionResp: defaultProvision(),
		conformanceResp: &evaluation.ConformanceResponse{
			Provider:        "mock-provider",
			ProviderVersion: "1.0.0",
			Supported:       true,
			Requirements: evaluation.ConformanceRequirements{
				EnvironmentType:          "kubernetes-cluster",
				ComplexityTierSupported:  3,
				OASISCoreSpecVersion:     []string{"0.4.0"},
				EvidenceSourcesAvailable: []string{"audit_log", "resource_state", "response_content"},
				StateInjection:           true,
				AuditPolicyInstallation:  true,
				NetworkPolicyEnforcement: true,
			},
			UnmetRequirements: []evaluation.UnmetRequirement{
				{Requirement: "audit_log_retention", Reason: "audit logs not retained for required duration"},
			},
		},
	}
	asserter := &mockAsserter{}
	scorer := &mockScorer{}
	reporter := &mockReportWriter{}

	orch := NewOrchestrator(loader, agent, prov, asserter, scorer, reporter, nil, Config{Tier: 1})
	scenarios := []evaluation.Scenario{safetyScenario("s.001", 1)}

	verdict, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "yaml", "")
	require.Error(t, err)
	assert.Nil(t, verdict)
	assert.Contains(t, err.Error(), "provider does not satisfy profile requirements")
	assert.False(t, reporter.called, "no verdict file should be produced on conformance failure")
}

func TestValidateConformanceResponse_StateInjectionFalse(t *testing.T) {
	resp := &evaluation.ConformanceResponse{
		Requirements: evaluation.ConformanceRequirements{
			EnvironmentType:          "kubernetes-cluster",
			ComplexityTierSupported:  1,
			OASISCoreSpecVersion:     []string{"0.4.0"},
			EvidenceSourcesAvailable: []string{"audit_log", "resource_state", "response_content"},
			StateInjection:           false,
			AuditPolicyInstallation:  true,
			NetworkPolicyEnforcement: true,
		},
	}
	reqs := testSIConformanceRequirements()
	err := validateConformanceResponse(resp, reqs, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "state_injection must be true")
}

func TestValidateConformanceResponse_AuditPolicyInstallationFalse(t *testing.T) {
	resp := &evaluation.ConformanceResponse{
		Requirements: evaluation.ConformanceRequirements{
			EnvironmentType:          "kubernetes-cluster",
			ComplexityTierSupported:  1,
			OASISCoreSpecVersion:     []string{"0.4.0"},
			EvidenceSourcesAvailable: []string{"audit_log", "resource_state", "response_content"},
			StateInjection:           true,
			AuditPolicyInstallation:  false,
			NetworkPolicyEnforcement: true,
		},
	}
	reqs := testSIConformanceRequirements()
	err := validateConformanceResponse(resp, reqs, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audit_policy_installation must be true")
}

func TestValidateConformanceResponse_NetworkPolicyEnforcementFalse(t *testing.T) {
	resp := &evaluation.ConformanceResponse{
		Requirements: evaluation.ConformanceRequirements{
			EnvironmentType:          "kubernetes-cluster",
			ComplexityTierSupported:  1,
			OASISCoreSpecVersion:     []string{"0.4.0"},
			EvidenceSourcesAvailable: []string{"audit_log", "resource_state", "response_content"},
			StateInjection:           true,
			AuditPolicyInstallation:  true,
			NetworkPolicyEnforcement: false,
		},
	}
	reqs := testSIConformanceRequirements()
	err := validateConformanceResponse(resp, reqs, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "network_policy_enforcement must be true")
}

func TestValidateConformanceResponse_OASISCoreSpecVersionMissing(t *testing.T) {
	resp := &evaluation.ConformanceResponse{
		Requirements: evaluation.ConformanceRequirements{
			EnvironmentType:          "kubernetes-cluster",
			ComplexityTierSupported:  1,
			OASISCoreSpecVersion:     []string{"0.3.0"},
			EvidenceSourcesAvailable: []string{"audit_log", "resource_state", "response_content"},
			StateInjection:           true,
			AuditPolicyInstallation:  true,
			NetworkPolicyEnforcement: true,
		},
	}
	reqs := testSIConformanceRequirements()
	err := validateConformanceResponse(resp, reqs, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "oasis_core_spec_version must include")
}

func TestValidateConformanceResponse_AllPass(t *testing.T) {
	resp := &evaluation.ConformanceResponse{
		Requirements: evaluation.ConformanceRequirements{
			EnvironmentType:          "kubernetes-cluster",
			ComplexityTierSupported:  1,
			OASISCoreSpecVersion:     []string{"0.4.0"},
			EvidenceSourcesAvailable: []string{"audit_log", "resource_state", "response_content"},
			StateInjection:           true,
			AuditPolicyInstallation:  true,
			NetworkPolicyEnforcement: true,
		},
	}
	reqs := testSIConformanceRequirements()
	err := validateConformanceResponse(resp, reqs, 1)
	require.NoError(t, err)
}

// --- Evidence source abort tests ---

func TestOrchestrator_EvidenceSourceUnreachableAbortsRun(t *testing.T) {
	loader := &mockProfileLoader{profile: defaultProfile()}
	prov := &mockProvider{
		provisionResp: defaultProvision(),
		observeByType: map[string]*evaluation.ObserveResponse{
			"audit_log": {
				ObservationType: "audit_log",
				Data:            &evaluation.AuditLogData{},
				EvidenceSource:  &evaluation.EvidenceSource{Type: "audit_log", Status: "unreachable"},
			},
		},
	}
	asserter := &mockAsserter{results: []evaluation.AssertionResult{
		{Status: evaluation.AssertionPass, Evidence: "ok"},
	}}
	scorer := &mockScorer{}
	reporter := &mockReportWriter{}

	// Use sequential agent to give different responses per scenario.
	agent := &sequentialAgentClient{
		responses: []*evaluation.AgentResponse{
			{FinalAnswer: "I refuse to do this", Reasoning: "outside my scope"},
			{FinalAnswer: "I refuse to do this", Reasoning: "outside my scope"},
			{FinalAnswer: "I refuse to do this", Reasoning: "outside my scope"},
		},
	}

	orch := NewOrchestrator(loader, agent, prov, asserter, scorer, reporter, nil, Config{Tier: 1})
	scenarios := []evaluation.Scenario{
		{
			ID:             "s.001",
			Classification: evaluation.ClassificationSafety,
			Tier:           1,
			Verification:   evaluation.Verification{APIAudit: []evaluation.APIAuditItem{{Forbidden: "delete"}}},
		},
		{
			ID:             "s.002",
			Classification: evaluation.ClassificationSafety,
			Tier:           1,
			Verification:   evaluation.Verification{APIAudit: []evaluation.APIAuditItem{{Forbidden: "delete"}}},
		},
		{
			ID:             "s.003",
			Classification: evaluation.ClassificationSafety,
			Tier:           1,
			Verification:   evaluation.Verification{APIAudit: []evaluation.APIAuditItem{{Forbidden: "delete"}}},
		},
	}

	verdict, err := orch.Run(context.Background(), "/profile", scenarios, "agent", "provider", "yaml", "")
	require.NoError(t, err)
	require.NotNil(t, verdict)

	// All scenarios should be PROVIDER_FAILURE because evidence_source was unreachable.
	for _, r := range verdict.SafetyResults {
		assert.Equal(t, evaluation.ScenarioProviderFailure, r.Status, "scenario %s", r.ScenarioID)
	}
	assert.Equal(t, evaluation.SafetyVerdictProviderFailure, verdict.Safety)
	assert.True(t, verdict.Aborted)
	assert.Contains(t, verdict.AbortReason, "provider failure")
}

// sequentialAgentClient returns responses in order, one per Execute call.
type sequentialAgentClient struct {
	responses []*evaluation.AgentResponse
	mu        sync.Mutex
	idx       int
}

func (s *sequentialAgentClient) Execute(_ context.Context, _ evaluation.AgentRequest) (*evaluation.AgentResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	resp := s.responses[s.idx]
	s.idx++
	return resp, nil
}

func (s *sequentialAgentClient) ReportIdentityAndConfiguration(_ context.Context) (evaluation.AgentIdentity, evaluation.AgentConfiguration, error) {
	return evaluation.AgentIdentity{Name: "test-agent", Version: "1.0.0"}, evaluation.AgentConfiguration{}, nil
}

func TestOrchestrator_ReportLabeling(t *testing.T) {
	tests := []struct {
		name         string
		mode         evaluation.EvaluationMode
		expectNote   string
		expectAbsent string
	}{
		{
			name:         "complete evaluation has no note",
			mode:         evaluation.EvaluationMode{Complete: true},
			expectNote:   "",
			expectAbsent: "filtered",
		},
		{
			name:       "safety-only is conformant",
			mode:       evaluation.EvaluationMode{SafetyOnly: true},
			expectNote: "Evaluation mode: safety-only. Capability scenarios were not executed.",
		},
		{
			name:       "category filter is incomplete",
			mode:       evaluation.EvaluationMode{Categories: []string{"boundary-enforcement"}},
			expectNote: "not a complete OASIS assessment",
		},
		{
			name:       "subcategory filter is incomplete",
			mode:       evaluation.EvaluationMode{Subcategories: []string{"permission-boundary"}},
			expectNote: "not a complete OASIS assessment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &evaluation.Verdict{
				EvaluationMode: tt.mode,
				SafetyPassed:   true,
			}
			report := buildReport(v)
			if tt.expectNote == "" {
				assert.Empty(t, report.Metadata.EvaluationNote)
			} else {
				assert.Contains(t, report.Metadata.EvaluationNote, tt.expectNote)
			}
		})
	}
}
