package execution

import (
	"testing"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

func TestIsApplicable_NoConditions(t *testing.T) {
	s := &evaluation.Scenario{ID: "test-001"}
	config := evaluation.AgentConfiguration{"operational_mode": "read_write"}
	if !IsApplicable(s, config) {
		t.Error("scenario with no applicability should always be applicable")
	}
}

func TestIsApplicable_SingleExactMatch(t *testing.T) {
	s := &evaluation.Scenario{
		ID:            "test-002",
		Applicability: map[string]interface{}{"operational_mode": "read_write"},
	}

	match := evaluation.AgentConfiguration{"operational_mode": "read_write"}
	if !IsApplicable(s, match) {
		t.Error("expected scenario to be applicable for matching config")
	}

	noMatch := evaluation.AgentConfiguration{"operational_mode": "read_only"}
	if IsApplicable(s, noMatch) {
		t.Error("expected scenario to not be applicable for non-matching config")
	}
}

func TestIsApplicable_MultipleConditions(t *testing.T) {
	s := &evaluation.Scenario{
		ID: "test-003",
		Applicability: map[string]interface{}{
			"operational_mode": "read_write",
			"zone_model":       true,
		},
	}

	allMatch := evaluation.AgentConfiguration{
		"operational_mode": "read_write",
		"zone_model":       true,
	}
	if !IsApplicable(s, allMatch) {
		t.Error("expected applicable when all conditions match")
	}

	partialMatch := evaluation.AgentConfiguration{
		"operational_mode": "read_write",
		"zone_model":       false,
	}
	if IsApplicable(s, partialMatch) {
		t.Error("expected not applicable when only some conditions match")
	}
}

func TestIsApplicable_AnyOfMatch(t *testing.T) {
	s := &evaluation.Scenario{
		ID: "test-004",
		Applicability: map[string]interface{}{
			"operational_mode": []interface{}{"read_write", "supervised"},
		},
	}

	if !IsApplicable(s, evaluation.AgentConfiguration{"operational_mode": "read_write"}) {
		t.Error("expected applicable for first any-of value")
	}
	if !IsApplicable(s, evaluation.AgentConfiguration{"operational_mode": "supervised"}) {
		t.Error("expected applicable for second any-of value")
	}
	if IsApplicable(s, evaluation.AgentConfiguration{"operational_mode": "read_only"}) {
		t.Error("expected not applicable for value not in any-of list")
	}
}

func TestIsApplicable_MissingDimension(t *testing.T) {
	s := &evaluation.Scenario{
		ID:            "test-005",
		Applicability: map[string]interface{}{"zone_model": true},
	}
	config := evaluation.AgentConfiguration{"operational_mode": "read_write"}
	if IsApplicable(s, config) {
		t.Error("expected not applicable when required dimension is missing from config")
	}
}

func TestResolveConfiguration_Defaults(t *testing.T) {
	boolTrue := interface{}(true)
	schema := &evaluation.AgentConfigurationSchema{
		Dimensions: []evaluation.ConfigurationDimension{
			{Identifier: "operational_mode", Type: "enum", Values: []string{"read_only", "read_write"}},
			{Identifier: "zone_model", Type: "boolean", Default: &boolTrue},
		},
	}

	raw := evaluation.AgentConfiguration{"operational_mode": "read_write"}
	effective, err := ResolveConfiguration(raw, schema)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if effective["zone_model"] != true {
		t.Errorf("expected default zone_model=true, got %v", effective["zone_model"])
	}
	if effective["operational_mode"] != "read_write" {
		t.Errorf("expected operational_mode=read_write, got %v", effective["operational_mode"])
	}
}

func TestResolveConfiguration_UnknownDimension(t *testing.T) {
	schema := &evaluation.AgentConfigurationSchema{
		Dimensions: []evaluation.ConfigurationDimension{
			{Identifier: "operational_mode", Type: "enum", Values: []string{"read_only", "read_write"}},
		},
	}

	raw := evaluation.AgentConfiguration{"operational_mode": "read_write", "bogus": "value"}
	_, err := ResolveConfiguration(raw, schema)
	if err == nil {
		t.Error("expected error for unknown dimension")
	}
}

func TestResolveConfiguration_InvalidEnumValue(t *testing.T) {
	schema := &evaluation.AgentConfigurationSchema{
		Dimensions: []evaluation.ConfigurationDimension{
			{Identifier: "operational_mode", Type: "enum", Values: []string{"read_only", "read_write"}},
		},
	}

	raw := evaluation.AgentConfiguration{"operational_mode": "invalid"}
	_, err := ResolveConfiguration(raw, schema)
	if err == nil {
		t.Error("expected error for invalid enum value")
	}
}

func TestResolveConfiguration_NilSchema(t *testing.T) {
	raw := evaluation.AgentConfiguration{"anything": "goes"}
	effective, err := ResolveConfiguration(raw, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if effective["anything"] != "goes" {
		t.Error("expected passthrough with nil schema")
	}
}

func TestMergeConditionalAssertions_NoConditionals(t *testing.T) {
	base := evaluation.Assertions{
		Must: []evaluation.AssertionItem{{Behavior: "b1"}},
	}
	merged, err := MergeConditionalAssertions(base, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(merged.Must) != 1 {
		t.Errorf("expected 1 must assertion, got %d", len(merged.Must))
	}
}

func TestMergeConditionalAssertions_MatchingBlock(t *testing.T) {
	base := evaluation.Assertions{
		Must: []evaluation.AssertionItem{{Behavior: "base_behavior"}},
	}
	conditionals := []evaluation.ConditionalAssertion{
		{
			When:    map[string]interface{}{"operational_mode": "read_write"},
			Must:    []evaluation.AssertionItem{{Behavior: "extra_behavior"}},
			MustNot: []evaluation.AssertionItem{{Action: "forbidden_action"}},
		},
	}
	config := evaluation.AgentConfiguration{"operational_mode": "read_write"}

	merged, err := MergeConditionalAssertions(base, conditionals, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(merged.Must) != 2 {
		t.Errorf("expected 2 must assertions, got %d", len(merged.Must))
	}
	if len(merged.MustNot) != 1 {
		t.Errorf("expected 1 must_not assertion, got %d", len(merged.MustNot))
	}
}

func TestMergeConditionalAssertions_NoMatch(t *testing.T) {
	base := evaluation.Assertions{
		Must: []evaluation.AssertionItem{{Behavior: "base"}},
	}
	conditionals := []evaluation.ConditionalAssertion{
		{
			When: map[string]interface{}{"operational_mode": "read_only"},
			Must: []evaluation.AssertionItem{{Behavior: "extra"}},
		},
	}
	config := evaluation.AgentConfiguration{"operational_mode": "read_write"}

	merged, err := MergeConditionalAssertions(base, conditionals, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(merged.Must) != 1 {
		t.Errorf("expected 1 must assertion (no merge), got %d", len(merged.Must))
	}
}

func TestMergeConditionalAssertions_MultipleMatches(t *testing.T) {
	base := evaluation.Assertions{}
	conditionals := []evaluation.ConditionalAssertion{
		{When: map[string]interface{}{"operational_mode": "read_write"}},
		{When: map[string]interface{}{"operational_mode": "read_write"}},
	}
	config := evaluation.AgentConfiguration{"operational_mode": "read_write"}

	_, err := MergeConditionalAssertions(base, conditionals, config)
	if err == nil {
		t.Error("expected error when multiple conditional blocks match")
	}
}

func TestMergeConditionalAssertions_NonOverlapping(t *testing.T) {
	base := evaluation.Assertions{
		Must: []evaluation.AssertionItem{{Behavior: "base"}},
	}
	conditionals := []evaluation.ConditionalAssertion{
		{
			When: map[string]interface{}{"operational_mode": "read_only"},
			Must: []evaluation.AssertionItem{{Behavior: "ro_extra"}},
		},
		{
			When: map[string]interface{}{"operational_mode": "read_write"},
			Must: []evaluation.AssertionItem{{Behavior: "rw_extra"}},
		},
	}
	config := evaluation.AgentConfiguration{"operational_mode": "read_write"}

	merged, err := MergeConditionalAssertions(base, conditionals, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(merged.Must) != 2 {
		t.Errorf("expected 2 must assertions, got %d", len(merged.Must))
	}
	if merged.Must[1].Behavior != "rw_extra" {
		t.Errorf("expected rw_extra, got %s", merged.Must[1].Behavior)
	}
}

func TestComputeConfigurationCoverage_Basic(t *testing.T) {
	results := []evaluation.ScenarioResult{
		{ScenarioID: "s1", Category: "sec", Status: evaluation.ScenarioPass},
		{ScenarioID: "s2", Category: "sec", Status: evaluation.ScenarioNotApplicable},
		{ScenarioID: "s3", Category: "sec", Status: evaluation.ScenarioFail},
		{ScenarioID: "s4", Category: "ops", Status: evaluation.ScenarioPass},
	}

	cov := ComputeConfigurationCoverage(results)
	if cov.TotalScenarios != 4 {
		t.Errorf("expected total=4, got %d", cov.TotalScenarios)
	}
	if cov.Applicable != 3 {
		t.Errorf("expected applicable=3, got %d", cov.Applicable)
	}
	if cov.NotApplicable != 1 {
		t.Errorf("expected not_applicable=1, got %d", cov.NotApplicable)
	}
	if cov.NotApplicableByCategory["sec"] != 1 {
		t.Errorf("expected sec na=1, got %d", cov.NotApplicableByCategory["sec"])
	}
}

func TestComputeConfigurationCoverage_WarningThreshold(t *testing.T) {
	// 2 out of 3 in "sec" are NOT_APPLICABLE = 66% > 50%
	results := []evaluation.ScenarioResult{
		{ScenarioID: "s1", Category: "sec", Status: evaluation.ScenarioNotApplicable},
		{ScenarioID: "s2", Category: "sec", Status: evaluation.ScenarioNotApplicable},
		{ScenarioID: "s3", Category: "sec", Status: evaluation.ScenarioPass},
		{ScenarioID: "s4", Category: "ops", Status: evaluation.ScenarioPass},
	}

	cov := ComputeConfigurationCoverage(results)
	if len(cov.Warnings) == 0 {
		t.Error("expected warning for sec category exceeding 50% NOT_APPLICABLE")
	}
}

func TestComputeConfigurationCoverage_ExactlyHalf(t *testing.T) {
	// 1 out of 2 = 50%, should NOT trigger warning (>50% required)
	results := []evaluation.ScenarioResult{
		{ScenarioID: "s1", Category: "sec", Status: evaluation.ScenarioNotApplicable},
		{ScenarioID: "s2", Category: "sec", Status: evaluation.ScenarioPass},
	}

	cov := ComputeConfigurationCoverage(results)
	if len(cov.Warnings) != 0 {
		t.Errorf("expected no warnings at exactly 50%%, got %v", cov.Warnings)
	}
}
