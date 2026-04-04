package execution

import (
	"fmt"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// IsApplicable checks whether a scenario applies to the given agent configuration.
// For each key in the scenario's applicability map, the agent's configuration must match.
// Supports exact match (single value) and any-of match (slice of values).
// A scenario with no applicability conditions is always applicable.
func IsApplicable(s *evaluation.Scenario, config evaluation.AgentConfiguration) bool {
	if len(s.Applicability) == 0 {
		return true
	}

	for key, required := range s.Applicability {
		actual, ok := config[key]
		if !ok {
			return false
		}
		if !matchesValue(actual, required) {
			return false
		}
	}
	return true
}

// matchesValue checks if actual matches required. Required can be a single value
// or a slice of values (any-of).
func matchesValue(actual, required interface{}) bool {
	// Any-of match: required is a slice.
	if slice, ok := toSlice(required); ok {
		for _, v := range slice {
			if valuesEqual(actual, v) {
				return true
			}
		}
		return false
	}
	// Exact match.
	return valuesEqual(actual, required)
}

// valuesEqual compares two values, handling type normalization between
// string, bool, and numeric types that YAML may produce.
func valuesEqual(a, b interface{}) bool {
	// Normalize: YAML may decode booleans as bool or strings.
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// toSlice attempts to convert an interface{} to a []interface{}.
func toSlice(v interface{}) ([]interface{}, bool) {
	switch s := v.(type) {
	case []interface{}:
		return s, true
	case []string:
		out := make([]interface{}, len(s))
		for i, val := range s {
			out[i] = val
		}
		return out, true
	default:
		return nil, false
	}
}

// ResolveConfiguration validates and fills defaults for the agent's reported configuration
// against the profile's schema. Returns the effective configuration.
func ResolveConfiguration(
	raw evaluation.AgentConfiguration,
	schema *evaluation.AgentConfigurationSchema,
) (evaluation.AgentConfiguration, error) {
	if schema == nil || len(schema.Dimensions) == 0 {
		return raw, nil
	}

	// Build dimension lookup.
	dims := make(map[string]evaluation.ConfigurationDimension, len(schema.Dimensions))
	for _, d := range schema.Dimensions {
		dims[d.Identifier] = d
	}

	// Check for unknown dimensions reported by the adapter.
	for key := range raw {
		if _, ok := dims[key]; !ok {
			return nil, fmt.Errorf("agent reported unknown configuration dimension %q", key)
		}
	}

	effective := make(evaluation.AgentConfiguration, len(dims))

	for _, dim := range schema.Dimensions {
		val, reported := raw[dim.Identifier]
		if !reported {
			if dim.Default != nil {
				effective[dim.Identifier] = *dim.Default
			}
			// If not reported and no default, leave absent — scenarios requiring
			// this dimension will be NOT_APPLICABLE.
			continue
		}

		// Validate enum values.
		if dim.Type == "enum" {
			strVal := fmt.Sprintf("%v", val)
			valid := false
			for _, allowed := range dim.Values {
				if strVal == allowed {
					valid = true
					break
				}
			}
			if !valid {
				return nil, fmt.Errorf("agent reported invalid value %q for enum dimension %q (allowed: %v)", strVal, dim.Identifier, dim.Values)
			}
		}

		effective[dim.Identifier] = val
	}

	return effective, nil
}

// MergeConditionalAssertions finds the matching conditional block (if any) and
// merges its must/must_not into the base assertions. Returns an error if more
// than one conditional block matches.
func MergeConditionalAssertions(
	base evaluation.Assertions,
	conditionals []evaluation.ConditionalAssertion,
	config evaluation.AgentConfiguration,
) (evaluation.Assertions, error) {
	if len(conditionals) == 0 {
		return base, nil
	}

	var matched *evaluation.ConditionalAssertion
	matchCount := 0

	for i := range conditionals {
		if conditionMatches(conditionals[i].When, config) {
			matched = &conditionals[i]
			matchCount++
		}
	}

	if matchCount > 1 {
		return evaluation.Assertions{}, fmt.Errorf("multiple conditional assertion blocks match the agent configuration (%d matched)", matchCount)
	}

	if matched == nil {
		return base, nil
	}

	merged := evaluation.Assertions{
		Must:    make([]evaluation.AssertionItem, len(base.Must)),
		MustNot: make([]evaluation.AssertionItem, len(base.MustNot)),
	}
	copy(merged.Must, base.Must)
	copy(merged.MustNot, base.MustNot)
	merged.Must = append(merged.Must, matched.Must...)
	merged.MustNot = append(merged.MustNot, matched.MustNot...)

	return merged, nil
}

// conditionMatches checks if all keys in the when clause match the config.
func conditionMatches(when map[string]interface{}, config evaluation.AgentConfiguration) bool {
	for key, required := range when {
		actual, ok := config[key]
		if !ok {
			return false
		}
		if !matchesValue(actual, required) {
			return false
		}
	}
	return true
}

// ComputeConfigurationCoverage calculates coverage stats from scenario results.
func ComputeConfigurationCoverage(results []evaluation.ScenarioResult) *evaluation.ConfigurationCoverage {
	cov := &evaluation.ConfigurationCoverage{
		NotApplicableByCategory: make(map[string]int),
	}

	totalByCategory := make(map[string]int)

	for _, r := range results {
		cov.TotalScenarios++
		if r.Status == evaluation.ScenarioNotApplicable {
			cov.NotApplicable++
			cov.NotApplicableByCategory[r.Category]++
		} else {
			cov.Applicable++
		}
		totalByCategory[r.Category]++
	}

	// Warn if >50% of scenarios in any category are NOT_APPLICABLE.
	for cat, naCount := range cov.NotApplicableByCategory {
		total := totalByCategory[cat]
		if total > 0 && float64(naCount)/float64(total) > 0.5 {
			cov.Warnings = append(cov.Warnings,
				fmt.Sprintf("category %q: %d of %d scenarios (%.0f%%) are NOT_APPLICABLE for this agent configuration",
					cat, naCount, total, float64(naCount)/float64(total)*100))
		}
	}

	return cov
}
