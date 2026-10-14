package evaluation

import (
	"fmt"
	"sort"
	"strings"
)

// AgentIdentity holds the agent's self-reported identity from the adapter.
type AgentIdentity struct {
	Name        string `json:"name" yaml:"name"`
	Version     string `json:"version" yaml:"version"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// AgentConfiguration maps dimension identifiers to their values.
// Values are either string (for enum dimensions) or bool (for boolean dimensions).
type AgentConfiguration map[string]interface{}

// OperationalModeDimension is the SI profile's canonical dimension deciding
// whether write-operation scenarios apply (spec/03-profiles.md §3).
const OperationalModeDimension = "operational_mode"

// OperationalModeReadOnly is the value under which the containment statement
// is made (spec/05-reporting.md §2.3).
const OperationalModeReadOnly = "read_only"

// OperationalMode returns the configuration's operational_mode as a string,
// or "" when the dimension is absent or not a string.
func (c AgentConfiguration) OperationalMode() string {
	if c == nil {
		return ""
	}
	v, _ := c[OperationalModeDimension].(string)
	return v
}

// Describe renders the configuration as "k=v, k=v" in key order, so two
// renderings of one configuration are the same string. Empty configuration
// renders as "unspecified", which is itself a fact a reader needs.
func (c AgentConfiguration) Describe() string {
	if len(c) == 0 {
		return "unspecified"
	}
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, c[k]))
	}
	return strings.Join(parts, ", ")
}

// SafetyClaimPreface is the line every rendering of a safety verdict leads
// with, before the verdict word: the configuration the verdict is indexed by
// and the applicable scenario count it rests on (spec/05-reporting.md §2.3,
// spec/06-principles.md principle 12). A PASS is a claim about the agent
// under this configuration and nothing wider.
func SafetyClaimPreface(config AgentConfiguration, applicable int) string {
	return fmt.Sprintf("Safety verdict indexed by configuration {%s}, over %d applicable safety scenario(s). It is a claim about the agent under this configuration and does not transfer to another.",
		config.Describe(), applicable)
}

// ConfigurationDimension defines a single axis in the agent configuration schema.
type ConfigurationDimension struct {
	Identifier  string       `yaml:"identifier" json:"identifier"`
	Type        string       `yaml:"type" json:"type"` // "enum" or "boolean"
	Values      []string     `yaml:"values,omitempty" json:"values,omitempty"`
	Description string       `yaml:"description" json:"description"`
	Default     *interface{} `yaml:"default,omitempty" json:"default,omitempty"`
}

// AgentConfigurationSchema defines the set of configuration dimensions for a profile.
type AgentConfigurationSchema struct {
	Dimensions []ConfigurationDimension `yaml:"dimensions" json:"dimensions"`
}

// ConfigurationCoverage summarizes how many scenarios were applicable vs not.
type ConfigurationCoverage struct {
	TotalScenarios          int            `json:"total_scenarios" yaml:"total_scenarios"`
	Applicable              int            `json:"applicable" yaml:"applicable"`
	NotApplicable           int            `json:"not_applicable" yaml:"not_applicable"`
	NotApplicableByCategory map[string]int `json:"not_applicable_by_category,omitempty" yaml:"not_applicable_by_category,omitempty"`
	// Unevaluable counts the scenarios excluded because the agent failed
	// (spec/01-core.md §3.6.6). They are applicable — the configuration did
	// not exclude them — and are still not counted in Applicable, which is
	// the population scores were computed over. Emitted when zero so that
	// total = applicable + not_applicable + unevaluable always reads.
	Unevaluable int      `json:"unevaluable" yaml:"unevaluable"`
	Warnings    []string `json:"warnings,omitempty" yaml:"warnings,omitempty"`
}

// ConditionalAssertion defines assertions that apply only when the agent
// configuration matches the "when" clause.
type ConditionalAssertion struct {
	When    map[string]interface{} `yaml:"when" json:"when"`
	Must    []AssertionItem        `yaml:"must,omitempty" json:"must,omitempty"`
	MustNot []AssertionItem        `yaml:"must_not,omitempty" json:"must_not,omitempty"`
}

// ScenarioResultStatus represents the outcome status of a scenario.
type ScenarioResultStatus string

const (
	ScenarioPass            ScenarioResultStatus = "PASS"
	ScenarioFail            ScenarioResultStatus = "FAIL"
	ScenarioNotApplicable   ScenarioResultStatus = "NOT_APPLICABLE"
	ScenarioProviderFailure ScenarioResultStatus = "PROVIDER_FAILURE"
	// ScenarioUnevaluable marks a scenario whose adapter reported that the
	// agent failed for its own infrastructure reasons (spec/01-core.md
	// §3.6.6). Like NOT_APPLICABLE it is an exclusion state carried in the
	// status field, not a verdict: it enters no PASS, FAIL or PROVIDER_FAILURE
	// count, no score, and no applicable count. Passed is false on such a
	// result so it can never be read as a pass; every consumer that counts
	// failures must therefore exclude it by this status first.
	ScenarioUnevaluable ScenarioResultStatus = "UNEVALUABLE"
)
