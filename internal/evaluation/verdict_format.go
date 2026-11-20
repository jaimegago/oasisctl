package evaluation

import (
	"time"
)

// StandardVerdict is the evaluation verdict in the format of
// spec/05-reporting.md §1, field for field. It is emitted under the `verdict`
// key of the output document; the evaluation report of §2 is the rest of that
// document. It is assembled from the evaluation's record and may be assembled
// while the evaluation is incomplete, in which case it says so.
//
// Every key the format names is emitted on every verdict, with the
// exceptions the format itself states: `capability` is present only when
// safety is PASS, `verdict_record` and `verdict_record_hash` once the safety
// verdict record is written, and `containment` and `subcategory_results` by
// their condition. A value that could not be established is an explicit null
// beside a field naming it, never an invented value.
// TestVerdictConformsToSection1 holds a produced verdict to the shape.
//
// Keys beyond the format (excluded_by_tier, dropped_dimensions, and the like)
// are additions the format's own totals need in order to read; none replaces
// a key the format names.
type StandardVerdict struct {
	// Evaluation is the name assigned when the evaluation was opened.
	Evaluation string `json:"evaluation" yaml:"evaluation"`
	// Complete is whether no execution remains to be made (spec/01-core.md
	// §2.7). False is rendered as "incomplete".
	Complete      bool                 `json:"complete" yaml:"complete"`
	Safety        SafetyVerdict        `json:"safety" yaml:"safety"`
	SafetyDetails VerdictSafetyDetails `json:"safety_details" yaml:"safety_details"`
	// Capability is present only when Safety is PASS.
	Capability *VerdictCapability `json:"capability,omitempty" yaml:"capability,omitempty"`
	// Scenarios holds one record per applicable scenario, safety first, each
	// with every execution made of it (spec/05-reporting.md §1.3).
	Scenarios []ScenarioRecord `json:"scenarios" yaml:"scenarios"`
	Metadata  VerdictMetadata  `json:"metadata" yaml:"metadata"`
}

// VerdictSafetyDetails is `safety_details`.
type VerdictSafetyDetails struct {
	// ExecutionsPerScenario is the declared safety count.
	ExecutionsPerScenario int `json:"executions_per_scenario" yaml:"executions_per_scenario"`
	// TotalScenarios counts the suite's safety scenarios, whatever became of
	// each: total = applicable + not_applicable + excluded_by_tier.
	TotalScenarios int `json:"total_scenarios" yaml:"total_scenarios"`
	Applicable     int `json:"applicable" yaml:"applicable"`
	NotApplicable  int `json:"not_applicable" yaml:"not_applicable"`
	// Passed counts scenarios holding their count, every counted execution
	// PASS; Failed those with any counted execution FAIL; ShortOfCount the
	// applicable scenarios holding fewer counted executions than declared,
	// none of them FAIL.
	Passed       int `json:"passed" yaml:"passed"`
	Failed       int `json:"failed" yaml:"failed"`
	ShortOfCount int `json:"short_of_count" yaml:"short_of_count"`
	// CountedExecutions is over all applicable safety scenarios, and Attempts
	// the safety executions that reached no verdict.
	CountedExecutions int `json:"counted_executions" yaml:"counted_executions"`
	Attempts          int `json:"attempts" yaml:"attempts"`
	// ToleranceAdjusted counts the counted executions that PASSed under
	// non-zero tolerance.
	ToleranceAdjusted int `json:"tolerance_adjusted" yaml:"tolerance_adjusted"`
	// Failures is emitted when empty.
	Failures []VerdictFailure `json:"failures" yaml:"failures"`
	// VerdictRecord and VerdictRecordHash are present once the safety verdict
	// record is written (spec/05-reporting.md §1.4).
	VerdictRecord     string `json:"verdict_record,omitempty" yaml:"verdict_record,omitempty"`
	VerdictRecordHash string `json:"verdict_record_hash,omitempty" yaml:"verdict_record_hash,omitempty"`
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

	// ExcludedByTier counts the suite's safety scenarios above the
	// evaluation's complexity tier.
	ExcludedByTier int `json:"excluded_by_tier" yaml:"excluded_by_tier"`
}

// VerdictFailure is one failed safety scenario.
type VerdictFailure struct {
	ScenarioID  string `json:"scenario_id" yaml:"scenario_id"`
	Description string `json:"description" yaml:"description"`
}

// VerdictSubcategoryResult is one subcategory's counts.
type VerdictSubcategoryResult struct {
	Total        int `json:"total" yaml:"total"`
	Passed       int `json:"passed" yaml:"passed"`
	Failed       int `json:"failed" yaml:"failed"`
	ShortOfCount int `json:"short_of_count" yaml:"short_of_count"`
}

// HumanReviewEntry is one counted execution that PASSed under non-zero
// tolerance.
type HumanReviewEntry struct {
	ScenarioID string `json:"scenario_id" yaml:"scenario_id"`
	// Execution is the execution's ordinal among the scenario's executions.
	Execution int `json:"execution" yaml:"execution"`
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
	// ExecutionsPerScenario is the declared capability count.
	ExecutionsPerScenario int             `json:"executions_per_scenario" yaml:"executions_per_scenario"`
	Tier                  int             `json:"tier" yaml:"tier"`
	Coverage              VerdictCoverage `json:"coverage" yaml:"coverage"`
	// CountedExecutions is over all applicable capability scenarios, and
	// Attempts the capability executions that reached no verdict.
	CountedExecutions int `json:"counted_executions" yaml:"counted_executions"`
	Attempts          int `json:"attempts" yaml:"attempts"`
	// CompleteSets is the number of sets of paired executions the scores
	// below are taken over (spec/01-core.md §4.3). It equals
	// ExecutionsPerScenario once the evaluation is complete.
	CompleteSets     int                        `json:"complete_sets" yaml:"complete_sets"`
	DomainCategories map[string]VerdictCategory `json:"domain_categories" yaml:"domain_categories"`
	// CoreDimensions carries every core dimension the profile's scoring model
	// declares. A dimension with no score is an explicit null and is named in
	// DroppedDimensions with the reason: a dimension dropped because every
	// category feeding it is excluded is never just missing.
	CoreDimensions    map[string]*VerdictDimension `json:"core_dimensions" yaml:"core_dimensions"`
	DroppedDimensions []DroppedDimension           `json:"dropped_dimensions,omitempty" yaml:"dropped_dimensions,omitempty"`
}

// VerdictCoverage is `capability.coverage`: the capability archetypes the
// evaluation's tier minimum requires, summed over the categories that count,
// the archetypes evaluated in them, and whether minimum coverage for the tier
// was met.
type VerdictCoverage struct {
	RequiredArchetypes  int  `json:"required_archetypes" yaml:"required_archetypes"`
	EvaluatedArchetypes int  `json:"evaluated_archetypes" yaml:"evaluated_archetypes"`
	Complete            bool `json:"complete" yaml:"complete"`
}

// VerdictCategory is one entry of `capability.domain_categories`: the mean of
// the per-set category scores, with their range. The range is part of the
// score and no category score is carried without it.
type VerdictCategory struct {
	Score               float64    `json:"score" yaml:"score"`
	Range               ScoreRange `json:"range" yaml:"range"`
	ArchetypesEvaluated int        `json:"archetypes_evaluated" yaml:"archetypes_evaluated"`
	MapsToDimensions    []string   `json:"maps_to_dimensions" yaml:"maps_to_dimensions"`
}

// VerdictDimension is one entry of `capability.core_dimensions`: the mean of
// the per-set scores, with their range.
type VerdictDimension struct {
	Score float64    `json:"score" yaml:"score"`
	Range ScoreRange `json:"range" yaml:"range"`
}

// DroppedDimension names a core dimension that has no score.
type DroppedDimension struct {
	Dimension string `json:"dimension" yaml:"dimension"`
	Reason    string `json:"reason" yaml:"reason"`
}

// VerdictMetadata is `metadata`.
type VerdictMetadata struct {
	Agent                    string                   `json:"agent" yaml:"agent"`
	AgentVersion             string                   `json:"agent_version" yaml:"agent_version"`
	AgentIdentity            VerdictAgentIdentity     `json:"agent_identity" yaml:"agent_identity"`
	AgentConfiguration       AgentConfiguration       `json:"agent_configuration" yaml:"agent_configuration"`
	DomainProfile            string                   `json:"domain_profile" yaml:"domain_profile"`
	DomainProfileVersion     string                   `json:"domain_profile_version" yaml:"domain_profile_version"`
	OASISCoreVersion         string                   `json:"oasis_core_version" yaml:"oasis_core_version"`
	EvaluationImplementation EvaluationImplementation `json:"evaluation_implementation" yaml:"evaluation_implementation"`
	Environment              VerdictEnvironment       `json:"environment" yaml:"environment"`
	// Evaluator is the organization or individual conducting the evaluation,
	// as declared when it was opened, or null when nobody declared one.
	Evaluator *string   `json:"evaluator" yaml:"evaluator"`
	OpenedAt  time.Time `json:"opened_at" yaml:"opened_at"`
	// DateRange is the evaluation's date: there is no single timestamp and no
	// single duration (spec/01-core.md §2.8).
	DateRange DateRange `json:"date_range" yaml:"date_range"`
	// ExecutionCounts is the two counts as declared at opening.
	ExecutionCounts ExecutionCounts `json:"execution_counts" yaml:"execution_counts"`
	// ScenarioCount is the applicable scenarios of each classification.
	ScenarioCount         ScenarioCount                `json:"scenario_count" yaml:"scenario_count"`
	ConfigurationCoverage VerdictConfigurationCoverage `json:"configuration_coverage" yaml:"configuration_coverage"`
	// Attempts is evaluation-wide, both phases: the executions that reached no
	// verdict. Emitted when zero, and AttemptsByOutcome in full.
	Attempts          int           `json:"attempts" yaml:"attempts"`
	AttemptsByOutcome AttemptCounts `json:"attempts_by_outcome" yaml:"attempts_by_outcome"`
}

// VerdictEnvironment is `metadata.environment`.
type VerdictEnvironment struct {
	Provider        string `json:"provider" yaml:"provider"`
	ProviderVersion string `json:"provider_version" yaml:"provider_version"`
	Tier            int    `json:"tier" yaml:"tier"`
	// Labs is every lab an execution of this evaluation was made on. Emitted
	// when empty.
	Labs []VerdictLab `json:"labs" yaml:"labs"`
	// ScenarioFigures is beyond the format: the tier figures the evaluator
	// judged from the scenarios executed, in one line. They are not a lab's
	// evidence, so they are not in a lab's entry.
	ScenarioFigures string `json:"scenario_figures,omitempty" yaml:"scenario_figures,omitempty"`
}

// EvaluationImplementation names the software that produced the verdict, and
// the commit it was built from. Its build is an element of the evaluation
// identity (spec/01-core.md §2.3).
type EvaluationImplementation struct {
	Name    string `json:"name" yaml:"name"`
	Version string `json:"version" yaml:"version"`
	Build   string `json:"build" yaml:"build"`
}

// ScenarioCount is `metadata.scenario_count`: the applicable scenarios of
// each classification.
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
	// not_applicable + excluded_by_tier.
	ExcludedByTier int `json:"excluded_by_tier" yaml:"excluded_by_tier"`
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

// ConformanceClaim says whether this evaluation supports a conformance claim
// (spec/05-reporting.md §3.2, §3.3), why not when it does not, and what the
// claim states when it does.
type ConformanceClaim struct {
	Supported bool `json:"supported" yaml:"supported"`
	// Reasons names every cause the claim is not supported. Empty exactly
	// when Supported is true.
	Reasons []string `json:"reasons,omitempty" yaml:"reasons,omitempty"`
	// Claim is what a conformance claim resting on this evaluation states.
	// Present exactly when Supported is true.
	Claim *ConformanceClaimStatement `json:"claim,omitempty" yaml:"claim,omitempty"`
}

// ConformanceClaimStatement is the content of a conformance claim
// (spec/05-reporting.md §3.2). It names one evaluation, which is complete,
// and states both execution counts: a claim that omits either is not one.
type ConformanceClaimStatement struct {
	Evaluation           string             `json:"evaluation" yaml:"evaluation"`
	DomainProfile        string             `json:"domain_profile" yaml:"domain_profile"`
	DomainProfileVersion string             `json:"domain_profile_version" yaml:"domain_profile_version"`
	Tier                 int                `json:"tier" yaml:"tier"`
	Agent                string             `json:"agent" yaml:"agent"`
	AgentVersion         string             `json:"agent_version" yaml:"agent_version"`
	AgentConfiguration   AgentConfiguration `json:"agent_configuration" yaml:"agent_configuration"`
	// DateRange is the date of evaluation, as a range: the first and the last
	// counted execution.
	DateRange DateRange     `json:"date_range" yaml:"date_range"`
	Verdict   SafetyVerdict `json:"verdict" yaml:"verdict"`
	// ApplicableSafetyScenarios is `safety_details.applicable`.
	ApplicableSafetyScenarios int `json:"applicable_safety_scenarios" yaml:"applicable_safety_scenarios"`
	// SafetyCount and CapabilityCount are the declared execution counts. On a
	// safety FAIL no capability execution was made, and the capability count
	// stated is the one the evaluation declared.
	SafetyCount     int `json:"safety_count" yaml:"safety_count"`
	CapabilityCount int `json:"capability_count" yaml:"capability_count"`
	// Text is the claim in one sentence.
	Text string `json:"text" yaml:"text"`
}

// NotApplicableStatement is the statement spec/05-reporting.md §2.3 requires
// in every safety summary.
const NotApplicableStatement = "NOT_APPLICABLE scenarios are excluded from pass/fail counts. " +
	"They indicate the scenario is not relevant to the agent's reported configuration, not that the agent passed."

// TierComparabilityStatement is the statement spec/05-reporting.md §2.4
// requires in every capability summary.
const TierComparabilityStatement = "Scores are only comparable between evaluations at the same tier"
