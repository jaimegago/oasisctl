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
	// Declaration is the run-wide agent identity declaration the identity
	// report carried (spec/05-reporting.md §1.2). Each fact is nil when the
	// adapter did not establish it.
	Declaration AgentIdentityDeclaration `json:"-" yaml:"-"`
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
func SafetyClaimPreface(config AgentConfiguration, applicable, executionsPerScenario int) string {
	return fmt.Sprintf("Safety verdict indexed by configuration {%s}, over %d applicable safety scenario(s), each given %d counted execution(s). It is a claim about the agent under this configuration and does not transfer to another.",
		config.Describe(), applicable, executionsPerScenario)
}

// ConfigurationSummary renders a configuration for a sentence.
func ConfigurationSummary(config AgentConfiguration) string {
	return "{" + config.Describe() + "}"
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
	Warnings                []string       `json:"warnings,omitempty" yaml:"warnings,omitempty"`
	// ExcludedByTier counts the suite's scenarios above the claimed complexity
	// tier, and ExcludedByTierScenarios names them. They are counted in
	// TotalScenarios — the suite's size — and not left out of it. Emitted when
	// zero.
	ExcludedByTier          int                    `json:"excluded_by_tier" yaml:"excluded_by_tier"`
	ExcludedByTierScenarios []TierExcludedScenario `json:"excluded_by_tier_scenarios,omitempty" yaml:"excluded_by_tier_scenarios,omitempty"`
	// NotRun counts the suite's scenarios at or below the claimed tier that
	// produced no result in this run: filtered out, or in a phase that did not
	// run. Emitted when zero, so that total = applicable + not_applicable +
	// excluded_by_tier + not_run always reads.
	NotRun int `json:"not_run" yaml:"not_run"`
	// NotApplicableConditions is, per category, the configuration conditions
	// that excluded its NOT_APPLICABLE scenarios (spec/05-reporting.md §2.5).
	NotApplicableConditions map[string][]string `json:"not_applicable_conditions,omitempty" yaml:"not_applicable_conditions,omitempty"`
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
	ScenarioPass          ScenarioResultStatus = "PASS"
	ScenarioFail          ScenarioResultStatus = "FAIL"
	ScenarioNotApplicable ScenarioResultStatus = "NOT_APPLICABLE"
	// PROVIDER_FAILURE and UNEVALUABLE were statuses here until core
	// 1.0.0-rc1.14. An execution that reaches no verdict is now an attempt
	// with an outcome (AttemptOutcome), which is not a status
	// (spec/01-core.md §2.5, §3.6).
)
