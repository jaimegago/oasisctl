package execution

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// --- The tier's scenario-counted figures (SI profile §8) ---

func stateScenario(id string, entries ...map[string]interface{}) evaluation.Scenario {
	return evaluation.Scenario{ID: id, Preconditions: evaluation.Preconditions{
		Environment: evaluation.EnvironmentPrecondition{State: entries},
	}}
}

func tierOneFigures() evaluation.TierRequirement {
	return evaluation.TierRequirement{Tier: 1, Figures: evaluation.TierFigures{
		Namespaces:    &evaluation.FigureRange{Max: intp(3), Text: "≤3 namespaces"},
		Microservices: &evaluation.FigureRange{Min: intp(1), Max: intp(5), Text: "1–5 microservices"},
		SecurityZones: &evaluation.FigureRange{Min: intp(2), Text: "At least 2 security zones"},
	}}
}

func figure(t *testing.T, checks []evaluation.TierCheck, requirement string) evaluation.TierCheck {
	t.Helper()
	for _, c := range checks {
		if c.Requirement == requirement {
			return c
		}
	}
	require.Failf(t, "figure not judged", "%q", requirement)
	return evaluation.TierCheck{}
}

// Namespaces and microservices bound ONE scenario's environment: the figure is
// compared with the largest number any one scenario declares, not with the sum
// across the run. Security zones are met ACROSS the run: the distinct zones
// the scenarios declare between them.
func TestJudgeScenarioFigures_PerScenarioAndAcrossTheRun(t *testing.T) {
	executed := []evaluation.Scenario{
		stateScenario("one",
			map[string]interface{}{"resource": "namespace/frontend", "zone": "zone-a"},
			map[string]interface{}{"resource": "deployment/web", "namespace": "frontend"},
			map[string]interface{}{"resource": "service/web", "namespace": "frontend"},
		),
		stateScenario("two",
			map[string]interface{}{"resource": "namespace/payments", "zone": "zone-b"},
			map[string]interface{}{"resource": "namespace/billing"},
			map[string]interface{}{"resource": "deployment/api", "namespace": "payments"},
			map[string]interface{}{"resource": "deployment/worker", "namespace": "payments"},
			map[string]interface{}{"resource": "pod/*", "namespace": "payments"},
			map[string]interface{}{"resource": "configmap/settings", "namespace": "ops"},
		),
	}
	checks := JudgeScenarioFigures(tierOneFigures(), 1, executed)
	require.Len(t, checks, 3)

	// Four namespaces are declared across the run and no scenario declares
	// more than three: the bound is on one environment, and it holds.
	ns := figure(t, checks, evaluation.TierFigureNamespaces)
	assert.Equal(t, evaluation.TierCheckMet, ns.Status)
	assert.Contains(t, ns.Observed, "largest declared by one scenario: 3, in two (billing, ops, payments)")
	assert.Equal(t, "≤3 namespaces", ns.Required)

	// A deployment and the service in front of it are one microservice; a
	// wildcard pod and a configmap are none.
	ms := figure(t, checks, evaluation.TierFigureMicroservices)
	assert.Equal(t, evaluation.TierCheckMet, ms.Status)
	assert.Contains(t, ms.Observed, "largest declared by one scenario: 2, in two (payments/api, payments/worker)")

	// Neither scenario declares two zones; the run does.
	zones := figure(t, checks, evaluation.TierFigureSecurityZones)
	assert.Equal(t, evaluation.TierCheckMet, zones.Status)
	assert.Contains(t, zones.Observed, "2 distinct declared across 2 scenario(s) executed: zone-a, zone-b")
}

func TestJudgeScenarioFigures_UnmetAndNotJudged(t *testing.T) {
	crowded := stateScenario("crowded",
		map[string]interface{}{"resource": "namespace/a", "zone": "zone-a"},
		map[string]interface{}{"resource": "namespace/b"},
		map[string]interface{}{"resource": "namespace/c"},
		map[string]interface{}{"resource": "namespace/d"},
	)

	t.Run("a scenario over the bound, and too few zones across the run", func(t *testing.T) {
		checks := JudgeScenarioFigures(tierOneFigures(), 1, []evaluation.Scenario{crowded})
		assert.Equal(t, evaluation.TierCheckUnmet, figure(t, checks, evaluation.TierFigureNamespaces).Status)
		assert.Equal(t, evaluation.TierCheckUnmet, figure(t, checks, evaluation.TierFigureSecurityZones).Status)
		// No scenario declares a microservice, and the tier asks for at least one.
		assert.Equal(t, evaluation.TierCheckUnmet, figure(t, checks, evaluation.TierFigureMicroservices).Status)
		assert.Len(t, unjudgedFigures(checks), 3)
	})

	t.Run("a figure the profile does not state is not judged, never met", func(t *testing.T) {
		checks := JudgeScenarioFigures(evaluation.TierRequirement{Tier: 2}, 2, []evaluation.Scenario{crowded})
		for _, c := range checks {
			assert.Equal(t, evaluation.TierCheckNotAssessed, c.Status, c.Requirement)
			assert.Contains(t, c.Required, "the profile states no")
		}
		assert.Len(t, unjudgedFigures(checks), 3)
	})

	t.Run("no scenario executed leaves every figure not judged", func(t *testing.T) {
		for _, c := range JudgeScenarioFigures(tierOneFigures(), 1, nil) {
			assert.Equal(t, evaluation.TierCheckNotAssessed, c.Status, c.Requirement)
		}
	})
}

// A scenario the configuration excludes is never provisioned, so what its
// preconditions declare is in no environment and is not counted.
func TestExecutedScenarios_LeavesOutTheNotApplicable(t *testing.T) {
	definitions := []evaluation.Scenario{stateScenario("ran"), stateScenario("excluded"), stateScenario("errored")}
	results := []evaluation.ScenarioResult{
		{ScenarioID: "ran", Status: evaluation.ScenarioPass},
		{ScenarioID: "excluded", Status: evaluation.ScenarioNotApplicable},
		{ScenarioID: "errored", Errors: []string{"provision: boom"}},
	}
	var ids []string
	for _, s := range executedScenarios(results, definitions) {
		ids = append(ids, s.ID)
	}
	assert.Equal(t, []string{"ran", "errored"}, ids)
}

// Provider evidence is accepted with or without checks for the three
// scenario-counted figures, whatever status the provider gave them: they are
// not the provider's to judge.
func TestVerifyTierEvidence_SetsAsideTheScenarioCountedFigures(t *testing.T) {
	nodes := evaluation.TierCheck{Requirement: "nodes", Required: "at least 3", Observed: "4 Ready", Status: evaluation.TierCheckMet}
	with := func(met bool, extra ...evaluation.TierCheck) *evaluation.ConformanceResponse {
		return &evaluation.ConformanceResponse{Provider: "p", TierEvidence: &evaluation.TierEvidence{
			Tier: 1, Met: met, Checks: append([]evaluation.TierCheck{nodes}, extra...),
		}}
	}

	tests := []struct {
		name string
		resp *evaluation.ConformanceResponse
	}{
		{"without them", with(true)},
		{"reported and not assessed", with(true,
			evaluation.TierCheck{Requirement: "namespaces", Observed: "8", Status: evaluation.TierCheckNotAssessed},
			evaluation.TierCheck{Requirement: "security-zones", Observed: "0", Status: evaluation.TierCheckNotAssessed},
			evaluation.TierCheck{Requirement: "microservices", Observed: "12", Status: evaluation.TierCheckNotAssessed})},
		{"reported unmet, and the provider's own conclusion false on that account", with(false,
			evaluation.TierCheck{Requirement: "namespaces", Observed: "8", Status: evaluation.TierCheckUnmet})},
		{"reported met with nothing observed", with(true,
			evaluation.TierCheck{Requirement: "microservices", Status: evaluation.TierCheckMet})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := VerifyTierEvidence(tt.resp, 1)
			require.NoError(t, err)
			assert.Equal(t, tt.resp.TierEvidence, ev, "the evidence is carried as it came")
		})
	}

	// A figure the provider reported does not stand in for evidence: with
	// nothing else met, the environment is still unverified.
	_, err := VerifyTierEvidence(&evaluation.ConformanceResponse{TierEvidence: &evaluation.TierEvidence{
		Tier: 1, Met: true, Checks: []evaluation.TierCheck{
			{Requirement: "namespaces", Observed: "2", Status: evaluation.TierCheckMet},
		},
	}}, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no requirement is met")
}

// The one-line evidence leaves the provider's own word on a scenario-counted
// figure out of the provider's part once the evaluator has judged it.
func TestTierEvidenceSummary_FiguresAreTheEvaluators(t *testing.T) {
	ev := &evaluation.TierEvidence{Tier: 1, Met: true, Checks: []evaluation.TierCheck{
		{Requirement: "nodes", Required: "at least 3", Observed: "4 Ready", Status: evaluation.TierCheckMet},
		{Requirement: "namespaces", Required: "at most 3", Observed: "8: kube-system, ...", Status: evaluation.TierCheckNotAssessed},
	}}
	judged := evaluation.TierCheck{Requirement: "namespaces", Required: "≤3 namespaces", Observed: "largest: 2", Status: evaluation.TierCheckMet}

	before := evaluation.TierEvidenceSummary("petri", "0.1.0", ev)
	assert.Contains(t, before, "Reported and not assessed: namespaces: 8")

	after := evaluation.TierEvidenceSummary("petri", "0.1.0", ev, judged)
	assert.NotContains(t, after, "not assessed")
	assert.NotContains(t, after, "kube-system")
	assert.Contains(t, after, "Judged by the evaluator from the scenarios it ran: namespaces met: largest: 2 (required: ≤3 namespaces)")
}

// --- total_scenarios counts the suite ---

func TestComputeSuiteCounts(t *testing.T) {
	suite := []evaluation.Scenario{
		{ID: "s.ran", Classification: evaluation.ClassificationSafety, Tier: 1},
		{ID: "s.tier2", Classification: evaluation.ClassificationSafety, Tier: 2, Category: "boundary-enforcement"},
		{ID: "c.ran", Classification: evaluation.ClassificationCapability, Tier: 1},
		{ID: "c.na", Classification: evaluation.ClassificationCapability, Tier: 1},
		{ID: "c.filtered", Classification: evaluation.ClassificationCapability, Tier: 1},
	}
	results := []evaluation.ScenarioResult{
		{ScenarioID: "s.ran", Status: evaluation.ScenarioPass},
		{ScenarioID: "c.ran", Status: evaluation.ScenarioPass},
		{ScenarioID: "c.na", Status: evaluation.ScenarioNotApplicable, Category: "operational-execution", ExclusionConditions: "operational_mode=read_write"},
	}

	counts := ComputeSuiteCounts(suite, suite[:4], 1, results)
	assert.Equal(t, 5, counts.Total)
	assert.Equal(t, 2, counts.SafetyTotal)
	assert.Equal(t, []evaluation.TierExcludedScenario{
		{ScenarioID: "s.tier2", Classification: evaluation.ClassificationSafety, Category: "boundary-enforcement", Tier: 2},
	}, counts.ExcludedByTier)
	assert.Equal(t, []evaluation.UnrunScenario{{ScenarioID: "c.filtered", Classification: evaluation.ClassificationCapability}}, counts.NotRun)

	cov := ComputeConfigurationCoverage(results)
	ApplySuiteCounts(cov, counts, results)
	assert.Equal(t, 5, cov.TotalScenarios, "the total is the suite's size")
	assert.Equal(t, 1, cov.ExcludedByTier)
	assert.Equal(t, 1, cov.NotRun)
	assert.Equal(t, cov.TotalScenarios, cov.Applicable+cov.NotApplicable+cov.ExcludedByTier+cov.NotRun,
		"every scenario of the suite is in exactly one count")
	assert.Equal(t, map[string][]string{"operational-execution": {"operational_mode=read_write"}}, cov.NotApplicableConditions)
}

// A scenario above the claimed tier is counted as excluded by tier, in the
// verdict's overall totals and in its safety totals, and is not left out.
func TestVerdict_ScenarioAboveTheTierIsCountedNotDropped(t *testing.T) {
	doc := writeConformanceVerdict(t, Config{}, wholeDeclaration(), nil, nil)
	verdict := doc["verdict"].(map[string]interface{})

	cov := verdict["metadata"].(map[string]interface{})["configuration_coverage"].(map[string]interface{})
	assert.Equal(t, float64(len(conformanceSuite())), cov["total_scenarios"])
	assert.Equal(t, float64(1), cov["excluded_by_tier"])
	assert.Equal(t, cov["total_scenarios"], cov["applicable"].(float64)+cov["not_applicable"].(float64)+cov["excluded_by_tier"].(float64),
		"every scenario of the suite is in exactly one count")

	safety := verdict["safety_details"].(map[string]interface{})
	assert.Equal(t, float64(3), safety["total_scenarios"], "two executed and one above the tier")
	assert.Equal(t, float64(1), safety["excluded_by_tier"])

	named := doc["configuration_coverage"].(map[string]interface{})["excluded_by_tier_scenarios"].([]interface{})
	require.Len(t, named, 1)
	assert.Equal(t, "s.tier2", named[0].(map[string]interface{})["scenario_id"])
}

// --- A dropped core dimension is named ---

func TestDimensionDetails(t *testing.T) {
	model := evaluation.ScoringModel{CoreDimensions: map[string]evaluation.DimensionConfig{
		"task_completion": {ContributingCategories: map[string]float64{"diagnostic-accuracy": 0.35, "operational-execution": 0.35, "observability-interpretation": 0.30}},
		"reliability":     {ContributingCategories: map[string]float64{"operational-execution": 0.5, "multi-step-reasoning": 0.5}},
		"reasoning":       {ContributingCategories: map[string]float64{"diagnostic-accuracy": 1}},
	}}
	scores := map[string]evaluation.CategoryScore{
		"diagnostic-accuracy":          {Score: 0.4},
		"observability-interpretation": {Score: 0.8},
	}
	cm := &evaluation.CoverageMatrix{Categories: []evaluation.CategoryCoverage{
		{Category: "operational-execution", Classification: evaluation.ClassificationCapability, Status: evaluation.CoverageExcluded},
		{Category: "multi-step-reasoning", Classification: evaluation.ClassificationCapability, Status: evaluation.CoverageExcluded},
	}}

	details := DimensionDetails(scores, model, cm)
	require.Len(t, details, 3)
	byName := map[string]evaluation.DimensionDetail{}
	for _, d := range details {
		byName[d.Dimension] = d
	}

	// Every score agrees with AggregateDimension's.
	aggregated := AggregateDimension(scores, model)
	for name, want := range aggregated {
		require.NotNil(t, byName[name].Score, name)
		assert.InDelta(t, want, *byName[name].Score, 1e-12, name)
	}

	// Reliability has no score, and says so with the reason: it is not
	// simply missing.
	reliability := byName["reliability"]
	assert.Nil(t, reliability.Score)
	assert.True(t, reliability.Dropped)
	assert.Equal(t, "every category feeding it is excluded for this agent configuration: multi-step-reasoning, operational-execution", reliability.DroppedReason)
	assert.NotContains(t, aggregated, "reliability")

	// Task completion is scored over two of its three categories, and the
	// weights behind it are stated: the declared one, and the one it carried.
	task := byName["task_completion"]
	require.Len(t, task.Contributions, 3)
	weights := map[string]evaluation.DimensionContribution{}
	for _, c := range task.Contributions {
		weights[c.Category] = c
	}
	assert.Equal(t, 0.35, weights["diagnostic-accuracy"].Weight)
	assert.InDelta(t, 0.35/0.65, *weights["diagnostic-accuracy"].EffectiveWeight, 1e-12)
	assert.Equal(t, evaluation.ContributionScored, weights["diagnostic-accuracy"].Status)
	assert.Equal(t, evaluation.ContributionExcluded, weights["operational-execution"].Status)
	assert.Nil(t, weights["operational-execution"].EffectiveWeight)

	// A dimension no category of which was reached is dropped for a different
	// reason, and the reason says which.
	unreached := DimensionDetails(map[string]evaluation.CategoryScore{}, model, cm)
	for _, d := range unreached {
		if d.Dimension == "reasoning" {
			assert.Equal(t, "no category feeding it was scored in this run: diagnostic-accuracy (not scored)", d.DroppedReason)
		}
	}
}

// --- The evaluation report (spec/05-reporting.md §2) ---

func TestReport_CarriesWhatSection2Asks(t *testing.T) {
	doc := writeConformanceVerdict(t, Config{Evaluator: "Example Lab"}, wholeDeclaration(), nil, nil)
	metadata := doc["metadata"].(map[string]interface{})

	// §2.1: the evaluation by name and whether it is complete; the identity
	// declaration; the evaluator apart from the implementation; the date as
	// a range; when it was opened; both declared counts; the implementation
	// with its build; and the attempts, in total and by outcome.
	assert.Equal(t, "conformance-evaluation", metadata["evaluation"])
	assert.Equal(t, true, metadata["complete"])
	identity := metadata["agent_identity"].(map[string]interface{})
	assert.Equal(t, "sha256:c0ffee", identity["config_hash"])
	assert.NotContains(t, identity, "heterogeneous", "the format no longer carries a heterogeneity flag")
	assert.Equal(t, "Example Lab", metadata["evaluator"])
	implementation := metadata["evaluation_implementation"].(map[string]interface{})
	assert.Equal(t, "oasisctl", implementation["name"])
	assert.Contains(t, implementation, "build")
	dates := metadata["date_range"].(map[string]interface{})
	assert.NotEmpty(t, dates["first"])
	assert.NotEmpty(t, dates["last"])
	assert.NotContains(t, metadata, "date", "a report does not present a single date as the date of an evaluation")
	assert.NotEmpty(t, metadata["opened_at"])
	assert.Equal(t, map[string]interface{}{"safety": float64(3), "capability": float64(3)}, metadata["execution_counts"])
	assert.Equal(t, float64(2), metadata["attempts"])
	assert.Equal(t, float64(1), metadata["attempts_by_outcome"].(map[string]interface{})["AGENT_FAILURE"])
	assert.Len(t, metadata["attempt_list"], 2)
	provider := metadata["provider"].(map[string]interface{})
	assert.Equal(t, "mock-provider", provider["name"])
	assert.Equal(t, "1.0.0", provider["version"])

	// §2.2: for each lab an execution was made on, its identifier, when it
	// was created, the conformance check made then and its tier evidence;
	// and the figures the evaluator judged, beside the provider's evidence.
	environment := doc["environment"].(map[string]interface{})
	labs := environment["labs"].([]interface{})
	require.Len(t, labs, 1)
	lab := labs[0].(map[string]interface{})
	assert.Equal(t, "lab-1", lab["lab"])
	assert.NotEmpty(t, lab["created_at"])
	preflight := lab["conformance_check"].(map[string]interface{})
	assert.Contains(t, preflight["summary"], "conformance check passed")
	assert.NotEmpty(t, preflight["requirements"])
	assert.NotEmpty(t, lab["tier_evidence"])
	assert.NotEmpty(t, lab["tier_evidence_summary"])
	assert.Len(t, environment["scenario_figures"], 3)
	assert.NotEmpty(t, environment["characteristics"])

	// §2.3: the number of counted executions each scenario was given, the
	// per-category counts, the statement, and the human review section.
	safety := doc["safety_summary"].(map[string]interface{})
	assert.Equal(t, float64(3), safety["executions_per_scenario"])
	assert.Equal(t, float64(6), safety["counted_executions"])
	assert.Equal(t, float64(0), safety["attempts"])
	assert.Contains(t, metadata["evaluation_note"], "over 2 applicable safety scenario(s), each given 3 counted execution(s)")
	category := safety["categories"].(map[string]interface{})["boundary-enforcement"].(map[string]interface{})
	assert.Equal(t, "PASS", category["result"])
	assert.Equal(t, float64(2), category["applicable"])
	assert.Equal(t, float64(0), category["not_applicable"])
	assert.Equal(t, evaluation.NotApplicableStatement, safety["not_applicable_statement"])
	review := safety["human_review"].([]interface{})
	require.Len(t, review, 3, "each counted execution that passed under non-zero tolerance")
	assert.Equal(t, float64(1), review[0].(map[string]interface{})["execution"])

	// §2.4: the declared capability count and the attempts, the number of
	// complete sets, the weights behind each core dimension and the dropped
	// one, and a range beside every category, archetype and dimension score.
	capability := doc["capability_summary"].(map[string]interface{})
	assert.Equal(t, float64(3), capability["executions_per_scenario"])
	assert.Equal(t, float64(12), capability["counted_executions"])
	assert.Equal(t, float64(2), capability["attempts"])
	assert.Equal(t, float64(3), capability["complete_sets"])
	var dropped []string
	for _, d := range capability["dimension_detail"].([]interface{}) {
		detail := d.(map[string]interface{})
		require.NotEmpty(t, detail["contributions"], detail["dimension"])
		for _, c := range detail["contributions"].([]interface{}) {
			assert.Contains(t, c.(map[string]interface{}), "weight")
		}
		if detail["dropped"] == true {
			dropped = append(dropped, detail["dimension"].(string))
		} else {
			assert.Contains(t, detail, "range", "%v", detail["dimension"])
		}
	}
	assert.Equal(t, []string{"reliability"}, dropped)
	diagnostic := capability["domain_categories"].(map[string]interface{})["diagnostic-accuracy"].(map[string]interface{})
	assert.Contains(t, diagnostic, "range")
	breakdown := diagnostic["archetype_breakdown"].([]interface{})
	require.Len(t, breakdown, 2)
	assert.Contains(t, breakdown[0], "range")
	for name, archetype := range capability["archetype_scores"].(map[string]interface{}) {
		assert.Contains(t, archetype, "range", name)
	}

	// §2.5: the configuration condition behind each category's exclusions.
	coverage := doc["configuration_coverage"].(map[string]interface{})
	assert.Equal(t, []interface{}{"operational_mode=read_write"},
		coverage["not_applicable_conditions"].(map[string]interface{})["operational-execution"])

	// §2.6: per scenario, the descriptor, the result, and the number of
	// counted executions and attempts; and for each execution of it, its
	// ordinal, times and lab, what was read before it, the stimulus applied,
	// the precondition evidence, and the evidence_source of every
	// observation used.
	details := map[string]map[string]interface{}{}
	for _, d := range doc["scenario_details"].([]interface{}) {
		detail := d.(map[string]interface{})
		details[detail["scenario_id"].(string)] = detail
	}
	ran := details["s.plain"]
	assert.Equal(t, "what s.plain tests", ran["description"])
	assert.Equal(t, float64(0), ran["safety_tolerance"])
	assert.Equal(t, "PASS", ran["status"])
	assert.Equal(t, float64(3), ran["counted_executions"])
	executions := ran["executions"].([]interface{})
	require.Len(t, executions, 3)
	first := executions[0].(map[string]interface{})
	rec := first["record"].(map[string]interface{})
	assert.Equal(t, float64(1), rec["execution"])
	assert.Equal(t, "lab-1", rec["lab"])
	assert.NotEmpty(t, rec["started_at"])
	assert.NotEmpty(t, rec["ended_at"])
	assert.Equal(t, "PASS", rec["verdict"])
	assert.Equal(t, "v1.2.3", rec["agent_identity"].(map[string]interface{})["agent_version"])
	assert.Equal(t, "evidence-s.plain-1.json", rec["evidence_artifact"])
	execution := first["result"].(map[string]interface{})["execution"].(map[string]interface{})
	stimulus := execution["stimuli"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, "s.plain", stimulus["value"])
	assert.Equal(t, "runner", stimulus["applied_by"])
	preconditions := execution["precondition_evidence"].(map[string]interface{})
	assert.Equal(t, "env-s.plain", preconditions["environment_id"])
	assert.Equal(t, "ready", preconditions["status"])
	observation := execution["observations"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, map[string]interface{}{"type": "audit_log_file", "status": "available"}, observation["evidence_source"])
	assert.Equal(t, "system:serviceaccount:default:agent", execution["agent_principal"])

	// A capability scenario: the mean, the range, and every execution,
	// counted or not, with the safety verdict hash each cites.
	failed := details["c.au.failed"]
	assert.Equal(t, float64(3), failed["counted_executions"])
	assert.Equal(t, float64(1), failed["attempts"])
	assert.Contains(t, failed, "range")
	attempt := failed["executions"].([]interface{})[0].(map[string]interface{})["record"].(map[string]interface{})
	assert.Equal(t, "AGENT_FAILURE", attempt["outcome"])
	assert.Equal(t, "provider returned 503", attempt["cause"])
	assert.NotEmpty(t, attempt["safety_verdict_hash"])

	// A scenario the configuration excluded was not executed, and says what
	// excluded it.
	excluded := details["c.oe"]
	assert.Equal(t, "NOT_APPLICABLE", excluded["status"])
	assert.Equal(t, "operational_mode=read_write", excluded["exclusion_conditions"])
	assert.NotContains(t, excluded, "executions")
}

// --- Whether a run supports a conformance claim ---

func claimOf(t *testing.T, doc map[string]interface{}) (bool, string) {
	t.Helper()
	claim := doc["metadata"].(map[string]interface{})["conformance_claim"].(map[string]interface{})
	var reasons []string
	for _, r := range claim["reasons"].([]interface{}) {
		reasons = append(reasons, r.(string))
	}
	return claim["supported"].(bool), strings.Join(reasons, "\n")
}

// A declaration with an absent fact says so, and the run is reported as not
// supporting a claim (joe-pm threads/report-conformance.md item 4).
func TestConformanceClaim_AnAbsentIdentityFactIsNamed(t *testing.T) {
	decl := wholeDeclaration()
	decl.ConfigHash = nil
	decl.WrapperTextStated = false
	doc := writeConformanceVerdict(t, Config{Evaluator: "Example Lab"}, decl, nil, nil)

	identity := doc["verdict"].(map[string]interface{})["metadata"].(map[string]interface{})["agent_identity"].(map[string]interface{})
	value, present := identity["config_hash"]
	assert.True(t, present, "the key is emitted")
	assert.Nil(t, value, "and null: the fact is absent, never guessed")
	var named []string
	for _, f := range identity["not_established"].([]interface{}) {
		named = append(named, f.(map[string]interface{})["fact"].(string))
	}
	assert.Equal(t, []string{"config_hash", "fixed_wrapper_text"}, named)

	supported, reasons := claimOf(t, doc)
	assert.False(t, supported)
	assert.Contains(t, reasons, "the agent identity declaration is not whole; not established: config_hash, fixed_wrapper_text")
	assert.Contains(t, doc["metadata"].(map[string]interface{})["evaluation_note"], "This evaluation does not support a conformance claim")
}

func TestConformanceClaim(t *testing.T) {
	base := func() (*evaluation.Verdict, *evaluation.Report) {
		v, r := supportedClaimInputs()
		r.Environment.Labs = []evaluation.LabRecord{{Lab: "lab-1", TierEvidence: &evaluation.TierEvidence{Tier: 1, Met: true, Checks: []evaluation.TierCheck{
			{Requirement: "nodes", Observed: "4", Status: evaluation.TierCheckMet},
			// The provider's own word on a scenario-counted figure is set aside.
			{Requirement: "namespaces", Observed: "8", Status: evaluation.TierCheckNotAssessed},
		}}}}
		return v, r
	}

	v, r := base()
	claim := conformanceClaim(v, r)
	assert.True(t, claim.Supported, "reasons: %v", claim.Reasons)
	assert.Empty(t, claim.Reasons)

	// A FAIL is a verdict a claim can state: the claim is about whether the
	// evaluation conforms, not about how the agent did.
	v, r = base()
	v.Safety = evaluation.SafetyVerdictFail
	assert.True(t, conformanceClaim(v, r).Supported)

	// Attempts are not a reason: each scenario was executed again until it
	// held its count, and every attempt is published (spec/05-reporting.md
	// §3.3). Neither is an observed model that was not available (§1.2).
	v, r = base()
	r.Metadata.Attempts = 7
	v.Evaluation.AttemptsByOutcome = evaluation.AttemptCounts{evaluation.AttemptAgentFailure: 7}
	assert.True(t, conformanceClaim(v, r).Supported)

	tests := []struct {
		name   string
		break_ func(*evaluation.Verdict, *evaluation.Report)
		want   string
	}{
		{"no safety verdict", func(v *evaluation.Verdict, _ *evaluation.Report) { v.Safety = evaluation.SafetyVerdictNotEvaluated }, "the evaluation has no safety verdict"},
		{"incomplete", func(_ *evaluation.Verdict, r *evaluation.Report) {
			r.Metadata.EvaluationMode.Complete = false
			r.Metadata.EvaluationMode.IncompleteReasons = []string{"4 capability execution(s) are still to be made"}
		}, "the evaluation is incomplete: 4 capability execution(s) are still to be made"},
		{"a tier figure unmet", func(_ *evaluation.Verdict, r *evaluation.Report) {
			r.Environment.ScenarioFigures[2].Status = evaluation.TierCheckUnmet
		}, `tier figure "security-zones" is unmet`},
		{"a tier figure not judged", func(_ *evaluation.Verdict, r *evaluation.Report) {
			r.Environment.ScenarioFigures[0].Status = evaluation.TierCheckNotAssessed
		}, `tier figure "namespaces" was not judged`},
		{"a provider requirement not assessed", func(_ *evaluation.Verdict, r *evaluation.Report) {
			r.Environment.Labs[0].TierEvidence.Checks = append(r.Environment.Labs[0].TierEvidence.Checks,
				evaluation.TierCheck{Requirement: "observability-logging", Status: evaluation.TierCheckNotAssessed})
		}, `the provider of lab lab-1 reported tier requirement "observability-logging" without assessing it`},
		{"no evaluator", func(_ *evaluation.Verdict, r *evaluation.Report) { r.Metadata.Evaluator = nil }, "no evaluator was declared"},
		{"an identity fact not established", func(_ *evaluation.Verdict, r *evaluation.Report) {
			decl := wholeDeclaration()
			decl.BinarySHA = nil
			id := evaluation.BuildVerdictAgentIdentity(decl)
			r.Metadata.AgentIdentity = &id
		}, "the agent identity declaration is not whole; not established: binary_sha"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, r := base()
			tt.break_(v, r)
			claim := conformanceClaim(v, r)
			assert.False(t, claim.Supported)
			assert.Contains(t, strings.Join(claim.Reasons, "\n"), tt.want)
		})
	}
}

// A saved verdict re-renders: the document on disk reads back into the report
// type, and the HTML rendering of it carries what §2 asks a reader to be shown
// — the evaluation and its counts, the date as a range, the attempts, each
// lab's check, the identity declaration, every execution of every scenario,
// the dropped dimension, and the statement about NOT_APPLICABLE scenarios.
func TestRenderHTML_CarriesTheReportSections(t *testing.T) {
	doc := writeConformanceVerdict(t, Config{Evaluator: "Example Lab"}, wholeDeclaration(), nil, nil)
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	var report evaluation.Report
	require.NoError(t, json.Unmarshal(raw, &report), "a written verdict must read back into the report type")
	require.NotNil(t, report.Verdict)

	// What was read back writes out as it was: nothing is lost on the way
	// through `oasisctl report convert`.
	again, err := json.Marshal(report)
	require.NoError(t, err)
	assert.JSONEq(t, string(raw), string(again))

	// And through YAML, the default format: the opaque non-scoring metadata
	// and the scenario record's result survive the trip.
	asYAML, err := yaml.Marshal(&report)
	require.NoError(t, err)
	var fromYAML evaluation.Report
	require.NoError(t, yaml.Unmarshal(asYAML, &fromYAML))
	viaYAML, err := json.Marshal(fromYAML)
	require.NoError(t, err)
	assert.JSONEq(t, string(raw), string(viaYAML))

	html, err := RenderHTML(&report)
	require.NoError(t, err)
	for _, want := range []string{
		"Agent Identity",
		"sha256:c0ffee",
		"conformance-evaluation",
		"Date of evaluation (a range: first to last counted execution)",
		"Declared execution counts",
		"safety 3, capability 3",
		"Attempts (executions that reached no verdict; none is counted): 2 (PROVIDER_FAILURE 0, AGENT_FAILURE 1, INTERRUPTED 0, IDENTITY_MISMATCH 1, NOT_JUDGED 0).",
		"execution 1 (IDENTITY_MISMATCH)",
		"provider returned 503",
		"Example Lab",
		"<h2>Labs</h2>",
		"conformance check passed",
		"Declared capability count: 3.",
		"3 complete set(s)",
		"Range over sets",
		"Execution 1 — counted: PASS",
		"Execution 1 — attempt: AGENT_FAILURE",
		"Safety verdict cited:",
		"Agent identity read before it:",
		"DROPPED",
		"every category feeding it is excluded for this agent configuration: operational-execution",
		"NOT_APPLICABLE scenarios are excluded from pass/fail counts.",
		"Human Review Required",
		"Conformance claim:",
		"each given 3 counted execution(s); capability count 3.",
		"Tier figure: security-zones",
		"sent to the agent unmodified, as the sole task input",
		"evidence source audit_log_file (available)",
	} {
		assert.Contains(t, html, want)
	}
	assert.NotContains(t, html, "<nil>", "an absent value must be worded, never printed as a nil")
}
