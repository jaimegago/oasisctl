package evaluation

import "time"

// StandardVerdict is the evaluation verdict in the format of
// spec/05-reporting.md §1, field for field. It is emitted under the `verdict`
// key of the output document; the evaluation report of §2 is the rest of that
// document.
//
// Every key the format names is emitted on every verdict, with two kinds of
// exception the format itself states: `capability` is present only when
// safety is PASS, and the keys the format marks "present when" (containment,
// subcategory_results, heterogeneity_detail, abort_reason,
// agent_failure_scenarios) follow their condition. A value that could not be
// established is an explicit null beside a field naming it, never an invented
// value. TestVerdictConformsToSection1 holds a produced verdict to the shape.
//
// Keys beyond the format (excluded_by_tier, dropped_dimensions, and the like)
// are additions the format's own totals need in order to read; none replaces
// a key the format names.
type StandardVerdict struct {
	Safety        SafetyVerdict        `json:"safety" yaml:"safety"`
	SafetyDetails VerdictSafetyDetails `json:"safety_details" yaml:"safety_details"`
	// Capability is present only when Safety is PASS and the capability phase
	// ran.
	Capability *VerdictCapability `json:"capability,omitempty" yaml:"capability,omitempty"`
	Metadata   VerdictMetadata    `json:"metadata" yaml:"metadata"`
	// ScenarioRecords holds one record per executed scenario, in run order,
	// safety first (spec/05-reporting.md §1.2). A scenario the configuration
	// excluded was not executed and has no record.
	ScenarioRecords []ScenarioRecord `json:"scenario_records" yaml:"scenario_records"`
}

// VerdictSafetyDetails is `safety_details`.
type VerdictSafetyDetails struct {
	// TotalScenarios counts the suite's safety scenarios, whatever became of
	// each: total = applicable + not_applicable + provider_failure +
	// unevaluable + excluded_by_tier + not_run.
	TotalScenarios    int `json:"total_scenarios" yaml:"total_scenarios"`
	Applicable        int `json:"applicable" yaml:"applicable"`
	NotApplicable     int `json:"not_applicable" yaml:"not_applicable"`
	Passed            int `json:"passed" yaml:"passed"`
	Failed            int `json:"failed" yaml:"failed"`
	ProviderFailure   int `json:"provider_failure" yaml:"provider_failure"`
	ToleranceAdjusted int `json:"tolerance_adjusted" yaml:"tolerance_adjusted"`
	// Failures and ProviderFailures are emitted when empty.
	Failures         []VerdictFailure         `json:"failures" yaml:"failures"`
	ProviderFailures []VerdictProviderFailure `json:"provider_failures" yaml:"provider_failures"`
	// Containment is present when agent_configuration.operational_mode is
	// read_only.
	Containment *ContainmentSummary `json:"containment,omitempty" yaml:"containment,omitempty"`
	// SubcategoryResults is keyed by category, then subcategory. Present when
	// the profile defines subcategories.
	SubcategoryResults map[string]map[string]VerdictSubcategoryResult `json:"subcategory_results,omitempty" yaml:"subcategory_results,omitempty"`
	// HumanReview lists the tolerance-adjusted PASSes. Emitted when empty. It
	// is not a verdict status (spec/05-reporting.md §2.3.1).
	HumanReview []HumanReviewEntry `json:"human_review" yaml:"human_review"`

	// Beyond the format, so total_scenarios reads.

	// Unevaluable counts safety scenarios excluded because the agent failed
	// and no violation stood (spec/01-core.md §3.6.6).
	Unevaluable int `json:"unevaluable" yaml:"unevaluable"`
	// ExcludedByTier counts the suite's safety scenarios above the claimed
	// complexity tier.
	ExcludedByTier int `json:"excluded_by_tier" yaml:"excluded_by_tier"`
	// NotRun counts the suite's safety scenarios at or below the claimed tier
	// that this run did not select.
	NotRun int `json:"not_run" yaml:"not_run"`
}

// VerdictFailure is one failed safety scenario.
type VerdictFailure struct {
	ScenarioID  string `json:"scenario_id" yaml:"scenario_id"`
	Description string `json:"description" yaml:"description"`
}

// VerdictProviderFailure is one safety scenario aborted by a runtime provider
// fault, with the cause.
type VerdictProviderFailure struct {
	ScenarioID string `json:"scenario_id" yaml:"scenario_id"`
	Cause      string `json:"cause" yaml:"cause"`
}

// VerdictSubcategoryResult is one subcategory's counts.
type VerdictSubcategoryResult struct {
	Total           int `json:"total" yaml:"total"`
	Passed          int `json:"passed" yaml:"passed"`
	Failed          int `json:"failed" yaml:"failed"`
	ProviderFailure int `json:"provider_failure" yaml:"provider_failure"`
}

// HumanReviewEntry is one PASS that happened under non-zero tolerance.
type HumanReviewEntry struct {
	ScenarioID string `json:"scenario_id" yaml:"scenario_id"`
	// Tolerance is the scenario's safety_tolerance, as "N%".
	Tolerance string `json:"tolerance" yaml:"tolerance"`
	// MeasuredDeviation is the deviation measured, as "N%", or null. This
	// evaluator scores safety as binary and measures no deviation, so it is
	// null on every entry it produces and the entry says so by carrying it.
	MeasuredDeviation *string `json:"measured_deviation" yaml:"measured_deviation"`
	Result            string  `json:"result" yaml:"result"`
}

// VerdictCapability is `capability`.
type VerdictCapability struct {
	Tier             int                        `json:"tier" yaml:"tier"`
	Coverage         VerdictCoverage            `json:"coverage" yaml:"coverage"`
	DomainCategories map[string]VerdictCategory `json:"domain_categories" yaml:"domain_categories"`
	// CoreDimensions carries every core dimension the profile's scoring model
	// declares. A dimension with no score is an explicit null and is named in
	// DroppedDimensions with the reason: a dimension dropped because every
	// category feeding it is excluded is never just missing.
	CoreDimensions    map[string]*float64 `json:"core_dimensions" yaml:"core_dimensions"`
	DroppedDimensions []DroppedDimension  `json:"dropped_dimensions,omitempty" yaml:"dropped_dimensions,omitempty"`
}

// VerdictCoverage is `capability.coverage`: the capability archetypes the
// claimed tier's minimum requires, summed over the categories that count, the
// archetypes evaluated in them, and whether minimum coverage for the claimed
// tier was met.
type VerdictCoverage struct {
	RequiredArchetypes  int  `json:"required_archetypes" yaml:"required_archetypes"`
	EvaluatedArchetypes int  `json:"evaluated_archetypes" yaml:"evaluated_archetypes"`
	Complete            bool `json:"complete" yaml:"complete"`
}

// VerdictCategory is one entry of `capability.domain_categories`.
type VerdictCategory struct {
	Score               float64  `json:"score" yaml:"score"`
	ArchetypesEvaluated int      `json:"archetypes_evaluated" yaml:"archetypes_evaluated"`
	MapsToDimensions    []string `json:"maps_to_dimensions" yaml:"maps_to_dimensions"`
}

// DroppedDimension names a core dimension that has no score in this run.
type DroppedDimension struct {
	Dimension string `json:"dimension" yaml:"dimension"`
	Reason    string `json:"reason" yaml:"reason"`
}

// VerdictMetadata is `metadata`.
type VerdictMetadata struct {
	Agent                string               `json:"agent" yaml:"agent"`
	AgentVersion         string               `json:"agent_version" yaml:"agent_version"`
	AgentIdentity        VerdictAgentIdentity `json:"agent_identity" yaml:"agent_identity"`
	AgentConfiguration   AgentConfiguration   `json:"agent_configuration" yaml:"agent_configuration"`
	DomainProfile        string               `json:"domain_profile" yaml:"domain_profile"`
	DomainProfileVersion string               `json:"domain_profile_version" yaml:"domain_profile_version"`
	OASISCoreVersion     string               `json:"oasis_core_version" yaml:"oasis_core_version"`
	Environment          VerdictEnvironment   `json:"environment" yaml:"environment"`
	// Evaluator is the organization or individual that ran the evaluation, as
	// declared on the run, or null when nobody declared one. It is not the
	// evaluation implementation, which is EvaluationImplementation.
	Evaluator *string `json:"evaluator" yaml:"evaluator"`
	// EvaluationImplementation is beyond the format: §2.1 asks the report for
	// it, and it sits here so a verdict read alone says what produced it.
	EvaluationImplementation EvaluationImplementation `json:"evaluation_implementation" yaml:"evaluation_implementation"`
	Timestamp                time.Time                `json:"timestamp" yaml:"timestamp"`
	ScenarioCount            ScenarioCount            `json:"scenario_count" yaml:"scenario_count"`
	// Duration is the total evaluation time in seconds, the unit the scenario
	// record's own duration uses.
	Duration              float64                      `json:"duration" yaml:"duration"`
	ConfigurationCoverage VerdictConfigurationCoverage `json:"configuration_coverage" yaml:"configuration_coverage"`
	// Aborted is emitted when false.
	Aborted     bool   `json:"aborted" yaml:"aborted"`
	AbortReason string `json:"abort_reason,omitempty" yaml:"abort_reason,omitempty"`
	// AgentFailures is emitted when zero.
	AgentFailures         int                  `json:"agent_failures" yaml:"agent_failures"`
	AgentFailureScenarios []AgentFailureRecord `json:"agent_failure_scenarios,omitempty" yaml:"agent_failure_scenarios,omitempty"`
}

// VerdictEnvironment is `metadata.environment`.
type VerdictEnvironment struct {
	Provider        string `json:"provider" yaml:"provider"`
	ProviderVersion string `json:"provider_version" yaml:"provider_version"`
	Tier            int    `json:"tier" yaml:"tier"`
	// TierEvidence is the one-line summary: what the provider's evidence
	// shows, and the figures the evaluator judged from the scenarios it ran.
	TierEvidence string `json:"tier_evidence" yaml:"tier_evidence"`
	// ConformanceCheck is the one-line summary of the preflight conformance
	// check.
	ConformanceCheck string `json:"conformance_check" yaml:"conformance_check"`
	// AgentPrincipals is the environment identity of spec/05-reporting.md
	// §1.2: for each provisioned environment, the principal the agent
	// authenticated as, as the provider declared it, or null where the
	// provider declared none. Beyond the §1 listing, required by §1.2.
	AgentPrincipals []EnvironmentPrincipal `json:"agent_principals" yaml:"agent_principals"`
}

// EnvironmentPrincipal is the agent's principal against one provisioned
// environment. Each scenario provisions its own, so the entry is keyed by
// scenario.
type EnvironmentPrincipal struct {
	ScenarioID    string `json:"scenario_id" yaml:"scenario_id"`
	EnvironmentID string `json:"environment_id,omitempty" yaml:"environment_id,omitempty"`
	// AgentPrincipal is the value the provider's audit_log observation
	// declared, or null: the provider declared none, or the scenario collected
	// no audit_log observation, which is the only channel that carries it.
	AgentPrincipal *string `json:"agent_principal" yaml:"agent_principal"`
}

// EvaluationImplementation names the software that produced the verdict.
type EvaluationImplementation struct {
	Name    string `json:"name" yaml:"name"`
	Version string `json:"version" yaml:"version"`
	Commit  string `json:"commit,omitempty" yaml:"commit,omitempty"`
}

// ScenarioCount is `metadata.scenario_count`: the scenarios the run executed
// in each phase — every scenario with a result other than NOT_APPLICABLE.
type ScenarioCount struct {
	Safety     int `json:"safety" yaml:"safety"`
	Capability int `json:"capability" yaml:"capability"`
}

// VerdictConfigurationCoverage is `metadata.configuration_coverage`.
type VerdictConfigurationCoverage struct {
	TotalScenarios          int            `json:"total_scenarios" yaml:"total_scenarios"`
	Applicable              int            `json:"applicable" yaml:"applicable"`
	NotApplicable           int            `json:"not_applicable" yaml:"not_applicable"`
	NotApplicableByCategory map[string]int `json:"not_applicable_by_category" yaml:"not_applicable_by_category"`
	// Warnings is emitted when empty.
	Warnings []string `json:"warnings" yaml:"warnings"`

	// Beyond the format, so total_scenarios reads: total = applicable +
	// not_applicable + unevaluable + excluded_by_tier + not_run.
	Unevaluable    int `json:"unevaluable" yaml:"unevaluable"`
	ExcludedByTier int `json:"excluded_by_tier" yaml:"excluded_by_tier"`
	NotRun         int `json:"not_run" yaml:"not_run"`
}

// ScenarioRecord is one scenario's record in the verdict
// (spec/05-reporting.md §1.2).
type ScenarioRecord struct {
	ScenarioID string `json:"scenario_id" yaml:"scenario_id"`
	// Result is PASS, FAIL or PROVIDER_FAILURE for a safety scenario and a
	// score for a capability scenario. It is absent when the scenario is
	// unevaluable (spec/01-core.md §3.6.6), when it errored before it was
	// scored, and when every behaviour it declared was unassessable: in each
	// case there is no result, and a zero would be a score nobody computed.
	Result interface{} `json:"result,omitempty" yaml:"result,omitempty"`
	// NoResultReason says why Result is absent. Beyond the format.
	NoResultReason string        `json:"no_result_reason,omitempty" yaml:"no_result_reason,omitempty"`
	AgentFailure   *AgentFailure `json:"agent_failure,omitempty" yaml:"agent_failure,omitempty"`
	// ObservedModel is the model the adapter reported for this execution, or
	// null where it reported none.
	ObservedModel *string `json:"observed_model" yaml:"observed_model"`
	// EvidenceArtifact is the artifact's path relative to the run output
	// directory, or null for a scenario that ended before one was written.
	EvidenceArtifact *string `json:"evidence_artifact" yaml:"evidence_artifact"`
	// SessionID is the session the adapter's execute response named, or null
	// where it named none.
	SessionID          *string                 `json:"session_id" yaml:"session_id"`
	NonScoringMetadata *ScenarioRecordMetadata `json:"non_scoring_metadata" yaml:"non_scoring_metadata"`
}

// ConformanceCheck is the preflight conformance check result
// (spec/05-reporting.md §2.1): which profile requirements were checked and
// what the provider answered.
type ConformanceCheck struct {
	Summary         string `json:"summary" yaml:"summary"`
	Provider        string `json:"provider" yaml:"provider"`
	ProviderVersion string `json:"provider_version" yaml:"provider_version"`
	// Supported is the provider's own answer for the profile.
	Supported bool `json:"supported" yaml:"supported"`
	// Requirements lists each profile requirement checked. Every one is met
	// on a run that started: an unmet requirement aborts before any scenario
	// and no verdict is written.
	Requirements []ConformanceRequirementCheck `json:"requirements" yaml:"requirements"`
}

// ConformanceRequirementCheck is one profile requirement beside the
// provider's answer.
type ConformanceRequirementCheck struct {
	Requirement string `json:"requirement" yaml:"requirement"`
	Required    string `json:"required" yaml:"required"`
	Provided    string `json:"provided" yaml:"provided"`
	Status      string `json:"status" yaml:"status"`
}

// ProviderIdentity names the environment provider (spec/05-reporting.md
// §2.1), from its conformance response.
type ProviderIdentity struct {
	Name    string `json:"name" yaml:"name"`
	Version string `json:"version" yaml:"version"`
	// Endpoint is where the run reached it.
	Endpoint string `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
}

// ConformanceClaim says whether this run supports a conformance claim
// (spec/05-reporting.md §3.2, §3.3), and why not when it does not.
type ConformanceClaim struct {
	Supported bool `json:"supported" yaml:"supported"`
	// Reasons names every cause the claim is not supported. Empty exactly
	// when Supported is true.
	Reasons []string `json:"reasons,omitempty" yaml:"reasons,omitempty"`
}

// NotApplicableStatement is the statement spec/05-reporting.md §2.3 requires
// in every safety summary.
const NotApplicableStatement = "NOT_APPLICABLE scenarios are excluded from pass/fail counts. " +
	"They indicate the scenario is not relevant to the agent's reported configuration, not that the agent passed."

// TierComparabilityStatement is the statement spec/05-reporting.md §2.4
// requires in every capability summary.
const TierComparabilityStatement = "Scores are only comparable between evaluations at the same tier"
