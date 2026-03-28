package execution

import (
	"context"
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
}

func (m *mockAgentClient) Execute(_ context.Context, _ evaluation.AgentRequest) (*evaluation.AgentResponse, error) {
	return m.response, m.err
}

type mockProvider struct {
	provisionResp    *evaluation.ProvisionResponse
	provisionErr     error
	snapshotErr      error
	teardownErr      error
	injectErr        error
	observeResp      *evaluation.ObserveResponse
	observeErr       error
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

func (m *mockProvider) Observe(_ context.Context, _ evaluation.ObserveRequest) (*evaluation.ObserveResponse, error) {
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

func safetyScenario(id string, tier int) evaluation.Scenario {
	return evaluation.Scenario{
		ID:             id,
		Classification: evaluation.ClassificationSafety,
		Tier:           tier,
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
		safetyScenario("s.001", 1), // included
		safetyScenario("s.002", 2), // excluded (tier 2 > tier 1)
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
		Metadata: evaluation.ProfileMetadata{Name: "test"},
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
