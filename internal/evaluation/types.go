package evaluation

import (
	"fmt"
	"strings"
	"time"
)

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
	// MisleadingSignals is the SI profile's declaration of which signal a
	// scenario asserting identify_misleading_signal treats as misleading
	// (behavior-definitions.md § identify_misleading_signal).
	MisleadingSignals []MisleadingSignal `yaml:"misleading_signals,omitempty" json:"misleading_signals,omitempty"`
	Verification      Verification       `yaml:"verification"`
	Scoring           Scoring            `yaml:"scoring"`
	Observability     []string           `yaml:"observability_requirements"`
}

// MisleadingSignal is one declared misleading signal: a resource the scenario
// declares in preconditions.environment.state, in the same kind/name form.
type MisleadingSignal struct {
	Resource string `yaml:"resource" json:"resource"`
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

	// Principal is the identity the agent authenticates to the environment as,
	// as it appears in an audit record — for Kubernetes,
	// "system:serviceaccount:<namespace>:<name>".
	//
	// It carries no yaml tag on purpose. A scenario must not be able to declare
	// it: the principal is a property of the deployment running the evaluation,
	// not of the scenario being evaluated, and a corpus that named identities
	// would only be correct for the one harness it was written against. It is
	// supplied per run (Config.AgentPrincipal) and merged in here on the way to
	// the provider.
	Principal string `yaml:"-" json:"principal,omitempty"`
}

// AgentScope defines the authorization scope for the agent.
type AgentScope struct {
	Namespaces []string `yaml:"namespaces,omitempty" json:"namespaces,omitempty"`
	Zones      []string `yaml:"zones,omitempty" json:"zones,omitempty"`
}

// Stimulus is a single input to the agent.
//
// The JSON tags are the shape a temporal_condition stimulus takes on the
// /provision request, which carries it to the provider as the scenario
// declares it (SI provider-guide.md §4.1).
type Stimulus struct {
	Type        StimulusType `yaml:"type" json:"type"`
	Value       string       `yaml:"value,omitempty" json:"value,omitempty"`
	Description string       `yaml:"description,omitempty" json:"description,omitempty"`
	Target      string       `yaml:"target,omitempty" json:"target,omitempty"`
	Timing      string       `yaml:"timing,omitempty" json:"timing,omitempty"`
	LibraryRef  string       `yaml:"library_ref,omitempty" json:"library_ref,omitempty"`
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
	Metadata             ProfileMetadata
	Vocabulary           []VocabularyEntry
	BehaviorDefinitions  map[string]BehaviorDefinition
	InterfaceTypes       []string
	StimulusLibrary      map[string]Stimulus
	SafetyCategories     []Category
	CapabilityCategories []Category
	ScoringModel         ScoringModel
	// TierRequirements is the profile's minimum evaluation coverage per
	// complexity tier (spec/03-profiles.md §2.7), keyed by tier. A tier the
	// profile does not state a minimum for has no entry, and coverage for a
	// claim at that tier cannot be established.
	TierRequirements map[int]TierRequirement
	// Scenarios is the profile's whole scenario suite (spec/03-profiles.md
	// §2.8), whatever subset a run selects. Minimum coverage is computed
	// against it: which archetypes a configuration can run is a fact about the
	// profile, and a run's filters must not be able to change it.
	Scenarios                       []Scenario
	Subcategories                   []SubcategoryDefinition
	IntentPromotion                 IntentPromotionConfig
	AgentConfigurationSchema        *AgentConfigurationSchema
	ProviderConformanceRequirements *ProviderConformanceRequirements
	// ExecutionMinimums is the minimum execution counts the profile sets
	// (spec/03-profiles.md §2.18). A zero Capability is a profile that states
	// none this loader could read, and an evaluation under it is not opened.
	ExecutionMinimums ExecutionMinimums
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
	// ArchetypesDeclared is how many archetypes the category itself declares.
	// ArchetypesEvaluated alone cannot answer the question a reader comparing
	// two runs actually has — 3 of what? — and a coverage count whose
	// denominator lives in a different file is a count nobody checks.
	ArchetypesDeclared int `json:"archetypes_declared" yaml:"archetypes_declared"`
	// Comparable is false when this score was computed over fewer archetypes
	// than the category declares. Such a score is NOT comparable to one
	// computed over all of them, and saying so here is the point: the
	// alternative is a reader noticing, and readers do not notice.
	//
	// Two figures produced from different archetype sets differ for a reason
	// that is not the agent, which is the same class of defect as a scenario
	// voided by a transient fault moving a headline number. Serialized without
	// omitempty so an explicit `true` is a positive claim of full coverage.
	Comparable bool `json:"comparable" yaml:"comparable"`
	// UnscoredArchetypes names the declared archetypes this score was NOT
	// computed over, in the category's declaration order — those never run,
	// and those whose scenarios were all unassessable. It is what turns
	// `comparable: false` from a flag into something actionable.
	UnscoredArchetypes []string `json:"unscored_archetypes,omitempty" yaml:"unscored_archetypes,omitempty"`
	// IncomparableArchetypes names the scored archetypes whose own score rests
	// on at least one scenario evaluated over fewer behaviours than it
	// declared. It is the second reason Comparable can be false, and it is
	// named separately because the two are different facts: an archetype in
	// UnscoredArchetypes contributed nothing, while one in here contributed a
	// number computed over a different population.
	//
	// Without the split, a category scored over every archetype it declares
	// would report comparable: false beside an empty UnscoredArchetypes, and a
	// reader would have no way to tell which coverage failed.
	IncomparableArchetypes []string `json:"incomparable_archetypes,omitempty" yaml:"incomparable_archetypes,omitempty"`
	// Aggregation and ArchetypeBreakdown are the archetype breakdown behind
	// the score (spec/05-reporting.md §2.4): how the category aggregates, and
	// each evaluated archetype's score and weight, in declaration order.
	Aggregation        AggregationMethod       `json:"aggregation,omitempty" yaml:"aggregation,omitempty"`
	ArchetypeBreakdown []ArchetypeContribution `json:"archetype_breakdown,omitempty" yaml:"archetype_breakdown,omitempty"`
	// Range is the lowest and the highest of the per-set category scores
	// Score is the mean of (spec/01-core.md §4.3). Nil on a score computed
	// from one set of executions made outside any evaluation.
	Range *ScoreRange `json:"range,omitempty" yaml:"range,omitempty"`
}

// ArchetypeContribution is one evaluated archetype's part in a category
// score. Weight is 1 under minimum aggregation, where no weight applies.
type ArchetypeContribution struct {
	Archetype string  `json:"archetype" yaml:"archetype"`
	Score     float64 `json:"score" yaml:"score"`
	Weight    float64 `json:"weight" yaml:"weight"`
	// Range is the range of the archetype's per-set scores.
	Range *ScoreRange `json:"range,omitempty" yaml:"range,omitempty"`
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

// TierRequirement is a complexity tier's minimum evaluation coverage, as the
// profile states it (spec/03-profiles.md §2.7, spec/01-core.md §5).
//
// It carries coverage only. What an environment must provide at the tier is
// verified by the provider, which returns TierEvidence; the evaluator checks
// that evidence and does not re-derive it.
type TierRequirement struct {
	Tier int
	// Safety and Capability are the minimum number of archetypes per category
	// for each classification.
	Safety     CoverageMinimum
	Capability CoverageMinimum
	// Uncomputed holds, verbatim, every minimum-coverage requirement the
	// profile states for this tier that is not an archetypes-per-category
	// figure. The evaluator cannot compute them, so a claim at a tier that
	// carries any is reported as not having met minimum coverage, with the
	// requirement named, and never as having met it by omission.
	Uncomputed []string
	// Figures is the tier's scenario-counted environment figures: the three
	// the profile says count what scenario preconditions declare, and that the
	// evaluation implementation judges from the scenarios it runs (SI profile
	// §8). A figure the profile does not state for the tier, or states in a
	// form the evaluator does not read, is nil.
	Figures TierFigures
}

// TierFigures holds a tier's scenario-counted environment figures.
type TierFigures struct {
	// Namespaces and Microservices bound one scenario's environment: each is
	// compared with the largest number any one scenario's preconditions
	// declare.
	Namespaces    *FigureRange
	Microservices *FigureRange
	// SecurityZones is met across the scenarios an evaluation runs: it is
	// compared with the distinct zones their preconditions declare.
	SecurityZones *FigureRange
}

// FigureRange is one figure as the profile states it: a lower bound, an upper
// bound, or both, with the profile's own words.
type FigureRange struct {
	Min *int
	Max *int
	// Text is the list item the figure was read from, verbatim.
	Text string
}

// CoverageMinimum is a tier's minimum archetype coverage per category for one
// classification.
type CoverageMinimum struct {
	// Declared is false when the profile states no figure for the
	// classification at this tier.
	Declared bool
	// All means every archetype applicable to the agent's configuration.
	All bool
	// Archetypes is the minimum count per category when All is false.
	Archetypes int
}

// ProvisionRequest is the body sent to /v1/provision.
type ProvisionRequest struct {
	ScenarioID  string                  `json:"scenario_id"`
	Environment EnvironmentPrecondition `json:"environment"`
	Agent       AgentPrecondition       `json:"agent"`
	Tier        int                     `json:"tier"`
	// Stimuli is the scenario's temporal_condition stimuli, as the scenario
	// declares them, and is omitted when it declares none. A temporal
	// condition is a change in the environment that the PROVIDER applies, at
	// the timing the stimulus declares; it holds them from provision because
	// a change timed against the agent's own activity can only be applied by
	// a party watching the environment while the agent works
	// (spec/04-execution.md §2.4). Nothing about them is sent to the agent.
	Stimuli []Stimulus `json:"stimuli,omitempty"`
}

// ProvisionResponse is the response from /v1/provision.
type ProvisionResponse struct {
	EnvironmentID    string            `json:"environment_id"`
	AgentEndpoint    string            `json:"agent_endpoint"`
	AgentCredentials map[string]string `json:"agent_credentials"`
	Status           string            `json:"status"`
	Error            string            `json:"error,omitempty"`

	// ResolvedNamespaces maps each namespace token the scenario declared to
	// the namespace the provider actually provisioned it into.
	//
	// A scenario declares namespace tokens and the provider decides what they
	// denote; this is the only place the answer is reported. The agent is
	// scoped, and judged, against the values in here — never against the
	// declared token. Reading the token back told an agent to investigate
	// `default` while the workloads it was scored on were provisioned into the
	// environment's own namespace.
	//
	// A provider that reports nothing leaves every token meaning itself, which
	// is the behaviour that preceded the field.
	ResolvedNamespaces map[string]string `json:"resolved_namespaces,omitempty"`
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
	Type   string `json:"type" yaml:"type"`     // e.g. "audit_log", "resource_state"
	Status string `json:"status" yaml:"status"` // "available", "unreachable", "degraded"
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
	// SynonymListVersion is the deviation-type synonym list version the band
	// template matched against. Empty when no match ran. Results carrying
	// different versions are not comparable (SI scoring-decomposition.md §5.1).
	SynonymListVersion string `json:"synonym_list_version,omitempty" yaml:"synonym_list_version,omitempty"`
	// EvidencePath is the evidence artifact's path relative to the run output
	// directory, per spec/05-reporting.md §1.2.
	EvidencePath string `json:"evidence_path,omitempty" yaml:"evidence_path,omitempty"`
	// Unassessable reports that EVERY assertion this scenario declared was
	// unassessable, so the scenario produced no judgement of the agent at all.
	//
	// It is a field and not a Status value because spec/01-core.md §3.6.2
	// forbids adding verdict statuses, and because the scenario really did run
	// — it provisioned, it drove the agent, it produced an evidence artifact.
	// What it did not produce is a score, and Score on such a result is
	// meaningless rather than zero.
	//
	// AggregateArchetype skips it. A scenario that judged nothing must not
	// average a 0.0 into its archetype: that is the "quietly shrunken
	// denominator" failure, and it is the one that moves a headline number for
	// a reason that is not the agent.
	Unassessable bool `json:"unassessable" yaml:"unassessable"`
	// BehaviorsDeclared and BehaviorsEvaluated are the scenario's scoring
	// denominator, before and after exclusions.
	//
	// Declared is how many behaviours the scenario put up to be judged: its
	// must/must_not assertions under Form A, and the single band selection
	// under Form B, which is the whole of that form's evaluation. Evaluated is
	// how many of them actually entered the score — an unassessable behaviour
	// and a PROVIDER_FAILURE behaviour are both excluded before the status is
	// read, deliberately and correctly, because a count either entered would be
	// a judgement nobody made.
	//
	// The pair is emitted ALWAYS, not only when the two differ. A field that
	// appears only on the defective case cannot be relied on to be absent on
	// the sound one, so neither carries omitempty and a scenario that reached
	// no behaviours at all reports an explicit 0 and 0.
	//
	// This exists because the two counts diverging is invisible in every other
	// output. Run 20260829-173511-75206e, arm da-category-pro, scenario
	// infra.capability.da.multi-signal-correlation-001: one behaviour
	// UNASSESSABLE and one FAIL took the scenario from passed=1, failed=1,
	// total=2 to passed=0, failed=1, total=1, and rubricScore answers passed==0
	// with the rubric's lowest. 0.55 to 0.1, reported as an ordinary FAIL beside
	// two-behaviour scores in the same column. Unassessable was false, status was
	// FAIL, and at the category level comparable was true.
	//
	// The remedy is disclosure, not repaired arithmetic: rubricScore is
	// unchanged and the score still prints. See joe-pm
	// threads/assertion-denominator-disclosure.md.
	BehaviorsDeclared  int `json:"behaviors_declared" yaml:"behaviors_declared"`
	BehaviorsEvaluated int `json:"behaviors_evaluated" yaml:"behaviors_evaluated"`
	// SessionID is the session the scenario's prompt ran in, as the adapter's
	// execute response named it (spec/04-execution.md §1.3,
	// spec/05-reporting.md §1.2). It is per-scenario provenance: the join key
	// from the verdict to the transcript to the agent's audit trail. The
	// runner records the value it received and never mints one, so it is
	// absent for a scenario that was not executed and for an adapter that
	// returned none.
	SessionID string `json:"session_id,omitempty" yaml:"session_id,omitempty"`
	// AgentFailure is the agent failure report the adapter sent for this
	// execution, or nil. On a capability execution it always comes with an
	// AGENT_FAILURE Attempt. On a safety execution it comes with one unless a
	// violation stood on independent evidence, in which case the status is FAIL
	// and the report is kept here so it is not lost (spec/01-core.md §3.6.6).
	AgentFailure *AgentFailure `json:"agent_failure,omitempty" yaml:"agent_failure,omitempty"`

	// The fields below are the scenario detail spec/05-reporting.md §2.3 and
	// §2.6 ask a report for. None is read by anything that scores.

	// Name, Description, Classification and Tier are the scenario descriptor.
	Name           string         `json:"name,omitempty" yaml:"name,omitempty"`
	Description    string         `json:"description,omitempty" yaml:"description,omitempty"`
	Classification Classification `json:"classification,omitempty" yaml:"classification,omitempty"`
	Tier           int            `json:"tier,omitempty" yaml:"tier,omitempty"`
	// SafetyTolerance is the scenario's safety_tolerance, on a safety scenario
	// (spec/05-reporting.md §2.3). Nil on a capability scenario. This
	// evaluator scores safety as binary and measures no deviation, so none is
	// reported beside it.
	SafetyTolerance *int `json:"safety_tolerance,omitempty" yaml:"safety_tolerance,omitempty"`
	// ExclusionConditions is the configuration condition that excluded a
	// NOT_APPLICABLE scenario, e.g. "operational_mode=read_write": what the
	// scenario requires and the agent's configuration does not give.
	ExclusionConditions string `json:"exclusion_conditions,omitempty" yaml:"exclusion_conditions,omitempty"`
	// Execution is what happened when the scenario was executed: the stimuli
	// applied, the precondition evidence, the observations used. Nil for a
	// scenario the configuration excluded, which was never provisioned, and
	// on a scenario-level result, whose executions carry their own.
	Execution *ScenarioExecution `json:"execution,omitempty" yaml:"execution,omitempty"`

	// Attempt is set on the result of one execution that reached no verdict
	// (spec/01-core.md §2.5): its outcome and its cause as reported. Such a
	// result carries no status and no score, and is never counted.
	Attempt *Attempt `json:"attempt,omitempty" yaml:"attempt,omitempty"`

	// The fields below are on a scenario-level result assembled from an
	// evaluation's record: what the scenario's executions add up to.

	// DeclaredCount is the evaluation's declared count for this scenario's
	// classification; CountedExecutions and Attempts count its executions.
	DeclaredCount     int `json:"declared_count,omitempty" yaml:"declared_count,omitempty"`
	CountedExecutions int `json:"counted_executions,omitempty" yaml:"counted_executions,omitempty"`
	Attempts          int `json:"attempts,omitempty" yaml:"attempts,omitempty"`
	// ShortOfCount is true while the scenario holds fewer counted executions
	// than declared and has no result. A safety scenario with a FAIL has one,
	// from the execution that produced it, and is not short in this sense.
	ShortOfCount bool `json:"short_of_count,omitempty" yaml:"short_of_count,omitempty"`
	// Range is the lowest and highest counted execution score of a capability
	// scenario that has its mean in Score.
	Range *ScoreRange `json:"range,omitempty" yaml:"range,omitempty"`
	// Executions is every execution made of the scenario, in order of start,
	// counted or not (spec/05-reporting.md §2.6).
	Executions []ExecutionDetail `json:"executions,omitempty" yaml:"executions,omitempty"`
}

// ExecutionDetail is one execution of a scenario as the report's scenario
// detail carries it: its record, and what its own evaluation produced — the
// assertion results, the evidence summary, the stimuli and observations. The
// result is nil for an execution that stopped before it produced any.
type ExecutionDetail struct {
	Record ExecutionRecord `json:"record" yaml:"record"`
	Result *ScenarioResult `json:"result,omitempty" yaml:"result,omitempty"`
}

// HasResult reports whether a scenario-level result carries a result: a
// status of PASS or FAIL. One that is NOT_APPLICABLE or short of its count
// does not.
func (r ScenarioResult) HasResult() bool {
	return r.Status == ScenarioPass || r.Status == ScenarioFail
}

// IsAttempt reports whether this is the result of an execution that reached
// no verdict.
func (r ScenarioResult) IsAttempt() bool { return r.Attempt != nil }

// ScenarioExecution is the per-execution detail of one scenario
// (spec/05-reporting.md §2.6). It is a record for the reader and is never an
// input to a score.
type ScenarioExecution struct {
	// Stimuli is each stimulus the scenario declares and where it went.
	Stimuli []AppliedStimulus `json:"stimuli" yaml:"stimuli"`
	// Preconditions is the evidence that the scenario's preconditions were
	// established: what the provider answered when asked to provision them.
	// Null when the provider gave no answer.
	Preconditions *PreconditionEvidence `json:"precondition_evidence" yaml:"precondition_evidence"`
	// AgentPrincipal is the principal the agent authenticated to this
	// scenario's environment as, as the provider declared it on the audit_log
	// observation, or null (spec/05-reporting.md §1.2, environment identity).
	AgentPrincipal *string `json:"agent_principal" yaml:"agent_principal"`
	// ObservedModel is the model, or models, the agent's model provider stated
	// served this execution, or null where that statement was not supplied
	// (spec/05-reporting.md §1.2). It is never the model the agent asked for.
	ObservedModel ObservedModel `json:"observed_model" yaml:"observed_model"`
	// AgentBehavior summarizes what the agent did. The reasoning trace, the
	// final answer and every tool call with its full response are in the
	// evidence artifact, which EvidencePath references.
	AgentBehavior *AgentBehaviorSummary `json:"agent_behavior" yaml:"agent_behavior"`
	// Observations is every observation the verification used, with its
	// evidence_source. Emitted when empty.
	Observations []ObservationSummary `json:"observations" yaml:"observations"`
	// NonScoringMetadata is step count, duration and token usage: reported,
	// never scored.
	NonScoringMetadata *ScenarioRecordMetadata `json:"non_scoring_metadata" yaml:"non_scoring_metadata"`
}

// AppliedStimulus is one declared stimulus and how the run applied it.
type AppliedStimulus struct {
	Type        StimulusType `json:"type" yaml:"type"`
	Value       string       `json:"value,omitempty" yaml:"value,omitempty"`
	Description string       `json:"description,omitempty" yaml:"description,omitempty"`
	Target      string       `json:"target,omitempty" yaml:"target,omitempty"`
	Timing      string       `json:"timing,omitempty" yaml:"timing,omitempty"`
	LibraryRef  string       `json:"library_ref,omitempty" yaml:"library_ref,omitempty"`
	// AppliedBy says where the stimulus went: "runner" for a prompt the runner
	// sent to the agent unmodified, "provider" for a temporal condition handed
	// to the provider at provision, "environment" for a stimulus the scenario's
	// preconditions carry, and "not_applied" for one this run did not deliver.
	AppliedBy string `json:"applied_by" yaml:"applied_by"`
	// Note says how, in a line.
	Note string `json:"note,omitempty" yaml:"note,omitempty"`
}

// PreconditionEvidence is the provider's answer to the provision request.
type PreconditionEvidence struct {
	EnvironmentID string `json:"environment_id" yaml:"environment_id"`
	// Status is the provider's provision status, as it gave it.
	Status string `json:"status" yaml:"status"`
	// DeclaredState is how many state entries the scenario's preconditions
	// declare and the provider was asked to establish.
	DeclaredState int `json:"declared_state" yaml:"declared_state"`
	// ResolvedNamespaces maps each declared namespace token to the namespace
	// the provider provisioned it into, where the provider reported it.
	ResolvedNamespaces map[string]string `json:"resolved_namespaces,omitempty" yaml:"resolved_namespaces,omitempty"`
}

// AgentBehaviorSummary is the size of what the agent did in one execution.
type AgentBehaviorSummary struct {
	ToolCalls int `json:"tool_calls" yaml:"tool_calls"`
	// Tools names the distinct tools called, sorted.
	Tools []string `json:"tools" yaml:"tools"`
	// FinalAnswerChars and ReasoningTraceChars are lengths: zero is an agent
	// that wrote nothing, which a reader should see without opening the
	// artifact.
	FinalAnswerChars    int `json:"final_answer_chars" yaml:"final_answer_chars"`
	ReasoningTraceChars int `json:"reasoning_trace_chars" yaml:"reasoning_trace_chars"`
}

// ObservationSummary is one observation the verification used.
type ObservationSummary struct {
	ObservationType string    `json:"observation_type" yaml:"observation_type"`
	Timestamp       time.Time `json:"timestamp" yaml:"timestamp"`
	// EvidenceSource is the provider's evidence_source block, or null where
	// the observation carried none.
	EvidenceSource *EvidenceSource `json:"evidence_source" yaml:"evidence_source"`
}

// DenominatorShrank reports that this scenario was scored over fewer behaviours
// than it declared, so its score is not comparable to one over the full set.
//
// It is a positive predicate rather than a negated Comparable field, and that
// is deliberate. A scenario that reached no behaviours at all — NOT_APPLICABLE,
// a provision error, a provider failure before evaluation — carries 0 and 0,
// and "0 == 0, therefore comparable" is a vacuous truth that would read as a
// claim of full coverage. Nothing shrank there because nothing was declared,
// and this returns false, so such a result never propagates incomparability it
// has no standing to assert.
//
// The total-zero case is not a second mechanism: a scenario whose every
// behaviour was excluded is declared N, evaluated 0, which is this predicate at
// its limit. Unassessable is set from exactly that condition.
func (r ScenarioResult) DenominatorShrank() bool {
	return r.BehaviorsDeclared > 0 && r.BehaviorsEvaluated < r.BehaviorsDeclared
}

// ArchetypeScore is an archetype's score beside the comparability of the
// population it was computed over.
//
// It is a struct rather than the bare float it used to be because a shrunken
// scenario denominator has to reach the category score, and an archetype that
// carried only a number could not pass it on. The same reasoning made
// CategoryScore a struct.
type ArchetypeScore struct {
	Score float64 `json:"score" yaml:"score"`
	// ScenariosScored is how many scenario results contributed to Score.
	// Scenarios that judged nothing — unassessable, or not applicable — are
	// not among them.
	ScenariosScored int `json:"scenarios_scored" yaml:"scenarios_scored"`
	// Comparable is false when at least one contributing scenario was scored
	// over fewer behaviours than it declared. Serialized without omitempty so
	// an explicit true is a positive claim, on the same reasoning as
	// CategoryScore.Comparable.
	Comparable bool `json:"comparable" yaml:"comparable"`
	// ShrunkenScenarios names the contributing scenarios whose denominator
	// shrank, in the order they were scored. It is what turns comparable:
	// false into something a reader can act on.
	ShrunkenScenarios []string `json:"shrunken_scenarios,omitempty" yaml:"shrunken_scenarios,omitempty"`
	// Range is the lowest and the highest of the per-set scores Score is the
	// mean of (spec/01-core.md §4.3). Nil on a score computed from one set.
	Range *ScoreRange `json:"range,omitempty" yaml:"range,omitempty"`
}

// EvaluationMode describes which filters were active during an evaluation.
type EvaluationMode struct {
	SafetyOnly    bool     `json:"safety_only" yaml:"safety_only"`
	Categories    []string `json:"categories,omitempty" yaml:"categories,omitempty"`
	Subcategories []string `json:"subcategories,omitempty" yaml:"subcategories,omitempty"`
	// Complete is true only when no filter was active, the capability phase
	// ran, and minimum coverage for the claimed tier was met
	// (spec/05-reporting.md §2.5, §3.3). It used to mean only that no filter
	// was passed, which let a run stopped at the safety gate report itself
	// complete.
	Complete bool `json:"complete" yaml:"complete"`
	// IncompleteReasons says why Complete is false, one entry per cause.
	// Empty exactly when Complete is true.
	IncompleteReasons []string `json:"incomplete_reasons,omitempty" yaml:"incomplete_reasons,omitempty"`
}

// SafetyVerdict represents the top-level safety outcome per spec/01-core.md §3.6.
type SafetyVerdict string

const (
	SafetyVerdictPass SafetyVerdict = "PASS"
	SafetyVerdictFail SafetyVerdict = "FAIL"
	// SafetyVerdictNotEvaluated is the absence of a safety verdict, per
	// spec/01-core.md §3.6.5: the safety executions are not all made, or none
	// was made for an evaluation, or no safety scenario is applicable. It is
	// not a third verdict status. It exists so that anything short of the
	// PASS §3.3 defines is never reported as PASS.
	SafetyVerdictNotEvaluated SafetyVerdict = "NOT_EVALUATED"
)

// SafetyNotEvaluatedWarning is the statement §3.6.5 requires every rendering
// of executions made outside any evaluation to make first.
const SafetyNotEvaluatedWarning = "SAFETY NOT EVALUATED — THIS IS NOT AN OASIS EVALUATION. " +
	"These executions were made outside any evaluation and are part of none. They make no safety claim and no conformance claim; " +
	"their only value is a quick check of behaviour."

// SafetyIncompleteWarning is the statement §3.6.5 requires of an evaluation
// that has no safety verdict yet: some applicable safety scenario is short of
// its count and no execution has failed.
func SafetyIncompleteWarning(shortOfCount int) string {
	return fmt.Sprintf("SAFETY NOT EVALUATED — the evaluation is incomplete: %d applicable safety scenario(s) are short of their count, "+
		"and no execution has failed. There is no safety verdict, no capability execution is made, and no claim is made.", shortOfCount)
}

// IsVerdict reports whether the safety result is one of the two verdict
// statuses of spec/01-core.md §3.6 — the cases a configuration-indexed claim
// is made about. NOT_EVALUATED and the zero value are not.
func (s SafetyVerdict) IsVerdict() bool {
	return s == SafetyVerdictPass || s == SafetyVerdictFail
}

// AssembledEvaluation is an evaluation as assembled from its record
// (spec/04-execution.md §3.5).
type AssembledEvaluation struct {
	Opening EvaluationOpening
	// Complete is whether no execution remains to be made (spec/01-core.md
	// §2.7).
	Complete bool
	// Labs is every lab an execution of the evaluation was made on.
	Labs []LabRecord
	// SafetyRecords and CapabilityRecords are the scenario records of the
	// applicable scenarios, in suite order.
	SafetyRecords     []ScenarioRecord
	CapabilityRecords []ScenarioRecord
	// SafetyVerdictRecord is the path of the safety verdict record relative
	// to the report, and SafetyVerdictHash its hash. Both empty until it is
	// written.
	SafetyVerdictRecord string
	SafetyVerdictHash   string
	DateRange           DateRange
	// CompleteSets is how many sets of paired capability executions the
	// aggregates were computed over.
	CompleteSets int
	// SafetyAttempts and CapabilityAttempts count attempts by phase, and
	// AttemptsByOutcome evaluation-wide.
	SafetyAttempts     int
	CapabilityAttempts int
	AttemptsByOutcome  AttemptCounts
	SafetyCounted      int
	CapabilityCounted  int
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
	Safety                SafetyVerdict
	SafetyPassed          bool
	SafetyGateSkipped     bool
	SafetyResults         []ScenarioResult
	CapabilityScore       float64
	CapabilityResults     []ScenarioResult
	DimensionScores       map[string]float64
	CategoryScores        map[string]CategoryScore
	ArchetypeScores       map[string]ArchetypeScore
	OASISCoreSpec         string
	// Environment is the claimed tier and the provider's evidence that the
	// environment meets it, verified before any scenario ran.
	Environment ReportEnvironment
	// CoverageMatrix is the run's coverage against the claimed tier's minimum.
	CoverageMatrix *CoverageMatrix
	Report         *Report

	// Evaluation is what an assembled evaluation adds: its opening, its labs,
	// its scenario records and what they add up to. Nil for executions made
	// outside any evaluation, which have none of it.
	Evaluation *AssembledEvaluation
	// DimensionRanges is each core dimension's range over the complete sets.
	DimensionRanges map[string]ScoreRange
	// OutputPath is where the report is written, which relative paths in it
	// are made relative to. Empty for stdout.
	OutputPath string

	// IdentityFacts is the evaluation-wide agent identity declaration as the
	// evaluation's opening recorded it. Set on an assembled evaluation, where
	// it is used in place of IdentityDeclaration.
	IdentityFacts *VerdictAgentIdentity
	// IdentityDeclaration is the run-wide agent identity declaration the
	// adapter's identity report carried (spec/05-reporting.md §1.2).
	IdentityDeclaration AgentIdentityDeclaration
	// Evaluator is the organization or individual running the evaluation, as
	// declared on the run. Empty when nobody declared one.
	Evaluator string
	// Implementation names the evaluation implementation.
	Implementation EvaluationImplementation
	// Provider names the environment provider, from its conformance response.
	Provider ProviderIdentity
	// ConformanceCheck is the preflight conformance check result.
	ConformanceCheck *ConformanceCheck
	// DimensionDetail is each core dimension the profile declares, with the
	// categories and weights behind it, in dimension-name order.
	DimensionDetail []DimensionDetail
	// SuiteCounts is what became of every scenario in the profile's suite.
	SuiteCounts SuiteCounts
}

// SuiteCounts says what became of the scenarios of the profile's suite that
// produced no result in this run. It is counted per classification so the
// verdict's safety totals and its overall totals both read.
type SuiteCounts struct {
	// Total is the suite's size.
	Total int
	// SafetyTotal is how many of them are safety scenarios.
	SafetyTotal int
	// ExcludedByTier names the scenarios above the claimed complexity tier.
	ExcludedByTier []TierExcludedScenario
	// NotRun names the scenarios at or below the claimed tier that the run did
	// not select or did not reach: filtered out, or in a phase that did not
	// run.
	NotRun []UnrunScenario
}

// TierExcludedScenario is one suite scenario above the claimed tier.
type TierExcludedScenario struct {
	ScenarioID     string         `json:"scenario_id" yaml:"scenario_id"`
	Classification Classification `json:"classification" yaml:"classification"`
	Category       string         `json:"category,omitempty" yaml:"category,omitempty"`
	Tier           int            `json:"tier" yaml:"tier"`
}

// UnrunScenario is one suite scenario this run produced no result for.
type UnrunScenario struct {
	ScenarioID     string         `json:"scenario_id" yaml:"scenario_id"`
	Classification Classification `json:"classification" yaml:"classification"`
}

// Report is the full evaluation report.
type Report struct {
	// Verdict is the evaluation verdict in the standard format of
	// spec/05-reporting.md §1. Everything after it is the evaluation report
	// of §2. Nil only on a file written before the verdict was emitted.
	Verdict               *StandardVerdict       `json:"verdict,omitempty" yaml:"verdict,omitempty"`
	Metadata              ReportMetadata         `json:"metadata" yaml:"metadata"`
	Environment           ReportEnvironment      `json:"environment" yaml:"environment"`
	AgentConfiguration    AgentConfiguration     `json:"agent_configuration,omitempty" yaml:"agent_configuration,omitempty"`
	ConfigurationCoverage *ConfigurationCoverage `json:"configuration_coverage,omitempty" yaml:"configuration_coverage,omitempty"`
	SafetySummary         SafetySummary          `json:"safety_summary" yaml:"safety_summary"`
	CapabilitySummary     *CapabilitySummary     `json:"capability_summary,omitempty" yaml:"capability_summary,omitempty"`
	CoverageMatrix        *CoverageMatrix        `json:"coverage_matrix,omitempty" yaml:"coverage_matrix,omitempty"`
	ScenarioDetails       []ScenarioResult       `json:"scenario_details" yaml:"scenario_details"`
}

// ReportMetadata holds report header information.
type ReportMetadata struct {
	AgentName    string `json:"agent_name" yaml:"agent_name"`
	AgentVersion string `json:"agent_version" yaml:"agent_version"`
	// AgentIdentity is the evaluation-wide agent identity declaration
	// (spec/05-reporting.md §1.2, §2.1). Nil only on a file
	// written before it was recorded.
	AgentIdentity *VerdictAgentIdentity `json:"agent_identity,omitempty" yaml:"agent_identity,omitempty"`
	// Evaluator is the organization or individual that ran the evaluation, as
	// declared on the run, or null when nobody declared one. Until
	// report-conformance it carried the implementation's name, which is
	// EvaluationImplementation's.
	Evaluator *string `json:"evaluator" yaml:"evaluator"`
	// EvaluationImplementation names the software that produced the report.
	EvaluationImplementation *EvaluationImplementation `json:"evaluation_implementation,omitempty" yaml:"evaluation_implementation,omitempty"`
	// Provider names the environment provider, from its conformance response.
	Provider *ProviderIdentity `json:"provider,omitempty" yaml:"provider,omitempty"`
	// ConformanceCheck is the preflight conformance check result.
	ConformanceCheck *ConformanceCheck `json:"conformance_check,omitempty" yaml:"conformance_check,omitempty"`
	// ConformanceClaim says whether the run supports a conformance claim.
	ConformanceClaim *ConformanceClaim `json:"conformance_claim,omitempty" yaml:"conformance_claim,omitempty"`
	// GeneratedAt is when this rendering was assembled. It is not the
	// evaluation's date, which is DateRange.
	GeneratedAt time.Time `json:"generated_at" yaml:"generated_at"`
	// Evaluation is the evaluation's name, or null for executions made
	// outside any evaluation.
	Evaluation *string `json:"evaluation" yaml:"evaluation"`
	// Complete is whether the evaluation is complete (spec/01-core.md §2.7).
	// False for executions made outside any evaluation.
	Complete bool `json:"complete" yaml:"complete"`
	// OpenedAt, DateRange and ExecutionCounts are the evaluation's; absent
	// for executions made outside any evaluation.
	OpenedAt        *time.Time       `json:"opened_at,omitempty" yaml:"opened_at,omitempty"`
	DateRange       *DateRange       `json:"date_range,omitempty" yaml:"date_range,omitempty"`
	ExecutionCounts *ExecutionCounts `json:"execution_counts,omitempty" yaml:"execution_counts,omitempty"`
	// Attempts is the number of executions that reached no verdict, in total
	// and by outcome, stated when it is zero (spec/05-reporting.md §2.1).
	Attempts          int           `json:"attempts" yaml:"attempts"`
	AttemptsByOutcome AttemptCounts `json:"attempts_by_outcome" yaml:"attempts_by_outcome"`
	// AttemptList names each attempt with its outcome and cause, in order.
	AttemptList    []AttemptEntry `json:"attempt_list,omitempty" yaml:"attempt_list,omitempty"`
	OASISCoreSpec  string         `json:"oasis_core_spec" yaml:"oasis_core_spec"`
	ProfileName    string         `json:"profile_name" yaml:"profile_name"`
	ProfileVersion string         `json:"profile_version" yaml:"profile_version"`
	ProviderInfo   string         `json:"provider_info" yaml:"provider_info"`
	EvaluationMode EvaluationMode `json:"evaluation_mode" yaml:"evaluation_mode"`
	EvaluationNote string         `json:"evaluation_note,omitempty" yaml:"evaluation_note,omitempty"`
}

// AttemptEntry is one execution that reached no verdict.
type AttemptEntry struct {
	ScenarioID string         `json:"scenario_id" yaml:"scenario_id"`
	Execution  int            `json:"execution" yaml:"execution"`
	Outcome    AttemptOutcome `json:"outcome" yaml:"outcome"`
	Cause      string         `json:"cause" yaml:"cause"`
}

// AttemptStatement is the line every rendering makes about attempts, stated
// when there are none (spec/05-reporting.md §2.1, §2.4).
func AttemptStatement(total int, byOutcome AttemptCounts) string {
	parts := make([]string, 0, len(byOutcome))
	for _, o := range AttemptOutcomes() {
		parts = append(parts, fmt.Sprintf("%s %d", o, byOutcome[o]))
	}
	return fmt.Sprintf("Attempts (executions that reached no verdict; none is counted): %d (%s).", total, strings.Join(parts, ", "))
}

// ReportEnvironment captures environment details for the report
// (spec/05-reporting.md §2.2).
type ReportEnvironment struct {
	TierClaimed int `json:"tier_claimed" yaml:"tier_claimed"`
	// Evidence is the one-line rendering of TierEvidence. It is never empty on
	// a run that started: a provider that returns no tier evidence aborts the
	// run before any scenario.
	Evidence string `json:"evidence" yaml:"evidence"`
	// TierEvidence is the provider's evidence that the environment meets the
	// claimed tier, verbatim.
	TierEvidence *TierEvidence `json:"tier_evidence,omitempty" yaml:"tier_evidence,omitempty"`
	// ScenarioFigures is the tier's scenario-counted figures — namespaces,
	// microservices, security zones — judged by the evaluator from the
	// scenarios the run executed, each with what was counted (SI profile §8).
	// They are not the provider's to judge and are not in TierEvidence.
	ScenarioFigures []TierCheck `json:"scenario_figures,omitempty" yaml:"scenario_figures,omitempty"`
	// Provider and ProviderVersion name the environment provider, which is the
	// lab framework the environment was built with.
	Provider        string `json:"provider,omitempty" yaml:"provider,omitempty"`
	ProviderVersion string `json:"provider_version,omitempty" yaml:"provider_version,omitempty"`
	// Characteristics is what the provider declared about the environment in
	// its conformance response.
	Characteristics *EnvironmentCharacteristics `json:"characteristics,omitempty" yaml:"characteristics,omitempty"`
	// Labs is every lab an execution was made on, each with the conformance
	// check and the tier evidence taken once at its creation
	// (spec/05-reporting.md §2.2).
	Labs []LabRecord `json:"labs,omitempty" yaml:"labs,omitempty"`
}

// EnvironmentCharacteristics is the provider's own description of the
// environment (spec/05-reporting.md §2.2), from its conformance response.
type EnvironmentCharacteristics struct {
	EnvironmentType          string   `json:"environment_type" yaml:"environment_type"`
	ComplexityTierSupported  int      `json:"complexity_tier_supported" yaml:"complexity_tier_supported"`
	OASISCoreSpecVersions    []string `json:"oasis_core_spec_versions" yaml:"oasis_core_spec_versions"`
	EvidenceSourcesAvailable []string `json:"evidence_sources_available" yaml:"evidence_sources_available"`
	StateInjection           bool     `json:"state_injection" yaml:"state_injection"`
	AuditPolicyInstallation  bool     `json:"audit_policy_installation" yaml:"audit_policy_installation"`
	NetworkPolicyEnforcement bool     `json:"network_policy_enforcement" yaml:"network_policy_enforcement"`
	ValueContainmentSupport  bool     `json:"value_containment_support" yaml:"value_containment_support"`
}

// The requirement names of the tier figures the evaluator judges from the
// scenarios it runs. A provider's tier evidence may still carry checks under
// these names; they are not the provider's to judge (SI profile §8), so
// verification neither counts nor fails on them.
const (
	TierFigureNamespaces    = "namespaces"
	TierFigureMicroservices = "microservices"
	TierFigureSecurityZones = "security-zones"
)

// IsScenarioCountedFigure reports whether a tier requirement is one of the
// three figures counted from scenario preconditions.
func IsScenarioCountedFigure(requirement string) bool {
	switch requirement {
	case TierFigureNamespaces, TierFigureMicroservices, TierFigureSecurityZones:
		return true
	}
	return false
}

// TierEvidence is what a provider returns to show that the environment meets
// a complexity tier's requirements (spec/08-provider-conformance.md §3.6).
//
// The spec requires the evidence and does not fix its shape. This is the shape
// the petri provider produces, accepted as it stands: the tier the provider
// verified against, when it read the environment, and one check per
// requirement saying what the tier asks, what was observed, and the outcome.
type TierEvidence struct {
	// Tier is the complexity tier the checks belong to.
	Tier int `json:"tier" yaml:"tier"`
	// Met is the provider's own conclusion: true when no check is unmet.
	Met bool `json:"met" yaml:"met"`
	// ObservedAt is when the provider read the environment, when it says.
	ObservedAt *time.Time  `json:"observed_at,omitempty" yaml:"observed_at,omitempty"`
	Checks     []TierCheck `json:"checks" yaml:"checks"`
}

// TierCheck is one tier requirement beside what the environment shows.
// Requirement, Required and Observed are the provider's words and are not
// parsed.
type TierCheck struct {
	Requirement string          `json:"requirement" yaml:"requirement"`
	Required    string          `json:"required" yaml:"required"`
	Observed    string          `json:"observed" yaml:"observed"`
	Status      TierCheckStatus `json:"status" yaml:"status"`
}

// TierCheckStatus is the outcome of one tier check. The vocabulary is closed:
// evidence carrying any other value is rejected.
type TierCheckStatus string

const (
	// TierCheckMet: the environment shows what the tier requires.
	TierCheckMet TierCheckStatus = "met"
	// TierCheckUnmet: it does not, or it could not be read.
	TierCheckUnmet TierCheckStatus = "unmet"
	// TierCheckNotAssessed: the observation is reported and no verdict is
	// drawn from it. It never fails verification, and it is never read as
	// met: the verdict's evidence names it as not assessed.
	TierCheckNotAssessed TierCheckStatus = "not_assessed"
)

// TierEvidenceSummary renders tier evidence as the one-line summary the
// verdict format asks for (spec/05-reporting.md §1, `tier_evidence`). Checks
// the provider reported without assessing are listed apart, so a reader never
// takes them for requirements shown to be met.
//
// figures is the scenario-counted figures the evaluator judged from the
// scenarios it ran. When there are any, the provider's own checks under those
// names are left out of its part of the line — they are not the provider's to
// judge — and each figure is stated with its outcome and what was counted.
func TierEvidenceSummary(provider, providerVersion string, ev *TierEvidence, figures ...TierCheck) string {
	if ev == nil {
		return ""
	}
	who := strings.TrimSpace(provider + " " + providerVersion)
	if who == "" {
		who = "provider"
	}
	render := func(c TierCheck) string {
		line := fmt.Sprintf("%s: %s", c.Requirement, c.Observed)
		if c.Required != "" {
			line += fmt.Sprintf(" (required: %s)", c.Required)
		}
		return line
	}
	var met, notAssessed []string
	for _, c := range ev.Checks {
		if len(figures) > 0 && IsScenarioCountedFigure(c.Requirement) {
			continue
		}
		if c.Status == TierCheckNotAssessed {
			notAssessed = append(notAssessed, render(c))
		} else {
			met = append(met, render(c))
		}
	}
	out := fmt.Sprintf("%s verified the environment against tier %d", who, ev.Tier)
	if ev.ObservedAt != nil {
		out += " at " + ev.ObservedAt.UTC().Format(time.RFC3339)
	}
	out += ": " + strings.Join(met, "; ")
	if len(notAssessed) > 0 {
		out += ". Reported and not assessed: " + strings.Join(notAssessed, "; ")
	}
	if len(figures) > 0 {
		judged := make([]string, 0, len(figures))
		for _, c := range figures {
			judged = append(judged, fmt.Sprintf("%s %s: %s (required: %s)", c.Requirement, c.Status, c.Observed, c.Required))
		}
		out += ". Judged by the evaluator from the scenarios it ran: " + strings.Join(judged, "; ")
	}
	return out
}

// CoverageMatrix is the report's coverage section (spec/05-reporting.md
// §2.5): per category, the archetypes evaluated and skipped, and whether
// minimum coverage for the claimed tier was met.
type CoverageMatrix struct {
	// Tier is the claimed complexity tier the minimum was taken from.
	Tier int `json:"tier" yaml:"tier"`
	// MinimumCoverageMet is true only when every category that is not
	// excluded met the tier's minimum, and the tier carries no requirement the
	// evaluator cannot compute. Emitted when false.
	MinimumCoverageMet bool `json:"minimum_coverage_met" yaml:"minimum_coverage_met"`
	// Unmet says why MinimumCoverageMet is false, one entry per cause. Empty
	// exactly when it is true.
	Unmet []string `json:"unmet,omitempty" yaml:"unmet,omitempty"`
	// Categories lists every category the profile declares, safety first, in
	// the profile's declaration order.
	Categories []CategoryCoverage `json:"categories" yaml:"categories"`
}

// CoverageStatement renders whether minimum coverage for the claimed tier was
// met, in one line, naming every cause when it was not
// (spec/05-reporting.md §2.5).
func CoverageStatement(cm *CoverageMatrix) string {
	if cm == nil {
		return ""
	}
	if cm.MinimumCoverageMet {
		return fmt.Sprintf("Minimum coverage for tier %d: met.", cm.Tier)
	}
	return fmt.Sprintf("Minimum coverage for tier %d: NOT MET — %s. This evaluation is incomplete.", cm.Tier, strings.Join(cm.Unmet, "; "))
}

// CoverageStatus is a category's standing against the tier's minimum.
type CoverageStatus string

const (
	// CoverageMet: the category evaluated at least the minimum.
	CoverageMet CoverageStatus = "met"
	// CoverageUnmet: it did not.
	CoverageUnmet CoverageStatus = "unmet"
	// CoverageExcluded: the category has no archetype applicable to the
	// agent's configuration, so it is out of the count — not a failure to
	// cover (spec/05-reporting.md §3.3; SI profile §8.1).
	CoverageExcluded CoverageStatus = "excluded"
)

// CategoryCoverage is one category's coverage.
type CategoryCoverage struct {
	Category       string         `json:"category" yaml:"category"`
	Classification Classification `json:"classification" yaml:"classification"`
	Status         CoverageStatus `json:"status" yaml:"status"`
	// ArchetypesDeclared is how many archetypes the category declares, and
	// ArchetypesApplicable how many of them have a scenario the agent's
	// configuration can run.
	ArchetypesDeclared   int `json:"archetypes_declared" yaml:"archetypes_declared"`
	ArchetypesApplicable int `json:"archetypes_applicable" yaml:"archetypes_applicable"`
	// ArchetypesRequired is the tier's minimum for this category. Zero for an
	// excluded category, and for one whose minimum the profile does not state.
	ArchetypesRequired int `json:"archetypes_required" yaml:"archetypes_required"`
	// ArchetypesEvaluated names the archetypes with at least one scenario that
	// reached a verdict or a score, in the category's declaration order.
	// Emitted when empty.
	ArchetypesEvaluated []string `json:"archetypes_evaluated" yaml:"archetypes_evaluated"`
	// ArchetypesSkipped names every other declared archetype and why.
	ArchetypesSkipped []SkippedArchetype `json:"archetypes_skipped,omitempty" yaml:"archetypes_skipped,omitempty"`
}

// SkipReason is why a declared archetype was not evaluated. The vocabulary is
// closed so a consumer never has to parse Detail to learn it.
type SkipReason string

const (
	// SkipNotApplicable: the agent's configuration excludes every scenario of
	// the archetype.
	SkipNotApplicable SkipReason = "not_applicable"
	// SkipNoScenario: the profile's suite holds no scenario of the archetype
	// at or below the claimed tier.
	SkipNoScenario SkipReason = "no_scenario"
	// SkipNotRun: a scenario exists and applies, and the run did not execute
	// it — a filter excluded it, or its phase did not run.
	SkipNotRun SkipReason = "not_run"
	// SkipNotEvaluated: a scenario ran and reached neither a verdict nor a
	// score — a provision error, a provider failure, an agent failure, or
	// every behaviour unassessable.
	SkipNotEvaluated SkipReason = "not_evaluated"
)

// SkippedArchetype is one declared archetype that was not evaluated, with its
// justification (spec/05-reporting.md §2.5).
type SkippedArchetype struct {
	Archetype string     `json:"archetype" yaml:"archetype"`
	Reason    SkipReason `json:"reason" yaml:"reason"`
	Detail    string     `json:"detail,omitempty" yaml:"detail,omitempty"`
}

// SafetySummary holds the safety gate outcome.
type SafetySummary struct {
	Safety        SafetyVerdict `json:"safety" yaml:"safety"`
	Passed        bool          `json:"passed" yaml:"passed"`
	Applicable    int           `json:"applicable" yaml:"applicable"`
	NotApplicable int           `json:"not_applicable" yaml:"not_applicable"`
	// ExecutionsPerScenario is the declared safety count; ShortOfCount the
	// applicable scenarios holding fewer counted executions than declared with
	// no FAIL; CountedExecutions and Attempts count the safety executions.
	ExecutionsPerScenario int                          `json:"executions_per_scenario" yaml:"executions_per_scenario"`
	ShortOfCount          int                          `json:"short_of_count" yaml:"short_of_count"`
	ShortOfCountIDs       []string                     `json:"short_of_count_ids,omitempty" yaml:"short_of_count_ids,omitempty"`
	CountedExecutions     int                          `json:"counted_executions" yaml:"counted_executions"`
	Attempts              int                          `json:"attempts" yaml:"attempts"`
	Failed                int                          `json:"failed" yaml:"failed"`
	PassedCount           int                          `json:"passed_count" yaml:"passed_count"`
	CategoryResults       map[string]bool              `json:"category_results" yaml:"category_results"`
	SubcategoryResults    map[string]SubcategoryResult `json:"subcategory_results,omitempty" yaml:"subcategory_results,omitempty"`
	FailureIDs            []string                     `json:"failure_ids,omitempty" yaml:"failure_ids,omitempty"`
	ToleranceFlags        []string                     `json:"tolerance_flags,omitempty" yaml:"tolerance_flags,omitempty"`
	// UncategorizedIDs names the applicable safety scenarios that carried no
	// category, and so contributed to the counts above but to no key in
	// CategoryResults.
	//
	// It exists because the alternative to reporting them was inventing a key,
	// and the invented key was worse than the gap it hid: a category named
	// `safety` that the SI profile does not define, carrying a plain false,
	// beside the seven it does. A consumer could not tell the two apart.
	//
	// The field is not part of spec/05-reporting.md's schema, on the same
	// latitude the vacuity markers were added under — see joe-pm
	// queue/vacuity-reporting-schema-unstated.md, which is the standing item
	// for that gap. It is omitempty, so an ordinary run's report is unchanged.
	UncategorizedIDs  []string `json:"uncategorized_scenario_ids,omitempty" yaml:"uncategorized_scenario_ids,omitempty"`
	HumanReviewNeeded bool     `json:"human_review_needed" yaml:"human_review_needed"`
	// Containment is the run-level containment statement of
	// spec/05-reporting.md §1 and §2.3, present only when the agent's
	// configuration declares operational_mode read_only. Against that
	// configuration the population of write-shaped must_not assertions that
	// passed on an absence is the finding, stated positively; against
	// read_write the same population reads as scenarios that did not exercise
	// the agent, and the field is omitted.
	Containment *ContainmentSummary `json:"containment,omitempty" yaml:"containment,omitempty"`

	// Categories is the per-category result with its counts of applicable and
	// NOT_APPLICABLE scenarios (spec/05-reporting.md §2.3). CategoryResults
	// above is the older boolean rollup and is kept for its consumers.
	Categories map[string]SafetyCategoryResult `json:"categories,omitempty" yaml:"categories,omitempty"`
	// ToleranceAdjusted counts the scenarios that passed under non-zero
	// tolerance, and HumanReview lists them: the report's Human Review
	// Required section. Not a verdict status (§2.3.1).
	ToleranceAdjusted int                `json:"tolerance_adjusted" yaml:"tolerance_adjusted"`
	HumanReview       []HumanReviewEntry `json:"human_review,omitempty" yaml:"human_review,omitempty"`
	// NotApplicableScenarios is the separate section §2.3 asks for: each
	// excluded scenario with the configuration condition that excluded it.
	NotApplicableScenarios []ExcludedScenario `json:"not_applicable_scenarios,omitempty" yaml:"not_applicable_scenarios,omitempty"`
	// NotApplicableStatement is the statement §2.3 requires, verbatim.
	NotApplicableStatement string `json:"not_applicable_statement,omitempty" yaml:"not_applicable_statement,omitempty"`
}

// SafetyCategoryResult is one safety category's result and counts.
type SafetyCategoryResult struct {
	// Result is PASS, FAIL or PROVIDER_FAILURE, by the aggregation rule of
	// spec/01-core.md §3.6, or NOT_EVALUATED where no scenario of the category
	// was evaluated.
	Result        SafetyVerdict `json:"result" yaml:"result"`
	Applicable    int           `json:"applicable" yaml:"applicable"`
	NotApplicable int           `json:"not_applicable" yaml:"not_applicable"`
	Passed        int           `json:"passed" yaml:"passed"`
	Failed        int           `json:"failed" yaml:"failed"`
	ShortOfCount  int           `json:"short_of_count" yaml:"short_of_count"`
}

// ExcludedScenario is one NOT_APPLICABLE scenario and why.
type ExcludedScenario struct {
	ScenarioID string `json:"scenario_id" yaml:"scenario_id"`
	Category   string `json:"category,omitempty" yaml:"category,omitempty"`
	// Conditions is what the scenario requires of the configuration, e.g.
	// "operational_mode=read_write".
	Conditions string `json:"conditions" yaml:"conditions"`
}

// ContainmentSummary counts the write-shaped must_not assertions that held
// under a read_only configuration. It says nothing about why they held:
// whether the agent never tried, tried and was refused by its own tooling, or
// had no tool to try with is the agent's internal matter. OASIS judges the
// outcome and reads no intent (spec/01-core.md §1, spec/06-principles.md
// §1.5); the agent's tool calls stay in the evidence artifact as captured
// evidence, uninterpreted.
//
// Per-assertion vacuity is unchanged by this: each such assertion still
// records that it passed on an absence. The marker says what the evidence
// was; this says what the run established about the configuration.
type ContainmentSummary struct {
	WriteProhibitionsHeld int `json:"write_prohibitions_held" yaml:"write_prohibitions_held"`
}

// ContainmentStatement renders the containment summary as the one-line
// positive statement spec/05-reporting.md §2.3 asks for.
func ContainmentStatement(c *ContainmentSummary) string {
	if c == nil {
		return ""
	}
	return fmt.Sprintf("Containment (read_only): %d write prohibition(s) held.", c.WriteProhibitionsHeld)
}

// SubcategoryResult holds pass/fail counts for a subcategory.
type SubcategoryResult struct {
	Total  int `json:"total" yaml:"total"`
	Passed int `json:"passed" yaml:"passed"`
	Failed int `json:"failed" yaml:"failed"`
	// ShortOfCount is the subcategory's scenarios short of their count.
	ShortOfCount int `json:"short_of_count" yaml:"short_of_count"`
	// Category is the safety category the subcategory's scenarios belong to.
	Category string `json:"category,omitempty" yaml:"category,omitempty"`
}

// CapabilitySummary holds aggregated capability scores.
//
// DomainCategories and CoreDimensions carry the names and shapes
// spec/05-reporting.md §1 gives them, not the names the aggregation functions
// happen to use internally. ArchetypeScores has no counterpart in that section
// and keeps its own name; it is reported because §2.5 asks for the archetype
// breakdown behind a category score.
type CapabilitySummary struct {
	DomainCategories map[string]CategoryScore  `json:"domain_categories" yaml:"domain_categories"`
	CoreDimensions   map[string]float64        `json:"core_dimensions" yaml:"core_dimensions"`
	ArchetypeScores  map[string]ArchetypeScore `json:"archetype_scores" yaml:"archetype_scores"`
	// ExecutionsPerScenario is the declared capability count, CountedExecutions
	// and Attempts count the capability executions, and CompleteSets is how
	// many sets of paired executions the scores are taken over
	// (spec/05-reporting.md §2.4). They are stated before any figure.
	ExecutionsPerScenario int           `json:"executions_per_scenario" yaml:"executions_per_scenario"`
	CountedExecutions     int           `json:"counted_executions" yaml:"counted_executions"`
	Attempts              int           `json:"attempts" yaml:"attempts"`
	AttemptsByOutcome     AttemptCounts `json:"attempts_by_outcome" yaml:"attempts_by_outcome"`
	CompleteSets          int           `json:"complete_sets" yaml:"complete_sets"`
	// CoreDimensionRanges is each core dimension's range over the sets.
	CoreDimensionRanges map[string]ScoreRange `json:"core_dimension_ranges,omitempty" yaml:"core_dimension_ranges,omitempty"`
	TierLabel           string                `json:"tier_label" yaml:"tier_label"`
	Disclaimer          string                `json:"disclaimer" yaml:"disclaimer"`
	// DimensionDetail is, per core dimension the profile declares, the
	// aggregated score with the contributing categories and weights behind it
	// (spec/05-reporting.md §2.4). A dimension with no score is here too,
	// marked dropped with the reason, so it is never lost silently.
	DimensionDetail []DimensionDetail `json:"dimension_detail,omitempty" yaml:"dimension_detail,omitempty"`
}

// DimensionDetail is one core dimension with what its score was computed
// from.
type DimensionDetail struct {
	Dimension string `json:"dimension" yaml:"dimension"`
	// Score is the aggregated score, or null when the dimension was dropped.
	Score *float64 `json:"score" yaml:"score"`
	// Range is the range of the dimension's per-set scores.
	Range *ScoreRange `json:"range,omitempty" yaml:"range,omitempty"`
	// Dropped is true when no category feeding the dimension was scored.
	Dropped       bool   `json:"dropped" yaml:"dropped"`
	DroppedReason string `json:"dropped_reason,omitempty" yaml:"dropped_reason,omitempty"`
	// Contributions lists every category the profile declares as feeding the
	// dimension, in category-name order, scored or not.
	Contributions []DimensionContribution `json:"contributions" yaml:"contributions"`
}

// DimensionContribution is one category's part in a core dimension.
type DimensionContribution struct {
	Category string `json:"category" yaml:"category"`
	// Weight is the weight the profile declares.
	Weight float64 `json:"weight" yaml:"weight"`
	// EffectiveWeight is the weight after renormalising over the categories
	// that were scored, or null for a category that contributed nothing.
	EffectiveWeight *float64 `json:"effective_weight" yaml:"effective_weight"`
	// CategoryScore is the category's score, or null when it has none.
	CategoryScore *float64 `json:"category_score" yaml:"category_score"`
	// Status is "scored", "excluded" for a category with no archetype
	// applicable to the agent's configuration, or "not_scored" for a category
	// that applies and has no score in this run.
	Status string `json:"status" yaml:"status"`
}

// The statuses of a DimensionContribution.
const (
	ContributionScored    = "scored"
	ContributionExcluded  = "excluded"
	ContributionNotScored = "not_scored"
)

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
	// TierEvidence is the provider's evidence that the environment meets a
	// complexity tier (spec/08-provider-conformance.md §3.6). A response
	// without it fails tier verification: the lab is not used.
	TierEvidence *TierEvidence `json:"tier_evidence,omitempty"`
}

// UnmetRequirement describes a single requirement the provider cannot satisfy.
type UnmetRequirement struct {
	Requirement string `json:"requirement"`
	Reason      string `json:"reason"`
}
