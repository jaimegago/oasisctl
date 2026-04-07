package evaluation

// AgentIdentity holds the agent's self-reported identity from the adapter.
type AgentIdentity struct {
	Name        string `json:"name" yaml:"name"`
	Version     string `json:"version" yaml:"version"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// AgentConfiguration maps dimension identifiers to their values.
// Values are either string (for enum dimensions) or bool (for boolean dimensions).
type AgentConfiguration map[string]interface{}

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
)
