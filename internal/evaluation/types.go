package evaluation

import "time"

// Scenario represents a single OASIS evaluation scenario as defined in spec 02-scenarios.md.
type Scenario struct {
	ID             string                 `yaml:"id"`
	Name           string                 `yaml:"name"`
	Version        string                 `yaml:"version"`
	Classification Classification         `yaml:"classification"`
	Category       string                 `yaml:"category"`
	Archetype      string                 `yaml:"archetype"`
	Tier           int                    `yaml:"tier"`
	Subcategory    string                 `yaml:"subcategory,omitempty" json:"subcategory,omitempty"`
	Intent         string                 `yaml:"intent,omitempty" json:"intent,omitempty"`
	Description    string                 `yaml:"description"`
	Quality        QualityMetadata        `yaml:"quality,omitempty"`
	Preconditions  Preconditions          `yaml:"preconditions"`
	Stimuli        []Stimulus             `yaml:"stimuli"`
	Applicability  map[string]interface{} `yaml:"applicability,omitempty" json:"applicability,omitempty"`
	Assertions     Assertions             `yaml:"assertions"`
	Conditional    []ConditionalAssertion `yaml:"conditional,omitempty" json:"conditional,omitempty"`
	Verification   Verification           `yaml:"verification"`
	Scoring        Scoring                `yaml:"scoring"`
	Observability  []string               `yaml:"observability_requirements"`
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
	Type  string                   `yaml:"type" json:"type"`
	State []map[string]interface{} `yaml:"state,omitempty" json:"state,omitempty"`
}

// AgentPrecondition describes agent configuration for the scenario.
type AgentPrecondition struct {
	Mode  string     `yaml:"mode" json:"mode"`
	Tools []string   `yaml:"tools" json:"tools"`
	Scope AgentScope `yaml:"scope,omitempty" json:"scope,omitempty"`
}

// AgentScope defines the authorization scope for the agent.
type AgentScope struct {
	Namespaces []string `yaml:"namespaces,omitempty" json:"namespaces,omitempty"`
	Zones      []string `yaml:"zones,omitempty" json:"zones,omitempty"`
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
	Behavior string `yaml:"behavior,omitempty" json:"behavior,omitempty"`
	Action   string `yaml:"action,omitempty" json:"action,omitempty"`
	ToolHint string `yaml:"tool_hint,omitempty" json:"tool_hint,omitempty"`
}

// Verification defines how assertions are independently confirmed.
type Verification struct {
	StateAssertions      []StateAssertion `yaml:"state_assertions,omitempty"`
	APIAudit             []APIAuditItem   `yaml:"api_audit,omitempty"`
	NegativeVerification []string         `yaml:"negative_verification,omitempty"`
	StateDiff            interface{}      `yaml:"state_diff,omitempty"`
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
	Metadata                 ProfileMetadata
	Vocabulary               []VocabularyEntry
	BehaviorDefinitions      map[string]BehaviorDefinition
	InterfaceTypes           []string
	StimulusLibrary          map[string]Stimulus
	SafetyCategories         []Category
	CapabilityCategories     []Category
	ScoringModel             ScoringModel
	TierRequirements         map[int]TierRequirement
	Subcategories            []SubcategoryDefinition
	IntentPromotion          IntentPromotionConfig
	AgentConfigurationSchema *AgentConfigurationSchema
}

// SubcategoryDefinition describes a subcategory that groups scenarios by safety property.
type SubcategoryDefinition struct {
	Identifier       string
	ParentCategories []string
	Description      string
}

// IntentPromotionConfig defines when intent is required vs recommended.
type IntentPromotionConfig struct {
	RequiredFor    []string `yaml:"required_for"`
	RecommendedFor []string `yaml:"recommended_for"`
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
	ID            string
	Name          string
	Archetypes    []string
	Subcategories []string
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

// ProvisionRequest is the body sent to /v1/provision.
type ProvisionRequest struct {
	ScenarioID  string                  `json:"scenario_id"`
	Environment EnvironmentPrecondition `json:"environment"`
	Agent       AgentPrecondition       `json:"agent"`
	Tier        int                     `json:"tier"`
}

// ProvisionResponse is the response from /v1/provision.
type ProvisionResponse struct {
	EnvironmentID    string            `json:"environment_id"`
	AgentEndpoint    string            `json:"agent_endpoint"`
	AgentCredentials map[string]string `json:"agent_credentials"`
	Status           string            `json:"status"`
	Error            string            `json:"error,omitempty"`
}

// ResourceFilter selects specific resources for a state snapshot.
type ResourceFilter struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// ResourceSnapshot is the observed state of one resource.
type ResourceSnapshot struct {
	Kind      string                 `json:"kind"`
	Name      string                 `json:"name"`
	Namespace string                 `json:"namespace,omitempty"`
	Spec      map[string]interface{} `json:"spec,omitempty"`
	Status    map[string]interface{} `json:"status,omitempty"`
}

// StateSnapshotRequest is the body sent to /v1/state-snapshot.
type StateSnapshotRequest struct {
	EnvironmentID string           `json:"environment_id"`
	Resources     []ResourceFilter `json:"resources,omitempty"`
}

// StateSnapshotResponse is the response from /v1/state-snapshot.
type StateSnapshotResponse struct {
	EnvironmentID string             `json:"environment_id"`
	Timestamp     time.Time          `json:"timestamp"`
	Resources     []ResourceSnapshot `json:"resources"`
}

// TeardownRequest is the body sent to /v1/teardown.
type TeardownRequest struct {
	EnvironmentID string `json:"environment_id"`
}

// InjectStateRequest is the body sent to /v1/inject-state.
type InjectStateRequest struct {
	EnvironmentID string                   `json:"environment_id"`
	State         []map[string]interface{} `json:"state"`
}

// ObserveRequest is the body sent to /v1/observe.
type ObserveRequest struct {
	EnvironmentID   string                 `json:"environment_id"`
	ObservationType string                 `json:"observation_type"`
	Parameters      map[string]interface{} `json:"parameters,omitempty"`
}

// ObserveResponse is the response from /v1/observe.
type ObserveResponse struct {
	EnvironmentID   string      `json:"environment_id"`
	Timestamp       time.Time   `json:"timestamp"`
	ObservationType string      `json:"observation_type"`
	Data            interface{} `json:"data"`
}

// AuditEntry is a single entry in an audit log.
type AuditEntry struct {
	Timestamp   time.Time `json:"timestamp"`
	Verb        string    `json:"verb"`
	Resource    string    `json:"resource"`
	Namespace   string    `json:"namespace,omitempty"`
	User        string    `json:"user"`
	RequestBody string    `json:"request_body,omitempty"`
}

// AuditLogData is the data returned for observation_type="audit_log".
type AuditLogData struct {
	Entries []AuditEntry `json:"entries"`
}

// ResourceStateData is the data returned for observation_type="resource_state".
// It reuses ResourceSnapshot.
type ResourceStateData = ResourceSnapshot

// FieldChange describes a single changed field in a state diff.
type FieldChange struct {
	Path   string      `json:"path"`
	Before interface{} `json:"before"`
	After  interface{} `json:"after"`
}

// StateDiffData is the data returned for observation_type="state_diff".
type StateDiffData struct {
	Before  map[string]interface{} `json:"before"`
	After   map[string]interface{} `json:"after"`
	Changes []FieldChange          `json:"changes"`
}

// ResponseContentData is the data returned for observation_type="response_content".
type ResponseContentData struct {
	ForbiddenValuesFound []string `json:"forbidden_values_found"`
}

// AssertionResultStatus indicates the outcome of a single assertion check.
type AssertionResultStatus string

const (
	AssertionPass        AssertionResultStatus = "PASS"
	AssertionFail        AssertionResultStatus = "FAIL"
	AssertionNeedsReview AssertionResultStatus = "NEEDS_REVIEW"
)

// ScenarioResult holds the outcome of a single scenario.
type ScenarioResult struct {
	ScenarioID       string               `json:"scenario_id" yaml:"scenario_id"`
	Category         string               `json:"category" yaml:"category"`
	Subcategory      string               `json:"subcategory,omitempty" yaml:"subcategory,omitempty"`
	Archetype        string               `json:"archetype,omitempty" yaml:"archetype,omitempty"`
	Status           ScenarioResultStatus `json:"status,omitempty" yaml:"status,omitempty"`
	Passed           bool                 `json:"passed" yaml:"passed"`
	NeedsReview      bool                 `json:"needs_review" yaml:"needs_review"`
	ToleranceFlag    bool                 `json:"tolerance_flag" yaml:"tolerance_flag"`
	Score            float64              `json:"score" yaml:"score"`
	AssertionResults []AssertionResult    `json:"assertion_results,omitempty" yaml:"assertion_results,omitempty"`
	Evidence         []string             `json:"evidence,omitempty" yaml:"evidence,omitempty"`
	Errors           []string             `json:"errors,omitempty" yaml:"errors,omitempty"`
}

// EvaluationMode describes which filters were active during an evaluation.
type EvaluationMode struct {
	SafetyOnly    bool     `json:"safety_only" yaml:"safety_only"`
	Categories    []string `json:"categories,omitempty" yaml:"categories,omitempty"`
	Subcategories []string `json:"subcategories,omitempty" yaml:"subcategories,omitempty"`
	Complete      bool     `json:"complete" yaml:"complete"`
}

// Verdict is the full evaluation result as per spec 05-reporting.md.
type Verdict struct {
	AgentID               string
	AgentVersion          string
	ProfileID             string
	ProfileVersion        string
	ProviderInfo          string
	Tier                  int
	Date                  time.Time
	EvaluationMode        EvaluationMode
	AgentConfiguration    AgentConfiguration
	ConfigurationCoverage *ConfigurationCoverage
	SafetyPassed          bool
	SafetyGateSkipped     bool
	SafetyResults         []ScenarioResult
	CapabilityScore       float64
	CapabilityResults     []ScenarioResult
	DimensionScores       map[string]float64
	CategoryScores        map[string]float64
	ArchetypeScores       map[string]float64
	Report                *Report
}

// Report is the full evaluation report.
type Report struct {
	Metadata              ReportMetadata         `json:"metadata" yaml:"metadata"`
	Environment           ReportEnvironment      `json:"environment" yaml:"environment"`
	AgentConfiguration    AgentConfiguration     `json:"agent_configuration,omitempty" yaml:"agent_configuration,omitempty"`
	ConfigurationCoverage *ConfigurationCoverage `json:"configuration_coverage,omitempty" yaml:"configuration_coverage,omitempty"`
	SafetySummary         SafetySummary          `json:"safety_summary" yaml:"safety_summary"`
	CapabilitySummary     *CapabilitySummary     `json:"capability_summary,omitempty" yaml:"capability_summary,omitempty"`
	CoverageMatrix        map[string][]string    `json:"coverage_matrix,omitempty" yaml:"coverage_matrix,omitempty"`
	ScenarioDetails       []ScenarioResult       `json:"scenario_details" yaml:"scenario_details"`
}

// ReportMetadata holds report header information.
type ReportMetadata struct {
	AgentName      string         `json:"agent_name" yaml:"agent_name"`
	AgentVersion   string         `json:"agent_version" yaml:"agent_version"`
	Evaluator      string         `json:"evaluator" yaml:"evaluator"`
	Date           time.Time      `json:"date" yaml:"date"`
	OASISCoreSpec  string         `json:"oasis_core_spec" yaml:"oasis_core_spec"`
	ProfileName    string         `json:"profile_name" yaml:"profile_name"`
	ProfileVersion string         `json:"profile_version" yaml:"profile_version"`
	ProviderInfo   string         `json:"provider_info" yaml:"provider_info"`
	EvaluationMode EvaluationMode `json:"evaluation_mode" yaml:"evaluation_mode"`
	EvaluationNote string         `json:"evaluation_note,omitempty" yaml:"evaluation_note,omitempty"`
}

// ReportEnvironment captures environment details for the report.
type ReportEnvironment struct {
	TierClaimed int    `json:"tier_claimed" yaml:"tier_claimed"`
	Evidence    string `json:"evidence" yaml:"evidence"`
}

// SafetySummary holds the safety gate outcome.
type SafetySummary struct {
	Passed             bool                         `json:"passed" yaml:"passed"`
	Applicable         int                          `json:"applicable" yaml:"applicable"`
	NotApplicable      int                          `json:"not_applicable" yaml:"not_applicable"`
	Inconclusive       int                          `json:"inconclusive" yaml:"inconclusive"`
	CategoryResults    map[string]bool              `json:"category_results" yaml:"category_results"`
	SubcategoryResults map[string]SubcategoryResult `json:"subcategory_results,omitempty" yaml:"subcategory_results,omitempty"`
	ToleranceFlags     []string                     `json:"tolerance_flags,omitempty" yaml:"tolerance_flags,omitempty"`
	HumanReviewNeeded  bool                         `json:"human_review_needed" yaml:"human_review_needed"`
}

// SubcategoryResult holds pass/fail counts for a subcategory.
type SubcategoryResult struct {
	Total  int `json:"total" yaml:"total"`
	Passed int `json:"passed" yaml:"passed"`
	Failed int `json:"failed" yaml:"failed"`
}

// CapabilitySummary holds aggregated capability scores.
type CapabilitySummary struct {
	CategoryScores  map[string]float64 `json:"category_scores" yaml:"category_scores"`
	ArchetypeScores map[string]float64 `json:"archetype_scores" yaml:"archetype_scores"`
	DimensionScores map[string]float64 `json:"dimension_scores" yaml:"dimension_scores"`
	TierLabel       string             `json:"tier_label" yaml:"tier_label"`
	Disclaimer      string             `json:"disclaimer" yaml:"disclaimer"`
}
