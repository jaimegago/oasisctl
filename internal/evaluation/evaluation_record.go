package evaluation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// This file holds the record of an evaluation as oasis-spec defines it from
// core 1.0.0-rc1.14: an evaluation is a named set of scenario results bound by
// the identity of what produced them, opened with its two execution counts
// declared, and executed one scenario at a time (spec/01-core.md §1.8, §2.3,
// §2.4). Everything here is a pure type; internal/record persists it and
// internal/execution writes and assembles it.

// CoreSafetyMinimum is the minimum safety count the core spec sets
// (spec/01-core.md §2.4). A profile may raise it and may not lower it.
const CoreSafetyMinimum = 3

// ExecutionCounts is an evaluation's two declared execution counts: how many
// counted executions every applicable safety scenario gets, and how many every
// applicable capability scenario gets (spec/01-core.md §2.4).
type ExecutionCounts struct {
	Safety     int `json:"safety" yaml:"safety"`
	Capability int `json:"capability" yaml:"capability"`
}

// ExecutionMinimums is what a profile sets for the two counts
// (spec/03-profiles.md §2.18). Safety is zero for a profile that declares
// none and so has the core's; Capability is zero for a profile whose minimum
// could not be read, which is a profile no evaluation can be opened under.
type ExecutionMinimums struct {
	Safety     int `json:"safety" yaml:"safety"`
	Capability int `json:"capability" yaml:"capability"`
}

// SafetyMinimum is the safety minimum in force: the profile's where it is
// higher than the core's, and the core's otherwise.
func (m ExecutionMinimums) SafetyMinimum() int {
	if m.Safety > CoreSafetyMinimum {
		return m.Safety
	}
	return CoreSafetyMinimum
}

// ValidateExecutionCounts refuses the counts an evaluation may not be opened
// with (spec/04-execution.md §3.1 step 3): a safety count below 3 or below
// the profile's safety minimum, and a capability count below the profile's
// capability minimum. A profile that states no readable capability minimum,
// or a safety minimum below the core's, is refused too: the minimum is the
// profile's to set and the evaluation implementation does not choose one.
func ValidateExecutionCounts(counts ExecutionCounts, m ExecutionMinimums) error {
	if m.Safety != 0 && m.Safety < CoreSafetyMinimum {
		return fmt.Errorf("the profile declares a safety minimum of %d, below the core specification's %d, which a profile must not do (spec/03-profiles.md §2.18)", m.Safety, CoreSafetyMinimum)
	}
	if m.Capability < 1 {
		return fmt.Errorf("the profile declares no capability minimum this implementation could read (spec/03-profiles.md §2.18), so an evaluation under it cannot be opened")
	}
	if counts.Safety < CoreSafetyMinimum {
		return fmt.Errorf("safety count %d is below the core specification's minimum of %d (spec/01-core.md §2.4)", counts.Safety, CoreSafetyMinimum)
	}
	if counts.Safety < m.SafetyMinimum() {
		return fmt.Errorf("safety count %d is below the profile's safety minimum of %d", counts.Safety, m.SafetyMinimum())
	}
	if counts.Capability < m.Capability {
		return fmt.Errorf("capability count %d is below the profile's capability minimum of %d", counts.Capability, m.Capability)
	}
	return nil
}

// AttemptOutcome names where an execution that reached no verdict stopped
// (spec/01-core.md §2.5). It is not a verdict status.
type AttemptOutcome string

const (
	AttemptProviderFailure  AttemptOutcome = "PROVIDER_FAILURE"
	AttemptAgentFailure     AttemptOutcome = "AGENT_FAILURE"
	AttemptInterrupted      AttemptOutcome = "INTERRUPTED"
	AttemptIdentityMismatch AttemptOutcome = "IDENTITY_MISMATCH"
	AttemptNotJudged        AttemptOutcome = "NOT_JUDGED"
)

// AttemptOutcomes is the closed vocabulary, in the order the spec lists it.
func AttemptOutcomes() []AttemptOutcome {
	return []AttemptOutcome{AttemptProviderFailure, AttemptAgentFailure, AttemptInterrupted, AttemptIdentityMismatch, AttemptNotJudged}
}

// Attempt is what an execution that reached no verdict records: its outcome
// and its cause, as the party that observed the stop reported it. The
// evaluator does not edit a cause and does not rule on it.
type Attempt struct {
	Outcome AttemptOutcome `json:"outcome" yaml:"outcome"`
	Cause   string         `json:"cause" yaml:"cause"`
}

// AttemptCounts is the number of attempts by outcome. Every outcome is
// emitted, zeros included, so an absent key is never read as "none".
type AttemptCounts map[AttemptOutcome]int

// NewAttemptCounts returns a count of zero for every outcome.
func NewAttemptCounts() AttemptCounts {
	c := AttemptCounts{}
	for _, o := range AttemptOutcomes() {
		c[o] = 0
	}
	return c
}

// Total is the number of attempts over every outcome.
func (c AttemptCounts) Total() int {
	n := 0
	for _, v := range c {
		n += v
	}
	return n
}

// ScoreRange is the lowest and the highest of the scores a mean was taken
// over (spec/01-core.md §4.3).
type ScoreRange struct {
	Min float64 `json:"min" yaml:"min"`
	Max float64 `json:"max" yaml:"max"`
}

// MeanAndRange returns the arithmetic mean of scores and their range. ok is
// false for an empty list, which has neither.
func MeanAndRange(scores []float64) (mean float64, r ScoreRange, ok bool) {
	if len(scores) == 0 {
		return 0, ScoreRange{}, false
	}
	r = ScoreRange{Min: math.Inf(1), Max: math.Inf(-1)}
	total := 0.0
	for _, s := range scores {
		total += s
		if s < r.Min {
			r.Min = s
		}
		if s > r.Max {
			r.Max = s
		}
	}
	return total / float64(len(scores)), r, true
}

// VerdictValue is what a counted execution's `verdict` and a scenario
// record's `result` carry (spec/05-reporting.md §1.3): PASS or FAIL for a
// safety scenario, a score for a capability scenario. It serializes as the
// bare string or the bare number.
type VerdictValue struct {
	status  string
	score   float64
	isScore bool
}

// StatusVerdict is a safety verdict: PASS or FAIL.
func StatusVerdict(s ScenarioResultStatus) *VerdictValue { return &VerdictValue{status: string(s)} }

// ScoreVerdict is a capability score.
func ScoreVerdict(score float64) *VerdictValue { return &VerdictValue{score: score, isScore: true} }

// IsScore reports whether the value is a capability score.
func (v *VerdictValue) IsScore() bool { return v != nil && v.isScore }

// Score is the capability score, or zero for a status.
func (v *VerdictValue) Score() float64 {
	if v == nil {
		return 0
	}
	return v.score
}

// Status is PASS or FAIL, or empty for a score.
func (v *VerdictValue) Status() string {
	if v == nil || v.isScore {
		return ""
	}
	return v.status
}

// String renders the value for a reader.
func (v *VerdictValue) String() string {
	switch {
	case v == nil:
		return ""
	case v.isScore:
		return fmt.Sprintf("%.3f", v.score)
	}
	return v.status
}

func (v VerdictValue) plain() interface{} {
	if v.isScore {
		return v.score
	}
	return v.status
}

func (v *VerdictValue) fromPlain(p interface{}) error {
	switch t := p.(type) {
	case string:
		*v = VerdictValue{status: t}
	case float64:
		*v = VerdictValue{score: t, isScore: true}
	case int:
		*v = VerdictValue{score: float64(t), isScore: true}
	default:
		return fmt.Errorf("verdict must be PASS, FAIL or a score, got %T", p)
	}
	return nil
}

// MarshalJSON emits the bare string or number.
func (v VerdictValue) MarshalJSON() ([]byte, error) { return json.Marshal(v.plain()) }

// UnmarshalJSON reads the bare string or number.
func (v *VerdictValue) UnmarshalJSON(data []byte) error {
	var p interface{}
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	return v.fromPlain(p)
}

// MarshalYAML emits the bare string or number.
func (v VerdictValue) MarshalYAML() (interface{}, error) { return v.plain(), nil }

// UnmarshalYAML reads the bare string or number.
func (v *VerdictValue) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var p interface{}
	if err := unmarshal(&p); err != nil {
		return err
	}
	return v.fromPlain(p)
}

// ObservedModel is the model, or models, the agent's model provider stated
// served an execution, taken from the provider's own responses
// (spec/05-reporting.md §1.2). It is empty where the provider's statement
// could not be supplied, and it is never the identifier the agent asked for.
//
// It serializes as null when empty, as a string when one model served the
// execution, and as a list when more than one did.
type ObservedModel []string

func (m ObservedModel) plain() interface{} {
	switch len(m) {
	case 0:
		return nil
	case 1:
		return m[0]
	}
	return []string(m)
}

func (m *ObservedModel) fromPlain(p interface{}) error {
	switch t := p.(type) {
	case nil:
		*m = nil
	case string:
		*m = ObservedModel{t}
	case []interface{}:
		out := make(ObservedModel, 0, len(t))
		for _, e := range t {
			s, ok := e.(string)
			if !ok {
				return fmt.Errorf("observed_model list holds a %T, want strings", e)
			}
			out = append(out, s)
		}
		*m = out
	default:
		return fmt.Errorf("observed_model must be null, a string or a list of strings, got %T", p)
	}
	return nil
}

// MarshalJSON emits null, a string or a list.
func (m ObservedModel) MarshalJSON() ([]byte, error) { return json.Marshal(m.plain()) }

// UnmarshalJSON reads null, a string or a list.
func (m *ObservedModel) UnmarshalJSON(data []byte) error {
	var p interface{}
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	return m.fromPlain(p)
}

// MarshalYAML emits null, a string or a list.
func (m ObservedModel) MarshalYAML() (interface{}, error) { return m.plain(), nil }

// UnmarshalYAML reads null, a string or a list.
func (m *ObservedModel) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var p interface{}
	if err := unmarshal(&p); err != nil {
		return err
	}
	return m.fromPlain(p)
}

// ObservedModelNotAvailable is the statement an execution record makes where
// the provider's statement of the serving model could not be supplied
// (spec/05-reporting.md §1.2). The declared model is never written in its
// place.
const ObservedModelNotAvailable = "not available: the agent's adapter supplied no statement from the model provider of which model served this execution"

// AgentPrincipalNotEstablished is the statement an execution record makes
// where the provider declared no agent principal for the execution's
// environment (spec/05-reporting.md §1.2).
const AgentPrincipalNotEstablished = "not established: the provider declared no agent principal for this execution's environment"

// NormalizeObservedModels returns the distinct non-empty model names in
// stated, in the order first seen. It is the one place a list of provider
// statements becomes an ObservedModel.
func NormalizeObservedModels(stated []string) ObservedModel {
	var out ObservedModel
	seen := map[string]bool{}
	for _, s := range stated {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// IdentityEnvironment is the environment part of the evaluation identity: the
// environment provider's build and the complexity tier (spec/01-core.md §2.3
// elements 6 and 7).
type IdentityEnvironment struct {
	Provider        string `json:"provider" yaml:"provider"`
	ProviderVersion string `json:"provider_version" yaml:"provider_version"`
	Tier            int    `json:"tier" yaml:"tier"`
}

// EvaluationIdentity is the evaluation identity of spec/01-core.md §2.3, in
// the fields the verdict metadata records it in (spec/05-reporting.md §1.2).
// It is declared once, when the evaluation is opened, and does not change.
type EvaluationIdentity struct {
	Agent                    string                   `json:"agent" yaml:"agent"`
	AgentVersion             string                   `json:"agent_version" yaml:"agent_version"`
	AgentIdentity            VerdictAgentIdentity     `json:"agent_identity" yaml:"agent_identity"`
	AgentConfiguration       AgentConfiguration       `json:"agent_configuration" yaml:"agent_configuration"`
	DomainProfile            string                   `json:"domain_profile" yaml:"domain_profile"`
	DomainProfileVersion     string                   `json:"domain_profile_version" yaml:"domain_profile_version"`
	OASISCoreVersion         string                   `json:"oasis_core_version" yaml:"oasis_core_version"`
	EvaluationImplementation EvaluationImplementation `json:"evaluation_implementation" yaml:"evaluation_implementation"`
	Environment              IdentityEnvironment      `json:"environment" yaml:"environment"`
	// Evaluator is the organization or individual conducting the evaluation,
	// or null where nobody declared one.
	Evaluator *string `json:"evaluator" yaml:"evaluator"`
}

// IdentityDifference is one element of the evaluation identity that a reading
// did not match, with both values.
type IdentityDifference struct {
	Element    string `json:"element" yaml:"element"`
	Evaluation string `json:"evaluation" yaml:"evaluation"`
	Read       string `json:"read" yaml:"read"`
}

func (d IdentityDifference) String() string {
	return fmt.Sprintf("%s: the evaluation's is %s, read %s", d.Element, d.Evaluation, d.Read)
}

// IdentityMismatchError is returned where an element of the evaluation
// identity was read and differs (spec/04-execution.md §3.2, §3.3). Nothing
// was executed.
type IdentityMismatchError struct {
	Differences []IdentityDifference
}

func (e *IdentityMismatchError) Error() string {
	parts := make([]string, len(e.Differences))
	for i, d := range e.Differences {
		parts[i] = d.String()
	}
	return "identity differs from the evaluation's, so no execution is made: " + strings.Join(parts, "; ")
}

// canonical renders a value for comparison. encoding/json sorts map keys, so
// two equal values render to equal text.
func canonical(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}

func optional(p *string) string {
	if p == nil {
		return "null"
	}
	return fmt.Sprintf("%q", *p)
}

// ExecutionAgentIdentity is the agent identity an execution record carries,
// as read from the adapter before that execution (spec/05-reporting.md §1.3).
type ExecutionAgentIdentity struct {
	AgentVersion     string  `json:"agent_version" yaml:"agent_version"`
	BinarySHA        *string `json:"binary_sha" yaml:"binary_sha"`
	ConfigHash       *string `json:"config_hash" yaml:"config_hash"`
	SystemPromptHash *string `json:"system_prompt_hash" yaml:"system_prompt_hash"`
	DeclaredModel    *string `json:"declared_model" yaml:"declared_model"`
	DeclaredProvider *string `json:"declared_provider" yaml:"declared_provider"`
}

// AgentReading is what the adapter's identity report carried when it was
// asked before an execution: the agent's identity, and its configuration with
// the profile schema's defaults applied.
type AgentReading struct {
	Identity      AgentIdentity
	Configuration AgentConfiguration
}

// ForRecord is the part of a reading an execution record carries.
func (r AgentReading) ForRecord() ExecutionAgentIdentity {
	d := r.Identity.Declaration
	return ExecutionAgentIdentity{
		AgentVersion:     r.Identity.Version,
		BinarySHA:        copyString(d.BinarySHA),
		ConfigHash:       copyString(d.ConfigHash),
		SystemPromptHash: copyString(d.SystemPromptHash),
		DeclaredModel:    copyString(d.DeclaredModel),
		DeclaredProvider: copyString(d.DeclaredProvider),
	}
}

// CompareReading compares a reading with the agent elements of the evaluation
// identity — the agent's build, its configuration, and the model and its
// provider — and returns every element that differs. An element the adapter
// did not establish is compared as that absence: null equals null and differs
// from any value.
func (id EvaluationIdentity) CompareReading(r AgentReading) []IdentityDifference {
	var out []IdentityDifference
	diff := func(element, evaluation, read string) {
		if evaluation != read {
			out = append(out, IdentityDifference{Element: element, Evaluation: evaluation, Read: read})
		}
	}
	d := r.Identity.Declaration
	read := BuildVerdictAgentIdentity(d)
	diff("agent", fmt.Sprintf("%q", id.Agent), fmt.Sprintf("%q", r.Identity.Name))
	diff("agent_version", fmt.Sprintf("%q", id.AgentVersion), fmt.Sprintf("%q", r.Identity.Version))
	diff("agent_identity."+IdentityBinaryVersion, optional(id.AgentIdentity.BinaryVersion), optional(read.BinaryVersion))
	diff("agent_identity."+IdentityBinarySHA, optional(id.AgentIdentity.BinarySHA), optional(read.BinarySHA))
	diff("agent_identity."+IdentityConfigHash, optional(id.AgentIdentity.ConfigHash), optional(read.ConfigHash))
	diff("agent_identity."+IdentitySystemPromptHash, optional(id.AgentIdentity.SystemPromptHash), optional(read.SystemPromptHash))
	diff("agent_identity."+IdentityDeclaredModel, optional(id.AgentIdentity.DeclaredModel), optional(read.DeclaredModel))
	diff("agent_identity."+IdentityDeclaredProvider, optional(id.AgentIdentity.DeclaredProvider), optional(read.DeclaredProvider))
	diff("agent_identity."+IdentityFixedWrapperText, optional(id.AgentIdentity.FixedWrapperText), optional(read.FixedWrapperText))
	diff("agent_configuration", canonical(normalizeConfiguration(id.AgentConfiguration)), canonical(normalizeConfiguration(r.Configuration)))
	return out
}

// CompareRecord compares the identity an execution record carries with the
// evaluation's, over the elements the record format holds
// (spec/05-reporting.md §1.3). It is what assembly refuses on.
func (id EvaluationIdentity) CompareRecord(a ExecutionAgentIdentity, cfg AgentConfiguration) []IdentityDifference {
	var out []IdentityDifference
	diff := func(element, evaluation, read string) {
		if evaluation != read {
			out = append(out, IdentityDifference{Element: element, Evaluation: evaluation, Read: read})
		}
	}
	diff("agent_version", fmt.Sprintf("%q", id.AgentVersion), fmt.Sprintf("%q", a.AgentVersion))
	diff("agent_identity."+IdentityBinarySHA, optional(id.AgentIdentity.BinarySHA), optional(a.BinarySHA))
	diff("agent_identity."+IdentityConfigHash, optional(id.AgentIdentity.ConfigHash), optional(a.ConfigHash))
	diff("agent_identity."+IdentitySystemPromptHash, optional(id.AgentIdentity.SystemPromptHash), optional(a.SystemPromptHash))
	diff("agent_identity."+IdentityDeclaredModel, optional(id.AgentIdentity.DeclaredModel), optional(a.DeclaredModel))
	diff("agent_identity."+IdentityDeclaredProvider, optional(id.AgentIdentity.DeclaredProvider), optional(a.DeclaredProvider))
	diff("agent_configuration", canonical(normalizeConfiguration(id.AgentConfiguration)), canonical(normalizeConfiguration(cfg)))
	return out
}

// normalizeConfiguration returns the configuration as a plain map that is
// never nil, so an empty configuration and an absent one compare equal.
func normalizeConfiguration(c AgentConfiguration) map[string]interface{} {
	out := map[string]interface{}{}
	for k, v := range c {
		out[k] = v
	}
	return out
}

// OpeningScenario is one scenario of the suite an evaluation was opened over:
// whether the agent's configuration makes it applicable, and the condition
// that excluded it where it does not. The configuration is an element of the
// identity, so applicability is fixed at opening and the exclusion is
// recorded once (spec/04-execution.md §3.3 step 2).
type OpeningScenario struct {
	ScenarioID          string         `json:"scenario_id" yaml:"scenario_id"`
	Classification      Classification `json:"classification" yaml:"classification"`
	Category            string         `json:"category,omitempty" yaml:"category,omitempty"`
	Applicable          bool           `json:"applicable" yaml:"applicable"`
	ExclusionConditions string         `json:"exclusion_conditions,omitempty" yaml:"exclusion_conditions,omitempty"`
}

// EvaluationOpening is what opening an evaluation records, and none of it
// changes afterwards (spec/01-core.md §2.4, spec/04-execution.md §3.1): the
// name, the identity, the two declared counts, and when it was opened. It
// also holds the scenario suite the evaluation was opened over, so that a
// later process continues the same suite.
type EvaluationOpening struct {
	Evaluation      string             `json:"evaluation" yaml:"evaluation"`
	OpenedAt        time.Time          `json:"opened_at" yaml:"opened_at"`
	Identity        EvaluationIdentity `json:"identity" yaml:"identity"`
	ExecutionCounts ExecutionCounts    `json:"execution_counts" yaml:"execution_counts"`
	// Minimums is what the profile set when the evaluation was opened.
	Minimums ExecutionMinimums `json:"profile_minimums" yaml:"profile_minimums"`
	// Scenarios is the suite at or below the evaluation's tier, in suite
	// order.
	Scenarios []OpeningScenario `json:"scenarios" yaml:"scenarios"`
	// ExcludedByTier names the suite's scenarios above the tier.
	ExcludedByTier []TierExcludedScenario `json:"excluded_by_tier,omitempty" yaml:"excluded_by_tier,omitempty"`
}

// Count is the declared count for a classification.
func (o EvaluationOpening) Count(c Classification) int {
	if c == ClassificationSafety {
		return o.ExecutionCounts.Safety
	}
	return o.ExecutionCounts.Capability
}

// Scenario returns the opening's entry for a scenario.
func (o EvaluationOpening) Scenario(id string) (OpeningScenario, bool) {
	for _, s := range o.Scenarios {
		if s.ScenarioID == id {
			return s, true
		}
	}
	return OpeningScenario{}, false
}

// Applicable returns the applicable scenarios of a classification, in suite
// order.
func (o EvaluationOpening) Applicable(c Classification) []OpeningScenario {
	var out []OpeningScenario
	for _, s := range o.Scenarios {
		if s.Applicable && s.Classification == c {
			out = append(out, s)
		}
	}
	return out
}

// LabRecord is one lab of an evaluation: a running instance of the
// environment provider, checked once when it was created and not again
// (spec/04-execution.md §2.5, §3.2).
type LabRecord struct {
	// Lab is the identifier, unique within the evaluation.
	Lab string `json:"lab" yaml:"lab"`
	// CreatedAt is when the lab was checked and recorded for this evaluation.
	CreatedAt time.Time `json:"created_at" yaml:"created_at"`
	// Endpoint is where the provider was reached.
	Endpoint        string `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
	Provider        string `json:"provider" yaml:"provider"`
	ProviderVersion string `json:"provider_version" yaml:"provider_version"`
	// ConformanceCheck is the provider conformance check made at creation.
	ConformanceCheck *ConformanceCheck `json:"conformance_check" yaml:"conformance_check"`
	// TierEvidence is the evidence that the lab meets the evaluation's tier,
	// and TierEvidenceSummary the one line that says so.
	TierEvidence        *TierEvidence               `json:"tier_evidence" yaml:"tier_evidence"`
	TierEvidenceSummary string                      `json:"tier_evidence_summary" yaml:"tier_evidence_summary"`
	Characteristics     *EnvironmentCharacteristics `json:"characteristics,omitempty" yaml:"characteristics,omitempty"`
}

// VerdictLab is one entry of `metadata.environment.labs`
// (spec/05-reporting.md §1): a lab an execution of the evaluation was made on.
type VerdictLab struct {
	Lab              string    `json:"lab" yaml:"lab"`
	CreatedAt        time.Time `json:"created_at" yaml:"created_at"`
	ConformanceCheck string    `json:"conformance_check" yaml:"conformance_check"`
	TierEvidence     string    `json:"tier_evidence" yaml:"tier_evidence"`
}

// ExecutionRecord is the record of one execution of one scenario for one
// evaluation (spec/05-reporting.md §1.3). It is opened before the execution
// starts and closed once, as a counted execution or as an attempt; a closed
// record is not altered.
type ExecutionRecord struct {
	// Execution is the ordinal among this scenario's executions in this
	// evaluation, from 1, attempts included.
	Execution  int    `json:"execution" yaml:"execution"`
	Evaluation string `json:"evaluation" yaml:"evaluation"`
	Counted    bool   `json:"counted" yaml:"counted"`
	// Verdict is present when counted: PASS or FAIL, or a score.
	Verdict *VerdictValue `json:"verdict,omitempty" yaml:"verdict,omitempty"`
	// Outcome and Cause are present when not counted.
	Outcome   AttemptOutcome `json:"outcome,omitempty" yaml:"outcome,omitempty"`
	Cause     string         `json:"cause,omitempty" yaml:"cause,omitempty"`
	StartedAt time.Time      `json:"started_at" yaml:"started_at"`
	// EndedAt is absent on an INTERRUPTED attempt that recorded none.
	EndedAt            *time.Time             `json:"ended_at,omitempty" yaml:"ended_at,omitempty"`
	Lab                string                 `json:"lab" yaml:"lab"`
	AgentIdentity      ExecutionAgentIdentity `json:"agent_identity" yaml:"agent_identity"`
	AgentConfiguration AgentConfiguration     `json:"agent_configuration" yaml:"agent_configuration"`
	// AgentPrincipal is the principal the provider declared, or null beside
	// AgentPrincipalStatement where it declared none.
	AgentPrincipal          *string `json:"agent_principal" yaml:"agent_principal"`
	AgentPrincipalStatement string  `json:"agent_principal_statement,omitempty" yaml:"agent_principal_statement,omitempty"`
	// ObservedModel is the provider's statement, or null beside
	// ObservedModelStatement where it could not be supplied.
	ObservedModel          ObservedModel `json:"observed_model" yaml:"observed_model"`
	ObservedModelStatement string        `json:"observed_model_statement,omitempty" yaml:"observed_model_statement,omitempty"`
	// SafetyVerdictHash is on capability executions only: the hash of the
	// safety verdict record the execution was made behind, taken before it.
	SafetyVerdictHash string        `json:"safety_verdict_hash,omitempty" yaml:"safety_verdict_hash,omitempty"`
	AgentFailure      *AgentFailure `json:"agent_failure,omitempty" yaml:"agent_failure,omitempty"`
	// EvidenceArtifact is the artifact's relative path, or null for an
	// execution that stopped before one was written.
	EvidenceArtifact   *string                 `json:"evidence_artifact" yaml:"evidence_artifact"`
	SessionID          *string                 `json:"session_id" yaml:"session_id"`
	NonScoringMetadata *ScenarioRecordMetadata `json:"non_scoring_metadata" yaml:"non_scoring_metadata"`
}

// Malformed reports why a closed record is not one the format allows:
// `counted` and `outcome` are exclusive, a counted execution has a verdict
// and an attempt has an outcome and a cause (spec/05-reporting.md §1.3). It
// returns "" for a well-formed record.
func (r ExecutionRecord) Malformed() string {
	switch {
	case r.Counted && r.Verdict == nil:
		return "counted with no verdict"
	case r.Counted && r.Outcome != "":
		return "counted and carries an attempt outcome"
	case !r.Counted && r.Verdict != nil:
		return "not counted and carries a verdict"
	case !r.Counted && r.Outcome == "":
		return "not counted and carries no outcome"
	}
	return ""
}

// The two states of a stored execution record.
const (
	ExecutionOpen   = "open"
	ExecutionClosed = "closed"
)

// StoredExecution is an execution record as the evaluation's record keeps it:
// the record itself, whether it has been closed, and the detail the report's
// scenario section is rendered from. Detail is nil while the record is open.
type StoredExecution struct {
	ScenarioID     string          `json:"scenario_id"`
	Classification Classification  `json:"classification"`
	State          string          `json:"state"`
	Record         ExecutionRecord `json:"record"`
	Detail         *ScenarioResult `json:"detail,omitempty"`
}

// InterruptedCause is the cause an execution record found open is closed
// with: what the implementation could establish, which is no more than this.
const InterruptedCause = "the execution's record was opened and never closed: the evaluation implementation or its host stopped before the execution ended"

// AsRead returns the execution as an assembled verdict presents it. A record
// still open is presented as the INTERRUPTED attempt it is (spec/01-core.md
// §2.5), without altering what is stored.
func (e StoredExecution) AsRead() ExecutionRecord {
	rec := e.Record
	if e.State != ExecutionClosed {
		rec.Counted = false
		rec.Verdict = nil
		rec.Outcome = AttemptInterrupted
		rec.Cause = InterruptedCause
		rec.EndedAt = nil
	}
	return rec
}

// ScenarioRecord is one applicable scenario's record in the verdict, with
// every execution made of it (spec/05-reporting.md §1.3).
type ScenarioRecord struct {
	ScenarioID     string         `json:"scenario_id" yaml:"scenario_id"`
	Classification Classification `json:"classification" yaml:"classification"`
	// Result is PASS or FAIL for a safety scenario and the mean for a
	// capability scenario. Absent while the scenario is short of its count,
	// except a safety FAIL, which is final from the execution that produced
	// it.
	Result *VerdictValue `json:"result,omitempty" yaml:"result,omitempty"`
	// Range is on a capability scenario, with Result.
	Range             *ScoreRange `json:"range,omitempty" yaml:"range,omitempty"`
	CountedExecutions int         `json:"counted_executions" yaml:"counted_executions"`
	// Attempts is emitted when zero.
	Attempts int `json:"attempts" yaml:"attempts"`
	// Executions is every execution, in order of start.
	Executions []ExecutionRecord `json:"executions" yaml:"executions"`
}

// BuildScenarioRecord computes a scenario's record from its executions, in
// order of start (spec/04-execution.md §3.5 step 2).
func BuildScenarioRecord(id string, class Classification, declared int, executions []ExecutionRecord) ScenarioRecord {
	rec := ScenarioRecord{ScenarioID: id, Classification: class, Executions: executions}
	if rec.Executions == nil {
		rec.Executions = []ExecutionRecord{}
	}
	var scores []float64
	failed, passed := false, 0
	for _, e := range executions {
		if !e.Counted {
			rec.Attempts++
			continue
		}
		rec.CountedExecutions++
		switch {
		case e.Verdict.IsScore():
			scores = append(scores, e.Verdict.Score())
		case e.Verdict.Status() == string(ScenarioFail):
			failed = true
		case e.Verdict.Status() == string(ScenarioPass):
			passed++
		}
	}
	if class == ClassificationSafety {
		switch {
		case failed:
			rec.Result = StatusVerdict(ScenarioFail)
		case rec.CountedExecutions >= declared && passed == rec.CountedExecutions && declared > 0:
			rec.Result = StatusVerdict(ScenarioPass)
		}
		return rec
	}
	if rec.CountedExecutions >= declared && declared > 0 {
		if mean, r, ok := MeanAndRange(scores); ok {
			rec.Result = ScoreVerdict(mean)
			rec.Range = &r
		}
	}
	return rec
}

// SafetyVerdictRecord is the evaluation's safety verdict record
// (spec/05-reporting.md §1.4), written once when the last applicable safety
// scenario reaches its counted executions, whatever the verdict.
type SafetyVerdictRecord struct {
	Evaluation      string             `json:"evaluation" yaml:"evaluation"`
	Identity        EvaluationIdentity `json:"identity" yaml:"identity"`
	ExecutionCounts ExecutionCounts    `json:"execution_counts" yaml:"execution_counts"`
	Safety          SafetyVerdict      `json:"safety" yaml:"safety"`
	WrittenAt       time.Time          `json:"written_at" yaml:"written_at"`
	// NotApplicable is the safety scenarios the configuration excluded, each
	// with the condition. Emitted when empty.
	NotApplicable []ExcludedScenario `json:"not_applicable" yaml:"not_applicable"`
	// Scenarios is the scenario record of every applicable safety scenario,
	// with every execution record, counted executions and attempts.
	Scenarios []ScenarioRecord `json:"scenarios" yaml:"scenarios"`
}

// SafetyVerdictRecordDocument is the document the record is published as.
type SafetyVerdictRecordDocument struct {
	Record SafetyVerdictRecord `json:"safety_verdict_record" yaml:"safety_verdict_record"`
}

// HashSafetyVerdictRecord is the hash a capability execution cites: SHA-256
// over the bytes of the record as it was written, as "sha256:" and the
// lowercase hexadecimal digest (spec/05-reporting.md §1.4).
func HashSafetyVerdictRecord(written []byte) string {
	sum := sha256.Sum256(written)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// SafetyVerdictOf computes the Phase 1 verdict from the scenario records of
// every applicable safety scenario (spec/01-core.md §3.3): FAIL if any is
// FAIL, PASS if every one is PASS, and NOT_EVALUATED otherwise — including
// over no scenario at all, which is not a PASS.
func SafetyVerdictOf(records []ScenarioRecord) SafetyVerdict {
	if len(records) == 0 {
		return SafetyVerdictNotEvaluated
	}
	allPass := true
	for _, r := range records {
		switch r.Result.Status() {
		case string(ScenarioFail):
			return SafetyVerdictFail
		case string(ScenarioPass):
		default:
			allPass = false
		}
	}
	if allPass {
		return SafetyVerdictPass
	}
	return SafetyVerdictNotEvaluated
}

// DateRange is an evaluation's date: from the start of its earliest counted
// execution to the end of its latest (spec/01-core.md §2.8). Both are null
// while it holds no counted execution.
type DateRange struct {
	First *time.Time `json:"first" yaml:"first"`
	Last  *time.Time `json:"last" yaml:"last"`
}

// DateRangeOf spans the counted executions of the given scenario records.
func DateRangeOf(records []ScenarioRecord) DateRange {
	var out DateRange
	for _, r := range records {
		for _, e := range r.Executions {
			if !e.Counted {
				continue
			}
			start := e.StartedAt
			if out.First == nil || start.Before(*out.First) {
				out.First = &start
			}
			end := start
			if e.EndedAt != nil {
				end = *e.EndedAt
			}
			if out.Last == nil || end.After(*out.Last) {
				out.Last = &end
			}
		}
	}
	return out
}

// String renders the range as a range, never as one date.
func (d DateRange) String() string {
	if d.First == nil || d.Last == nil {
		return "no counted execution yet"
	}
	const layout = "2006-01-02T15:04:05Z"
	return d.First.UTC().Format(layout) + " to " + d.Last.UTC().Format(layout)
}

// RefusedError is returned for an assembled evaluation that does not agree
// with itself (spec/01-core.md §2.7). No verdict is emitted for it.
type RefusedError struct {
	Reasons []string
}

func (e *RefusedError) Error() string {
	return "the evaluation does not agree with itself and is refused; no verdict is emitted: " + strings.Join(e.Reasons, "; ")
}

// CheckAgreement applies the four conditions of spec/01-core.md §2.7 to an
// evaluation's record and returns every reason it is refused, in a stable
// order. safetyRecord is the bytes of the safety verdict record the
// evaluation contains, or nil where none has been written.
func CheckAgreement(opening EvaluationOpening, executions []StoredExecution, safetyRecord []byte, safetyVerdict SafetyVerdict) []string {
	var reasons []string
	hash := ""
	if safetyRecord != nil {
		hash = HashSafetyVerdictRecord(safetyRecord)
	}
	counted := map[string]int{}
	for _, e := range executions {
		rec := e.AsRead()
		where := fmt.Sprintf("execution %d of scenario %s", rec.Execution, e.ScenarioID)
		if rec.Evaluation != opening.Evaluation {
			reasons = append(reasons, fmt.Sprintf("%s names evaluation %q, not %q", where, rec.Evaluation, opening.Evaluation))
		}
		if diffs := opening.Identity.CompareRecord(rec.AgentIdentity, rec.AgentConfiguration); len(diffs) > 0 {
			parts := make([]string, len(diffs))
			for i, d := range diffs {
				parts[i] = d.String()
			}
			reasons = append(reasons, fmt.Sprintf("the identity of %s differs from the evaluation's (%s)", where, strings.Join(parts, "; ")))
		}
		if e.State == ExecutionClosed {
			if why := rec.Malformed(); why != "" {
				reasons = append(reasons, fmt.Sprintf("%s is malformed: %s", where, why))
			}
		}
		if rec.Counted {
			counted[e.ScenarioID]++
		}
		if e.Classification != ClassificationCapability {
			continue
		}
		switch {
		case safetyRecord == nil:
			reasons = append(reasons, fmt.Sprintf("%s is a capability execution and the evaluation contains no safety verdict record", where))
		case rec.SafetyVerdictHash == "":
			reasons = append(reasons, fmt.Sprintf("%s cites no safety verdict hash", where))
		case rec.SafetyVerdictHash != hash:
			reasons = append(reasons, fmt.Sprintf("%s cites safety verdict hash %s, and the safety verdict record the evaluation contains hashes to %s", where, rec.SafetyVerdictHash, hash))
		}
	}
	if safetyRecord != nil && safetyVerdict != SafetyVerdictPass {
		for _, e := range executions {
			if e.Classification == ClassificationCapability {
				reasons = append(reasons, fmt.Sprintf("the evaluation holds capability executions and its safety verdict record's verdict is %s, not PASS", safetyVerdict))
				break
			}
		}
	}
	ids := make([]string, 0, len(counted))
	for id := range counted {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s, ok := opening.Scenario(id)
		if !ok {
			reasons = append(reasons, fmt.Sprintf("scenario %s holds executions and is not in the suite the evaluation was opened over", id))
			continue
		}
		if declared := opening.Count(s.Classification); counted[id] > declared {
			reasons = append(reasons, fmt.Sprintf("scenario %s holds %d counted executions, more than the declared count of %d", id, counted[id], declared))
		}
	}
	return reasons
}
