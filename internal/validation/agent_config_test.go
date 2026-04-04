package validation

import (
	"testing"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

func TestValidateAgentConfigSchema_Empty(t *testing.T) {
	schema := &evaluation.AgentConfigurationSchema{}
	verr := &evaluation.ValidationError{}
	validateAgentConfigSchema(schema, nil, verr)
	if !verr.HasIssues() {
		t.Error("expected error for empty schema")
	}
}

func TestValidateAgentConfigSchema_DuplicateDimensions(t *testing.T) {
	schema := &evaluation.AgentConfigurationSchema{
		Dimensions: []evaluation.ConfigurationDimension{
			{Identifier: "mode", Type: "enum", Values: []string{"a", "b"}},
			{Identifier: "mode", Type: "enum", Values: []string{"x", "y"}},
		},
	}
	verr := &evaluation.ValidationError{}
	validateAgentConfigSchema(schema, nil, verr)
	if !verr.HasIssues() {
		t.Error("expected error for duplicate dimension identifiers")
	}
}

func TestValidateAgentConfigSchema_EnumTooFewValues(t *testing.T) {
	schema := &evaluation.AgentConfigurationSchema{
		Dimensions: []evaluation.ConfigurationDimension{
			{Identifier: "mode", Type: "enum", Values: []string{"only_one"}},
		},
	}
	verr := &evaluation.ValidationError{}
	validateAgentConfigSchema(schema, nil, verr)
	if !verr.HasIssues() {
		t.Error("expected error for enum with fewer than two values")
	}
}

func TestValidateAgentConfigSchema_InvalidType(t *testing.T) {
	schema := &evaluation.AgentConfigurationSchema{
		Dimensions: []evaluation.ConfigurationDimension{
			{Identifier: "mode", Type: "integer"},
		},
	}
	verr := &evaluation.ValidationError{}
	validateAgentConfigSchema(schema, nil, verr)
	if !verr.HasIssues() {
		t.Error("expected error for invalid dimension type")
	}
}

func TestValidateAgentConfigSchema_InvalidApplicabilityRef(t *testing.T) {
	schema := &evaluation.AgentConfigurationSchema{
		Dimensions: []evaluation.ConfigurationDimension{
			{Identifier: "mode", Type: "enum", Values: []string{"a", "b"}},
		},
	}
	scenarios := []evaluation.Scenario{
		{
			ID:            "s1",
			Applicability: map[string]interface{}{"nonexistent_dim": "value"},
		},
	}
	verr := &evaluation.ValidationError{}
	validateAgentConfigSchema(schema, scenarios, verr)
	if !verr.HasIssues() {
		t.Error("expected error for unknown dimension in applicability")
	}
}

func TestValidateAgentConfigSchema_InvalidEnumValueInApplicability(t *testing.T) {
	schema := &evaluation.AgentConfigurationSchema{
		Dimensions: []evaluation.ConfigurationDimension{
			{Identifier: "mode", Type: "enum", Values: []string{"read_only", "read_write"}},
		},
	}
	scenarios := []evaluation.Scenario{
		{
			ID:            "s1",
			Applicability: map[string]interface{}{"mode": "bogus"},
		},
	}
	verr := &evaluation.ValidationError{}
	validateAgentConfigSchema(schema, scenarios, verr)
	if !verr.HasIssues() {
		t.Error("expected error for invalid enum value")
	}
}

func TestValidateAgentConfigSchema_ValidConditionalRef(t *testing.T) {
	schema := &evaluation.AgentConfigurationSchema{
		Dimensions: []evaluation.ConfigurationDimension{
			{Identifier: "mode", Type: "enum", Values: []string{"read_only", "read_write"}},
		},
	}
	scenarios := []evaluation.Scenario{
		{
			ID: "s1",
			Conditional: []evaluation.ConditionalAssertion{
				{When: map[string]interface{}{"mode": "read_write"}},
			},
		},
	}
	verr := &evaluation.ValidationError{}
	validateAgentConfigSchema(schema, scenarios, verr)
	if verr.HasIssues() {
		t.Errorf("unexpected errors: %v", verr)
	}
}

func TestValidateAgentConfigSchema_InvalidConditionalRef(t *testing.T) {
	schema := &evaluation.AgentConfigurationSchema{
		Dimensions: []evaluation.ConfigurationDimension{
			{Identifier: "mode", Type: "enum", Values: []string{"read_only", "read_write"}},
		},
	}
	scenarios := []evaluation.Scenario{
		{
			ID: "s1",
			Conditional: []evaluation.ConditionalAssertion{
				{When: map[string]interface{}{"unknown_dim": "value"}},
			},
		},
	}
	verr := &evaluation.ValidationError{}
	validateAgentConfigSchema(schema, scenarios, verr)
	if !verr.HasIssues() {
		t.Error("expected error for unknown dimension in conditional when clause")
	}
}
