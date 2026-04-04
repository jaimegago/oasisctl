package validation

import (
	"fmt"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// validateAgentConfigSchema validates the agent configuration schema and all
// scenario references to it.
func validateAgentConfigSchema(
	schema *evaluation.AgentConfigurationSchema,
	scenarios []evaluation.Scenario,
	verr *evaluation.ValidationError,
) {
	if len(schema.Dimensions) == 0 {
		verr.Add("agent_configuration_schema", "must define at least one dimension")
		return
	}

	// Build dimension lookup and check uniqueness.
	dims := make(map[string]evaluation.ConfigurationDimension, len(schema.Dimensions))
	for _, d := range schema.Dimensions {
		if _, exists := dims[d.Identifier]; exists {
			verr.Add("agent_configuration_schema",
				fmt.Sprintf("duplicate dimension identifier %q", d.Identifier))
		}
		if d.Type == "enum" && len(d.Values) < 2 {
			verr.Add(fmt.Sprintf("agent_configuration_schema.%s", d.Identifier),
				"enum dimensions must have at least two values")
		}
		if d.Type != "enum" && d.Type != "boolean" {
			verr.Add(fmt.Sprintf("agent_configuration_schema.%s", d.Identifier),
				fmt.Sprintf("invalid dimension type %q (must be enum or boolean)", d.Type))
		}
		dims[d.Identifier] = d
	}

	// Build valid values lookup for enum dimensions.
	enumValues := make(map[string]map[string]struct{})
	for _, d := range schema.Dimensions {
		if d.Type == "enum" {
			vals := make(map[string]struct{}, len(d.Values))
			for _, v := range d.Values {
				vals[v] = struct{}{}
			}
			enumValues[d.Identifier] = vals
		}
	}

	// Validate scenario applicability and conditional assertion references.
	for _, s := range scenarios {
		validateDimensionRefs(s.ID, "applicability", s.Applicability, dims, enumValues, verr)

		for i, cond := range s.Conditional {
			validateDimensionRefs(s.ID, fmt.Sprintf("conditional[%d].when", i), cond.When, dims, enumValues, verr)
		}
	}
}

// validateDimensionRefs checks that all keys in a condition map reference valid
// dimensions and values.
func validateDimensionRefs(
	scenarioID string,
	field string,
	conditions map[string]interface{},
	dims map[string]evaluation.ConfigurationDimension,
	enumValues map[string]map[string]struct{},
	verr *evaluation.ValidationError,
) {
	for key, val := range conditions {
		dim, ok := dims[key]
		if !ok {
			verr.Add(fmt.Sprintf("scenario %s: %s", scenarioID, field),
				fmt.Sprintf("references unknown dimension %q", key))
			continue
		}

		// Validate enum values.
		if dim.Type == "enum" {
			vals := enumValues[key]
			validateEnumValue(scenarioID, field, key, val, vals, verr)
		}
	}
}

// validateEnumValue checks that a value (or slice of values) is valid for an enum dimension.
func validateEnumValue(
	scenarioID, field, dimID string,
	val interface{},
	allowed map[string]struct{},
	verr *evaluation.ValidationError,
) {
	switch v := val.(type) {
	case string:
		if _, ok := allowed[v]; !ok {
			verr.Add(fmt.Sprintf("scenario %s: %s", scenarioID, field),
				fmt.Sprintf("invalid value %q for enum dimension %q", v, dimID))
		}
	case []interface{}:
		for _, item := range v {
			s := fmt.Sprintf("%v", item)
			if _, ok := allowed[s]; !ok {
				verr.Add(fmt.Sprintf("scenario %s: %s", scenarioID, field),
					fmt.Sprintf("invalid value %q for enum dimension %q", s, dimID))
			}
		}
	}
}
