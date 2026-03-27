package evaluation

// Scenario represents a single OASIS evaluation scenario as defined in spec 02-scenarios.md.
type Scenario struct {
	ID             string         `yaml:"id"`
	Name           string         `yaml:"name"`
	Version        string         `yaml:"version"`
	Classification Classification `yaml:"classification"`
	Category       string         `yaml:"category"`
	Archetype      string         `yaml:"archetype"`
	Tier           int            `yaml:"tier"`
	Description    string         `yaml:"description"`
	Quality        QualityMetadata `yaml:"quality,omitempty"`
	Preconditions  Preconditions  `yaml:"preconditions"`
	Stimuli        []Stimulus     `yaml:"stimuli"`
	Assertions     Assertions     `yaml:"assertions"`
	Verification   Verification   `yaml:"verification"`
	Scoring        Scoring        `yaml:"scoring"`
	Observability  []string       `yaml:"observability_requirements"`
}

// QualityMetadata holds safety scenario quality fields.
type QualityMetadata struct {
	AttackSurface     string     `yaml:"attack_surface,omitempty"`
	Difficulty        Difficulty `yaml:"difficulty,omitempty"`
	CompanionScenario string     `yaml:"companion_scenario,omitempty"`
}

// Difficulty represents the three-axis difficulty rating.
type Difficulty struct {
	Plausibility        string `yaml:"plausibility,omitempty"`
	DetectionComplexity string `yaml:"detection_complexity,omitempty"`
	ContextualEmbedding string `yaml:"contextual_embedding,omitempty"`
}

// Preconditions defines what must be true before the scenario runs.
type Preconditions struct {
	Environment EnvironmentPrecondition `yaml:"environment"`
	Agent       AgentPrecondition       `yaml:"agent"`
}

// EnvironmentPrecondition describes the required environment state.
type EnvironmentPrecondition struct {
	Type  string                   `yaml:"type"`
	State []map[string]interface{} `yaml:"state,omitempty"`
}

// AgentPrecondition describes agent configuration for the scenario.
type AgentPrecondition struct {
	Mode  string     `yaml:"mode"`
	Tools []string   `yaml:"tools"`
	Scope AgentScope `yaml:"scope,omitempty"`
}

// AgentScope defines the authorization scope for the agent.
type AgentScope struct {
	Namespaces []string `yaml:"namespaces,omitempty"`
	Zones      []string `yaml:"zones,omitempty"`
}

// Stimulus is a single input to the agent.
type Stimulus struct {
	Type        StimulusType `yaml:"type"`
	Value       string       `yaml:"value,omitempty"`
	Description string       `yaml:"description,omitempty"`
	Target      string       `yaml:"target,omitempty"`
	Timing      string       `yaml:"timing,omitempty"`
	LibraryRef  string       `yaml:"library_ref,omitempty"`
}

// Assertions defines what the agent must and must not do.
type Assertions struct {
	Must    []AssertionItem `yaml:"must,omitempty"`
	MustNot []AssertionItem `yaml:"must_not,omitempty"`
}

// AssertionItem is a single must/must_not assertion.
type AssertionItem struct {
	Behavior string `yaml:"behavior,omitempty"`
	Action   string `yaml:"action,omitempty"`
	ToolHint string `yaml:"tool_hint,omitempty"`
}

// Verification defines how assertions are independently confirmed.
type Verification struct {
	StateAssertions      []StateAssertion       `yaml:"state_assertions,omitempty"`
	APIAudit             []APIAuditItem         `yaml:"api_audit,omitempty"`
	NegativeVerification []string               `yaml:"negative_verification,omitempty"`
	StateDiff            interface{}            `yaml:"state_diff,omitempty"`
}

// StateAssertion checks a resource's state after the scenario.
type StateAssertion struct {
	Resource    string                 `yaml:"resource,omitempty"`
	Namespace   string                 `yaml:"namespace,omitempty"`
	Condition   string                 `yaml:"condition,omitempty"`
	Replicas    int                    `yaml:"replicas,omitempty"`
	Description string                 `yaml:"description,omitempty"`
	Data        map[string]interface{} `yaml:"data,omitempty"`
}

// APIAuditItem checks the API audit log.
type APIAuditItem struct {
	Forbidden string `yaml:"forbidden,omitempty"`
	Required  string `yaml:"required,omitempty"`
}

// Scoring defines how the scenario is scored.
type Scoring struct {
	Type            ScoringType            `yaml:"type"`
	SafetyTolerance int                    `yaml:"safety_tolerance,omitempty"`
	PassCondition   string                 `yaml:"pass_condition,omitempty"`
	Rubric          map[string]interface{} `yaml:"rubric,omitempty"`
	Dimensions      map[string]interface{} `yaml:"dimensions,omitempty"`
}

// Suite is a collection of scenarios to run together.
type Suite struct {
	ID            string            `yaml:"id"`
	Name          string            `yaml:"name"`
	Version       string            `yaml:"version"`
	DomainProfile string            `yaml:"domain_profile"`
	Scenarios     []string          `yaml:"scenarios"`
	Environment   map[string]string `yaml:"environment,omitempty"`
}

// Profile is the full in-memory representation of a loaded domain profile.
type Profile struct {
	Metadata             ProfileMetadata
	Vocabulary           []VocabularyEntry
	BehaviorDefinitions  map[string]BehaviorDefinition
	InterfaceTypes       []string
	StimulusLibrary      map[string]Stimulus
	SafetyCategories     []Category
	CapabilityCategories []Category
	ScoringModel         ScoringModel
	TierRequirements     map[int]TierRequirement
}

// ProfileMetadata holds profile header information.
type ProfileMetadata struct {
	Name       string
	Identifier string
	Version    string
	Domain     string
	OASISCore  string
}

// VocabularyEntry is a domain-specific term definition.
type VocabularyEntry struct {
	Term        string
	Definition  string
	CoreConcept string
}

// BehaviorDefinition is a named behavior from behavior-definitions.md.
type BehaviorDefinition struct {
	Identifier         string
	Description        string
	VerificationMethod string
	Group              string
}

// Category represents a safety or capability category with its archetypes.
type Category struct {
	ID         string
	Name       string
	Archetypes []string
}

// ScoringModel defines how scores aggregate.
type ScoringModel struct {
	SafetyTolerance float64
	CoreDimensions  map[string]DimensionConfig
}

// DimensionConfig defines how a core dimension aggregates from categories.
type DimensionConfig struct {
	ContributingCategories map[string]float64
}

// TierRequirement specifies what an environment must provide at a given tier.
type TierRequirement struct {
	Tier               int
	Description        string
	RequiredInterfaces []string
	MinCoverage        map[string]int
}

// Verdict is the full evaluation result as per spec 05-reporting.md.
type Verdict struct {
	AgentID           string
	ProfileID         string
	Tier              int
	SafetyPassed      bool
	SafetyResults     []ScenarioResult
	CapabilityScore   float64
	CapabilityResults []ScenarioResult
	Report            *Report
}

// ScenarioResult holds the outcome of a single scenario.
type ScenarioResult struct {
	ScenarioID string
	Passed     bool
	Score      float64
	Evidence   []string
	Errors     []string
}

// Report is the full evaluation report.
type Report struct {
	Metadata          map[string]string
	Environment       string
	SafetySummary     string
	CapabilitySummary string
	CoverageMatrix    map[string][]string
	ScenarioDetails   []ScenarioResult
}
