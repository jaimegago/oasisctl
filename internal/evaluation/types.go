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
	Injection      *Injection             `yaml:"injection,omitempty" json:"injection,omitempty"`
	Verification   Verification           `yaml:"verification"`
	Scoring        Scoring                `yaml:"scoring"`
	Observability  []string               `yaml:"observability_requirements"`
}

// Injection is the scenario's injection manifest per spec/02-scenarios.md §1.11.
// It declares the healthy baseline, the deviations applied to it, and the symptom
// those deviations produce. A Form B scoring binding derives its answer key from
// this block rather than restating it.
type Injection struct {
	Baseline       []map[string]interface{} `yaml:"baseline" json:"baseline"`
	Deviations     []Deviation              `yaml:"deviations" json:"deviations"`
	Symptom        Symptom                  `yaml:"symptom" json:"symptom"`
	Counterfactual string                   `yaml:"counterfactual" json:"counterfactual"`
}

// Deviation is a single injected departure from the declared baseline.
// Scoring bindings reference it by ID.
type Deviation struct {
	ID            string `yaml:"id" json:"id"`
	Resource      string `yaml:"resource" json:"resource"`
	DeviationType string `yaml:"deviation_type" json:"deviation_type"`
	Subject       string `yaml:"subject" json:"subject"`
}

// Symptom is the observable condition the deviations produce.
type Symptom struct {
	Resource  string `yaml:"resource" json:"resource"`
	Condition string `yaml:"condition" json:"condition"`
}

// FindDeviation returns the deviation with the given ID, or nil.
func (i *Injection) FindDeviation(id string) *Deviation {
	if i == nil {
		return nil
	}
	for idx := range i.Deviations {
		if i.Deviations[idx].ID == id {
			return &i.Deviations[idx]
		}
	}
	return nil
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
	StateAssertions      []StateAssertion        `yaml:"state_assertions,omitempty"`
	APIAudit             []APIAuditItem          `yaml:"api_audit,omitempty"`
	NegativeVerification []string                `yaml:"negative_verification,omitempty"`
	StateDiff            interface{}             `yaml:"state_diff,omitempty"`
	ValueContainment     []ValueContainmentEntry `yaml:"value_containment,omitempty" json:"value_containment,omitempty"`
}

// ValueContainmentScope identifies whether a value containment assertion is
// absolute (value must never appear) or contextual (value must not appear
// when the agent operates outside the value's declared boundary).
type ValueContainmentScope string

const (
	// ValueContainmentAbsolute — value must never appear in any declared output channel.
	ValueContainmentAbsolute ValueContainmentScope = "absolute"
	// ValueContainmentContextual — value must not appear when the agent operates
	// outside the boundary of the precondition resource that sourced it.
	ValueContainmentContextual ValueContainmentScope = "contextual"
)

// ValueContainmentEntry is a single value containment assertion. The ValueRef
// is a dotted path resolvable against scenario.preconditions; OutputChannels
// enumerate the channels the matcher scans (agent_response, tool_call_parameters,
// reasoning_trace).
type ValueContainmentEntry struct {
	ValueRef       string                `yaml:"value_ref" json:"value_ref"`
	Scope          ValueContainmentScope `yaml:"scope" json:"scope"`
	OutputChannels []string              `yaml:"output_channels" json:"output_channels"`
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

// Scoring defines how the scenario is scored. Capability scenarios take one of
// two mutually exclusive forms per spec/02-scenarios.md §1.7: Form A declares
// type/rubric/dimensions, Form B declares archetype_template plus the binding
// parameters the referenced band template requires.
type Scoring struct {
	Type            ScoringType            `yaml:"type"`
	SafetyTolerance int                    `yaml:"safety_tolerance,omitempty"`
	PassCondition   string                 `yaml:"pass_condition,omitempty"`
	Rubric          map[string]interface{} `yaml:"rubric,omitempty"`
	Dimensions      map[string]interface{} `yaml:"dimensions,omitempty"`

	// Form B binding. ArchetypeTemplate names a band template in the owning
	// profile's scoring-decomposition document; the remaining fields bind that
	// template's declared roles. The permitted key set is profile-owned, so
	// these fields track the SI profile's registry v1 role vocabulary.
	ArchetypeTemplate string         `yaml:"archetype_template,omitempty" json:"archetype_template,omitempty"`
	StepBudget        int            `yaml:"step_budget,omitempty" json:"step_budget,omitempty"`
	Channels          []string       `yaml:"channels,omitempty" json:"channels,omitempty"`
	Factor            *ScoringFactor `yaml:"factor,omitempty" json:"factor,omitempty"`
	SubsystemSet      []string       `yaml:"subsystem_set,omitempty" json:"subsystem_set,omitempty"`
	ExcludeToolEcho   bool           `yaml:"exclude_tool_echo,omitempty" json:"exclude_tool_echo,omitempty"`
}

// ScoringFactor binds a contributing factor to a deviation in the scenario's
// injection manifest. Ref is a deviation ID; the deviation type is read from
// that deviation, never restated here.
type ScoringFactor struct {
	Ref                 string   `yaml:"ref" json:"ref"`
	RequiredIdentifiers []string `yaml:"required_identifiers" json:"required_identifiers"`
}

// IsFormB reports whether the scoring block is a Form B binding.
func (s Scoring) IsFormB() bool { return s.ArchetypeTemplate != "" }

// IsFormA reports whether the scoring block is a Form A rubric. A capability
// scenario declaring neither form, or both, is malformed.
func (s Scoring) IsFormA() bool {
	return s.Type != "" || len(s.Rubric) > 0 || len(s.Dimensions) > 0
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
	Metadata                        ProfileMetadata
	Vocabulary                      []VocabularyEntry
	BehaviorDefinitions             map[string]BehaviorDefinition
	InterfaceTypes                  []string
	StimulusLibrary                 map[string]Stimulus
	SafetyCategories                []Category
	CapabilityCategories            []Category
	ScoringModel                    ScoringModel
	TierRequirements                map[int]TierRequirement
	Subcategories                   []SubcategoryDefinition
	IntentPromotion                 IntentPromotionConfig
	AgentConfigurationSchema        *AgentConfigurationSchema
	ProviderConformanceRequirements *ProviderConformanceRequirements
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

	// The four fields below are declared by capability categories only. Safety
	// categories hold no score to aggregate — safety is binary pass/fail at the
	// scenario level — so they leave all four zero.

	// MapsToDimensions is the category's declared core dimension mapping. It is
	// reported per category, and it is a separate declaration from
	// DimensionWeights: a dimension may be mapped without carrying a weight.
	MapsToDimensions []string
	// DimensionWeights is the weight this category contributes to each core
	// dimension, keyed by dimension identifier.
	DimensionWeights map[string]float64
	// Aggregation is how archetype scores roll up into this category's score.
	Aggregation AggregationMethod
	// ArchetypeWeights carries only the archetypes whose weight differs from
	// DefaultArchetypeWeight. Empty under AggregationMinimum, which uses none.
	ArchetypeWeights map[string]float64
}

// AggregationMethod names how a category's archetype scores roll up into its
// category score.
type AggregationMethod string

const (
	// AggregationWeightedAverage is the sum of archetype scores times their
	// weights, divided by the sum of those weights.
	AggregationWeightedAverage AggregationMethod = "weighted_average"
	// AggregationMinimum is the lowest archetype score in the category.
	AggregationMinimum AggregationMethod = "minimum"
)

// DefaultArchetypeWeight is the weight of an archetype that a category's
// ArchetypeWeights does not mention.
const DefaultArchetypeWeight = 1.0

// CategoryScore is a capability category's aggregated score together with the
// coverage count and dimension mapping that spec/05-reporting.md §1 requires
// beside it.
type CategoryScore struct {
	Score               float64  `json:"score" yaml:"score"`
	ArchetypesEvaluated int      `json:"archetypes_evaluated" yaml:"archetypes_evaluated"`
	MapsToDimensions    []string `json:"maps_to_dimensions" yaml:"maps_to_dimensions"`
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
	EnvironmentID   string          `json:"environment_id"`
	Timestamp       time.Time       `json:"timestamp"`
	ObservationType string          `json:"observation_type"`
	Data            interface{}     `json:"data"`
	EvidenceSource  *EvidenceSource `json:"evidence_source,omitempty"`
}

// EvidenceSource describes the provenance and availability of observation evidence
// per spec/01-core.md §3.7.
type EvidenceSource struct {
	Type   string `json:"type"`   // e.g. "audit_log", "resource_state"
	Status string `json:"status"` // "available", "unreachable", "degraded"
}

// AuditEntry is a single entry in an audit log.
//
// Name and Subresource are read when the provider carries them and are absent
// otherwise. The SI provider guide § 4.5 declares the entry shape as timestamp,
// verb, resource, namespace, user and request_body — it names no object
// identity, which is why an SI action of the form `verb resource/name` could
// not be checked against it at all. Both fields are optional here rather than
// required: a provider that predates them still loads, and an assertion whose
// action names an object it cannot see is marked vacuous rather than silently
// passing. See joe-pm queue/action-vocabulary-unmatchable-in-audit.md.
type AuditEntry struct {
	Timestamp   time.Time `json:"timestamp"`
	Verb        string    `json:"verb"`
	Resource    string    `json:"resource"`
	Subresource string    `json:"subresource,omitempty"`
	Name        string    `json:"name,omitempty"`
	Namespace   string    `json:"namespace,omitempty"`
	User        string    `json:"user"`
	RequestBody string    `json:"request_body,omitempty"`
}

// AuditLogData is the data returned for observation_type="audit_log".
//
// The observation is annotated, never narrowed: Entries carries every entry the
// provider captured, from every principal on the cluster, and AgentPrincipal
// declares which of them is the agent. A provider that filtered instead would
// destroy the difference between "the log was empty" and "the agent did
// nothing", and no consumer past that boundary could recover it.
type AuditLogData struct {
	Entries []AuditEntry `json:"entries"`

	// AgentPrincipal is the identity the agent authenticates to the environment
	// as — for Kubernetes, the audit event's user.username, e.g.
	// "system:serviceaccount:default:joe-oasis-e2e". Empty means no party
	// declared it, which is a vacuity cause and never a licence to match
	// unattributed entries.
	//
	// It is not derived from anything the agent reported about itself: the
	// principal is declared by the harness that mints the credential, and the
	// entries are attributed by the API server. Independent verification
	// (spec/01-core.md §3.4) constrains the provenance of the evidence, not its
	// scope, so selecting the agent's own entries from an independently
	// captured log preserves it intact.
	AgentPrincipal string `json:"agent_principal,omitempty"`

	// scopeReason is set on the narrowed view returned by agentScopedAuditLog
	// and reports why that view is unusable, or "" when it is sound. It is
	// unexported and unserialized: it describes a derived value, not anything
	// the provider sent.
	scopeReason VacuityReason

	// full points at the unnarrowed log a scoped view was derived from, and is
	// nil on the unnarrowed log itself. It is what makes "annotate, never
	// narrow" true in the evaluator and not only at the provider boundary: the
	// full evidence remains reachable from the scoped value.
	full *AuditLogData
}

// AgentScoped returns a view of the log holding only entries attributed to
// AgentPrincipal, together with the reason that view cannot be trusted when it
// cannot be built. Callers in the execution package use the wrapper there.
func (a *AuditLogData) AgentScoped() *AuditLogData {
	if a == nil {
		return nil
	}
	if a.AgentPrincipal == "" {
		return &AuditLogData{scopeReason: VacuityAgentPrincipalUnknown, full: a}
	}
	scoped := &AuditLogData{AgentPrincipal: a.AgentPrincipal, full: a}
	for _, e := range a.Entries {
		if e.User == a.AgentPrincipal {
			scoped.Entries = append(scoped.Entries, e)
		}
	}
	// Naming the right absence. A captured log that held other principals'
	// entries and none of the agent's is not an empty log, and reporting it as
	// one would say the environment was quiet when in fact it was busy and none
	// of it was the agent's. That distinction is the whole point of annotating
	// rather than narrowing, so it survives into the vacuity reason.
	//
	// An empty capture is left alone: scopeReason stays "" and the ordinary
	// empty-log reason applies, because there the outer absence IS the truth.
	if len(scoped.Entries) == 0 && len(a.Entries) > 0 {
		scoped.scopeReason = VacuityNoApplicableAuditEntries
	}
	return scoped
}

// ScopeReason reports why a scoped view had nothing usable to inspect, or "".
func (a *AuditLogData) ScopeReason() VacuityReason {
	if a == nil {
		return ""
	}
	return a.scopeReason
}

// Full returns the unnarrowed log a scoped view was derived from, or the
// receiver when it is already unnarrowed. A check that must see principals
// other than the agent calls this and records why it may.
func (a *AuditLogData) Full() *AuditLogData {
	if a == nil {
		return nil
	}
	if a.full != nil {
		return a.full
	}
	return a
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

// AssertionResultStatus indicates the outcome of a single assertion check.
type AssertionResultStatus string

const (
	AssertionPass            AssertionResultStatus = "PASS"
	AssertionFail            AssertionResultStatus = "FAIL"
	AssertionProviderFailure AssertionResultStatus = "PROVIDER_FAILURE"
)

// ScenarioResult holds the outcome of a single scenario.
type ScenarioResult struct {
	ScenarioID       string               `json:"scenario_id" yaml:"scenario_id"`
	Category         string               `json:"category" yaml:"category"`
	Subcategory      string               `json:"subcategory,omitempty" yaml:"subcategory,omitempty"`
	Archetype        string               `json:"archetype,omitempty" yaml:"archetype,omitempty"`
	Status           ScenarioResultStatus `json:"status,omitempty" yaml:"status,omitempty"`
	Passed           bool                 `json:"passed" yaml:"passed"`
	ToleranceFlag    bool                 `json:"tolerance_flag" yaml:"tolerance_flag"`
	Score            float64              `json:"score" yaml:"score"`
	AssertionResults []AssertionResult    `json:"assertion_results,omitempty" yaml:"assertion_results,omitempty"`
	Evidence         []string             `json:"evidence,omitempty" yaml:"evidence,omitempty"`
	Errors           []string             `json:"errors,omitempty" yaml:"errors,omitempty"`
	// Band is the band label selected by the archetype band template. Empty for
	// Form A scenarios, which have no band vocabulary.
	Band string `json:"band,omitempty" yaml:"band,omitempty"`
	// EvidencePath is the evidence artifact's path relative to the run output
	// directory, per spec/05-reporting.md §1.2.
	EvidencePath string `json:"evidence_path,omitempty" yaml:"evidence_path,omitempty"`
}

// EvaluationMode describes which filters were active during an evaluation.
type EvaluationMode struct {
	SafetyOnly    bool     `json:"safety_only" yaml:"safety_only"`
	Categories    []string `json:"categories,omitempty" yaml:"categories,omitempty"`
	Subcategories []string `json:"subcategories,omitempty" yaml:"subcategories,omitempty"`
	Complete      bool     `json:"complete" yaml:"complete"`
}

// SafetyVerdict represents the top-level safety outcome per spec/01-core.md §3.6.
type SafetyVerdict string

const (
	SafetyVerdictPass            SafetyVerdict = "PASS"
	SafetyVerdictFail            SafetyVerdict = "FAIL"
	SafetyVerdictProviderFailure SafetyVerdict = "PROVIDER_FAILURE"
)

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
	Safety                SafetyVerdict
	SafetyPassed          bool
	SafetyGateSkipped     bool
	SafetyResults         []ScenarioResult
	CapabilityScore       float64
	CapabilityResults     []ScenarioResult
	DimensionScores       map[string]float64
	CategoryScores        map[string]CategoryScore
	ArchetypeScores       map[string]float64
	OASISCoreSpec         string
	Report                *Report
	Aborted               bool
	AbortReason           string
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
	Aborted        bool           `json:"aborted,omitempty" yaml:"aborted,omitempty"`
	AbortReason    string         `json:"abort_reason,omitempty" yaml:"abort_reason,omitempty"`
}

// ReportEnvironment captures environment details for the report.
type ReportEnvironment struct {
	TierClaimed int    `json:"tier_claimed" yaml:"tier_claimed"`
	Evidence    string `json:"evidence" yaml:"evidence"`
}

// SafetySummary holds the safety gate outcome.
type SafetySummary struct {
	Safety             SafetyVerdict                `json:"safety" yaml:"safety"`
	Passed             bool                         `json:"passed" yaml:"passed"`
	Applicable         int                          `json:"applicable" yaml:"applicable"`
	NotApplicable      int                          `json:"not_applicable" yaml:"not_applicable"`
	ProviderFailures   int                          `json:"provider_failures" yaml:"provider_failures"`
	Failed             int                          `json:"failed" yaml:"failed"`
	PassedCount        int                          `json:"passed_count" yaml:"passed_count"`
	CategoryResults    map[string]bool              `json:"category_results" yaml:"category_results"`
	SubcategoryResults map[string]SubcategoryResult `json:"subcategory_results,omitempty" yaml:"subcategory_results,omitempty"`
	ProviderFailureIDs []string                     `json:"provider_failure_ids,omitempty" yaml:"provider_failure_ids,omitempty"`
	FailureIDs         []string                     `json:"failure_ids,omitempty" yaml:"failure_ids,omitempty"`
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
//
// DomainCategories and CoreDimensions carry the names and shapes
// spec/05-reporting.md §1 gives them, not the names the aggregation functions
// happen to use internally. ArchetypeScores has no counterpart in that section
// and keeps its own name; it is reported because §2.5 asks for the archetype
// breakdown behind a category score.
type CapabilitySummary struct {
	DomainCategories map[string]CategoryScore `json:"domain_categories" yaml:"domain_categories"`
	CoreDimensions   map[string]float64       `json:"core_dimensions" yaml:"core_dimensions"`
	ArchetypeScores  map[string]float64       `json:"archetype_scores" yaml:"archetype_scores"`
	TierLabel        string                   `json:"tier_label" yaml:"tier_label"`
	Disclaimer       string                   `json:"disclaimer" yaml:"disclaimer"`
}

// ProviderConformanceRequirements defines what a profile requires from the
// environment provider, per spec/08-provider-conformance.md §3.8.
type ProviderConformanceRequirements struct {
	EnvironmentType          string   `yaml:"environment_type" json:"environment_type"`
	MinComplexityTier        int      `yaml:"min_complexity_tier" json:"min_complexity_tier"`
	OASISCoreSpecVersion     string   `yaml:"oasis_core_spec_version" json:"oasis_core_spec_version"`
	EvidenceSourcesRequired  []string `yaml:"evidence_sources_required" json:"evidence_sources_required"`
	StateInjection           bool     `yaml:"state_injection" json:"state_injection"`
	AuditPolicyInstallation  bool     `yaml:"audit_policy_installation" json:"audit_policy_installation"`
	NetworkPolicyEnforcement bool     `yaml:"network_policy_enforcement" json:"network_policy_enforcement"`
	ValueContainmentSupport  bool     `yaml:"value_containment_support" json:"value_containment_support"`
}

// ConformanceRequest is the query sent to GET /v1/conformance.
type ConformanceRequest struct {
	ProfileID string `json:"profile"`
}

// ConformanceRequirements holds the nested requirements object within
// a conformance response, per spec §3.8.2.
type ConformanceRequirements struct {
	EnvironmentType          string   `json:"environment_type"`
	ComplexityTierSupported  int      `json:"complexity_tier_supported"`
	OASISCoreSpecVersion     []string `json:"oasis_core_spec_version"`
	EvidenceSourcesAvailable []string `json:"evidence_sources_available"`
	StateInjection           bool     `json:"state_injection"`
	AuditPolicyInstallation  bool     `json:"audit_policy_installation"`
	NetworkPolicyEnforcement bool     `json:"network_policy_enforcement"`
	ValueContainmentSupport  bool     `json:"value_containment_support"`
}

// ConformanceResponse is the response from GET /v1/conformance per spec §3.8.2.
type ConformanceResponse struct {
	Provider              string                  `json:"provider"`
	ProviderVersion       string                  `json:"provider_version"`
	OASISCoreSpecVersions []string                `json:"oasis_core_spec_versions"`
	Profile               string                  `json:"profile"`
	ProfileVersion        string                  `json:"profile_version"`
	Supported             bool                    `json:"supported"`
	Requirements          ConformanceRequirements `json:"requirements"`
	UnmetRequirements     []UnmetRequirement      `json:"unmet_requirements,omitempty"`
}

// UnmetRequirement describes a single requirement the provider cannot satisfy.
type UnmetRequirement struct {
	Requirement string `json:"requirement"`
	Reason      string `json:"reason"`
}

// ReportMetadataAbort holds abort information in the report metadata.
type ReportMetadataAbort struct {
	Aborted     bool   `json:"aborted" yaml:"aborted"`
	AbortReason string `json:"abort_reason,omitempty" yaml:"abort_reason,omitempty"`
}
