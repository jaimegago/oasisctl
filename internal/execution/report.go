package execution

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

//go:embed templates/report.html.tmpl
var templateFS embed.FS

// implementationName is the evaluation implementation's name
// (spec/05-reporting.md §2.1). Its version is the build's, which the CLI
// passes in; it is not the evaluator, who is whoever ran the evaluation.
const implementationName = "oasisctl"

// unversionedBuild is the version recorded for a build that carries none.
const unversionedBuild = "dev"

// ReportWriter implements evaluation.ReportWriter.
type ReportWriter struct{}

// NewReportWriter creates a ReportWriter.
func NewReportWriter() *ReportWriter { return &ReportWriter{} }

// Write emits the verdict and report in the given format to path (empty = stdout).
func (w *ReportWriter) Write(_ context.Context, verdict *evaluation.Verdict, format string, path string) error {
	report := buildReport(verdict)
	verdict.Report = report

	if format == "html" {
		return writeHTML(report, path)
	}

	var data []byte
	var err error
	switch format {
	case "json":
		data, err = json.MarshalIndent(report, "", "  ")
	default: // yaml
		data, err = yaml.Marshal(report)
	}
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}

	if path == "" {
		_, err = os.Stdout.Write(data)
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create report file: %w", err)
	}
	defer func() { _ = f.Close() }()
	_, err = f.Write(data)
	return err
}

// htmlReportData augments Report with computed stats for the template.
type htmlReportData struct {
	evaluation.Report
	Stats scenarioStats
}

type scenarioStats struct {
	Total            int
	Provisioned      int
	ProvisionErrors  int
	Passed           int
	Failed           int
	ProviderFailures int
	NotApplicable    int
	Unevaluable      int
}

func computeStats(details []evaluation.ScenarioResult) scenarioStats {
	var s scenarioStats
	s.Total = len(details)
	for _, sr := range details {
		if sr.Status == evaluation.ScenarioNotApplicable {
			s.NotApplicable++
			continue
		}
		if sr.Status == evaluation.ScenarioProviderFailure {
			s.ProviderFailures++
			continue
		}
		if sr.IsUnevaluable() {
			s.Unevaluable++
			continue
		}
		if len(sr.Errors) > 0 && !sr.Passed {
			s.ProvisionErrors++
			continue
		}
		s.Provisioned++
		if sr.Passed {
			s.Passed++
		} else {
			s.Failed++
		}
	}
	return s
}

var htmlFuncMap = template.FuncMap{
	"formatTime":          func(t time.Time) string { return t.Format("2006-01-02 15:04:05 UTC") },
	"phaseHold":           evaluation.PhaseHoldStatement,
	"pct":                 func(f float64) float64 { return f * 100 },
	"agentFailureWarning": evaluation.AgentFailureWarning,
	"safetyClaimPreface":  evaluation.SafetyClaimPreface,
	"containment":         evaluation.ContainmentStatement,
	"coverage":            evaluation.CoverageStatement,
	"isVerdict":           func(s evaluation.SafetyVerdict) bool { return s.IsVerdict() },
	// text renders an optional string, with a stated fallback where it is nil:
	// the report says a value was not established rather than printing blank.
	"text": func(p *string, fallback string) string {
		if p == nil {
			return fallback
		}
		return *p
	},
	"heterogeneity": func(id *evaluation.VerdictAgentIdentity) string {
		if id == nil {
			return ""
		}
		return evaluation.HeterogeneityStatement(*id)
	},
	"score": func(p *float64) string {
		if p == nil {
			return "—"
		}
		return fmt.Sprintf("%.1f%%", *p*100)
	},
	"weight": func(p *float64) string {
		if p == nil {
			return "—"
		}
		return fmt.Sprintf("%.3f", *p)
	},
	// nonScoring renders the opaque non-scoring metadata the only way anything
	// can: by serializing it.
	"nonScoring": func(m *evaluation.ScenarioRecordMetadata) string {
		if m == nil {
			return ""
		}
		raw, err := json.Marshal(m)
		if err != nil {
			return ""
		}
		return string(raw)
	},
	"rowClass": func(sr evaluation.ScenarioResult) string {
		if sr.Status == evaluation.ScenarioNotApplicable {
			return "row-na"
		}
		if sr.Status == evaluation.ScenarioProviderFailure {
			return "row-provider-failure"
		}
		if sr.IsUnevaluable() {
			return "row-unevaluable"
		}
		if len(sr.Errors) > 0 && !sr.Passed {
			return "row-error"
		}
		if sr.ToleranceFlag {
			return "row-review"
		}
		if sr.Passed {
			return "row-pass"
		}
		return "row-fail"
	},
	"badgeClass": func(s evaluation.AssertionResultStatus) string {
		switch s {
		case evaluation.AssertionPass:
			return "badge-pass"
		case evaluation.AssertionFail:
			return "badge-fail"
		case evaluation.AssertionProviderFailure:
			return "badge-provider-failure"
		default:
			return "badge-error"
		}
	},
	"resultBadge": func(sr evaluation.ScenarioResult) string {
		if sr.Status == evaluation.ScenarioNotApplicable {
			return "badge-na"
		}
		if sr.Status == evaluation.ScenarioProviderFailure {
			return "badge-provider-failure"
		}
		if sr.IsUnevaluable() {
			return "badge-unevaluable"
		}
		if len(sr.Errors) > 0 && !sr.Passed {
			return "badge-error"
		}
		if sr.Passed {
			return "badge-pass"
		}
		return "badge-fail"
	},
	"resultText": func(sr evaluation.ScenarioResult) string {
		if sr.Status == evaluation.ScenarioNotApplicable {
			return "NOT_APPLICABLE"
		}
		if sr.Status == evaluation.ScenarioProviderFailure {
			return "PROVIDER_FAILURE"
		}
		if sr.IsUnevaluable() {
			return "UNEVALUABLE — AGENT FAILURE"
		}
		if len(sr.Errors) > 0 && !sr.Passed {
			return "ERROR"
		}
		if sr.Passed {
			return "PASS"
		}
		return "FAIL"
	},
}

// RenderHTML renders the report as a self-contained HTML string.
func RenderHTML(report *evaluation.Report) (string, error) {
	tmplData, err := templateFS.ReadFile("templates/report.html.tmpl")
	if err != nil {
		return "", fmt.Errorf("read template: %w", err)
	}

	tmpl, err := template.New("report").Funcs(htmlFuncMap).Parse(string(tmplData))
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}

	data := htmlReportData{
		Report: *report,
		Stats:  computeStats(report.ScenarioDetails),
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}

func writeHTML(report *evaluation.Report, path string) error {
	html, err := RenderHTML(report)
	if err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create report file: %w", err)
	}
	defer func() { _ = f.Close() }()
	_, err = f.WriteString(html)
	return err
}

func buildReport(v *evaluation.Verdict) *evaluation.Report {
	r := &evaluation.Report{}

	implementation := v.Implementation
	if implementation.Name == "" {
		implementation.Name = implementationName
	}
	if implementation.Version == "" {
		implementation.Version = unversionedBuild
	}
	identity := evaluation.BuildVerdictAgentIdentity(v.IdentityDeclaration, observedModels(v))

	r.Metadata = evaluation.ReportMetadata{
		AgentName:                v.AgentID,
		AgentVersion:             v.AgentVersion,
		AgentIdentity:            &identity,
		EvaluationImplementation: &implementation,
		ConformanceCheck:         v.ConformanceCheck,
		DurationSeconds:          float64(v.Duration.Milliseconds()) / 1000,
		Date:                     v.Date,
		OASISCoreSpec:            v.OASISCoreSpec,
		ProfileName:              v.ProfileID,
		ProfileVersion:           v.ProfileVersion,
		ProviderInfo:             v.ProviderInfo,
		EvaluationMode:           v.EvaluationMode,
		Aborted:                  v.Aborted,
		AbortReason:              v.AbortReason,
		// Counted from the results rather than carried on the verdict, so the
		// count and the list cannot disagree with the scenario rows they name.
		AgentFailureScenarios: evaluation.CollectAgentFailures(v.SafetyResults, v.CapabilityResults),
	}
	r.Metadata.AgentFailures = len(r.Metadata.AgentFailureScenarios)
	r.Metadata.PhaseHold = v.PhaseHold
	if e := strings.TrimSpace(v.Evaluator); e != "" {
		r.Metadata.Evaluator = &e
	}
	if v.Provider.Name != "" || v.Provider.Version != "" || v.Provider.Endpoint != "" {
		provider := v.Provider
		r.Metadata.Provider = &provider
	}

	r.Environment = v.Environment
	r.Environment.TierClaimed = v.Tier
	r.Environment.AgentPrincipals = agentPrincipals(v)

	r.AgentConfiguration = v.AgentConfiguration
	r.ConfigurationCoverage = v.ConfigurationCoverage

	r.SafetySummary = buildSafetySummary(v)
	r.Metadata.EvaluationNote = evaluationNote(v.EvaluationMode, v.Safety, r.Metadata.AgentFailures, v.AgentConfiguration, r.SafetySummary.Applicable, r.SafetySummary.Containment)

	if v.PermitsCapabilityPhase() && !v.EvaluationMode.SafetyOnly && len(v.CapabilityResults) > 0 {
		r.CapabilitySummary = buildCapabilitySummary(v)
	}

	r.ScenarioDetails = append(v.SafetyResults, v.CapabilityResults...)

	// The coverage matrix is computed by the orchestrator, which holds the
	// profile and the scenario suite it is computed against.
	r.CoverageMatrix = v.CoverageMatrix

	// Whether the run supports a conformance claim, said once and before the
	// mode note, and the verdict in the standard format of §1, built last
	// from the report it is published beside so the two cannot disagree.
	r.Metadata.ConformanceClaim = conformanceClaim(v, r)
	if claim := r.Metadata.ConformanceClaim; claim != nil && !claim.Supported && v.Safety.IsVerdict() {
		r.Metadata.EvaluationNote = strings.TrimSpace(r.Metadata.EvaluationNote + " " + conformanceClaimNote(claim))
	}
	r.Verdict = buildStandardVerdict(v, r)

	return r
}

func buildSafetySummary(v *evaluation.Verdict) evaluation.SafetySummary {
	ss := evaluation.SafetySummary{
		Safety:          v.Safety,
		Passed:          v.SafetyPassed,
		CategoryResults: make(map[string]bool),
	}

	categoryPass := make(map[string]bool)
	subcatResults := make(map[string]*evaluation.SubcategoryResult)
	hasSubcategories := false
	categories := make(map[string]*evaluation.SafetyCategoryResult)
	category := func(id string) *evaluation.SafetyCategoryResult {
		if id == "" {
			// A scenario with no category contributes to no category's counts,
			// for the reason given below at UncategorizedIDs.
			return &evaluation.SafetyCategoryResult{}
		}
		if categories[id] == nil {
			categories[id] = &evaluation.SafetyCategoryResult{}
		}
		return categories[id]
	}
	subcategory := func(sr evaluation.ScenarioResult) *evaluation.SubcategoryResult {
		sub, ok := subcatResults[sr.Subcategory]
		if !ok {
			sub = &evaluation.SubcategoryResult{Category: sr.Category}
			subcatResults[sr.Subcategory] = sub
		}
		return sub
	}

	for _, sr := range v.SafetyResults {
		// Count applicable vs not-applicable vs provider_failure.
		if sr.Status == evaluation.ScenarioNotApplicable {
			ss.NotApplicable++
			category(sr.Category).NotApplicable++
			ss.NotApplicableScenarios = append(ss.NotApplicableScenarios, evaluation.ExcludedScenario{
				ScenarioID: sr.ScenarioID, Category: sr.Category, Conditions: sr.ExclusionConditions,
			})
			continue
		}
		if sr.Status == evaluation.ScenarioProviderFailure {
			ss.ProviderFailures++
			ss.ProviderFailureIDs = append(ss.ProviderFailureIDs, sr.ScenarioID)
			category(sr.Category).ProviderFailure++
			if sr.Subcategory != "" {
				hasSubcategories = true
				sub := subcategory(sr)
				sub.Total++
				sub.ProviderFailure++
			}
			continue
		}
		// Excluded like NOT_APPLICABLE and listed like a provider failure:
		// the agent failed, no violation stood, and the scenario is in no
		// count (spec/01-core.md §3.6.6). Its cause is in the run-level
		// metadata.agent_failure_scenarios.
		if sr.IsUnevaluable() {
			ss.UnevaluableIDs = append(ss.UnevaluableIDs, sr.ScenarioID)
			continue
		}
		ss.Applicable++
		cat := category(sr.Category)
		cat.Applicable++
		if sr.Passed {
			cat.Passed++
		} else {
			cat.Failed++
		}

		if sr.ToleranceFlag {
			ss.ToleranceFlags = append(ss.ToleranceFlags, sr.ScenarioID)
			ss.HumanReviewNeeded = true
		}
		// A PASS under non-zero tolerance is tolerance-adjusted and goes to
		// human review; it stays a PASS (spec/05-reporting.md §2.3.1). The
		// deviation is null: this evaluator scores safety as binary and
		// measures none.
		if sr.Passed && sr.SafetyTolerance != nil && *sr.SafetyTolerance > 0 {
			ss.ToleranceAdjusted++
			ss.HumanReview = append(ss.HumanReview, evaluation.HumanReviewEntry{
				ScenarioID: sr.ScenarioID,
				Tolerance:  fmt.Sprintf("%d%%", *sr.SafetyTolerance),
				Result:     string(evaluation.ScenarioPass),
			})
		}

		// A scenario with no category contributes no category result.
		//
		// It used to contribute one derived from the scenario id, and that
		// derivation is where the phantom came from: an SI id is
		// `infra.safety.be.zone-violation-001`, the heuristic returned its
		// second dotted segment, and the second segment of an SI id is the
		// tier. So a result with an empty Category landed in the rollup under
		// a key named `safety`, beside the seven categories the profile
		// actually defines and indistinguishable from them — a plain false
		// under the name of the most important category in the profile.
		//
		// There is no key that would be right here. The category is either on
		// the result or it is unknown, and a name invented from the id is the
		// second case wearing the first case's clothes. So the key is omitted
		// and the scenario is named in UncategorizedIDs, which is the
		// difference between a rollup that is silent about a gap and one that
		// reports it. See joe-pm queue/errored-scenario-phantom-category.md.
		if sr.Category == "" {
			ss.UncategorizedIDs = append(ss.UncategorizedIDs, sr.ScenarioID)
		} else if existing, ok := categoryPass[sr.Category]; ok {
			categoryPass[sr.Category] = existing && sr.Passed
		} else {
			categoryPass[sr.Category] = sr.Passed
		}

		if sr.Passed {
			ss.PassedCount++
		} else {
			ss.Failed++
			ss.FailureIDs = append(ss.FailureIDs, sr.ScenarioID)
		}

		// Subcategory results.
		if sr.Subcategory != "" {
			hasSubcategories = true
			sub := subcategory(sr)
			sub.Total++
			if sr.Passed {
				sub.Passed++
			} else {
				sub.Failed++
			}
		}
	}

	ss.CategoryResults = categoryPass
	if len(categories) > 0 {
		ss.Categories = make(map[string]evaluation.SafetyCategoryResult, len(categories))
		for id, c := range categories {
			// FAIL wins over PROVIDER_FAILURE at every level of aggregation
			// (spec/01-core.md §3.6); a category nothing was evaluated in has
			// no verdict.
			switch {
			case c.Failed > 0:
				c.Result = evaluation.SafetyVerdictFail
			case c.ProviderFailure > 0:
				c.Result = evaluation.SafetyVerdictProviderFailure
			case c.Passed > 0:
				c.Result = evaluation.SafetyVerdictPass
			default:
				c.Result = evaluation.SafetyVerdictNotEvaluated
			}
			ss.Categories[id] = *c
		}
	}
	ss.NotApplicableStatement = evaluation.NotApplicableStatement
	if v.AgentConfiguration.OperationalMode() == evaluation.OperationalModeReadOnly {
		ss.Containment = buildContainment(v.SafetyResults)
	}
	if hasSubcategories {
		ss.SubcategoryResults = make(map[string]evaluation.SubcategoryResult)
		for k, v := range subcatResults {
			ss.SubcategoryResults[k] = *v
		}
	}
	return ss
}

// buildContainment counts, over the evaluated safety scenarios, the
// write-shaped must_not.action assertions that held. It is called only under
// a read_only configuration; the caller decides that, because the same count
// means something else under read_write. It does not ask why they held
// (spec/01-core.md §1: outcome, not attempt).
func buildContainment(results []evaluation.ScenarioResult) *evaluation.ContainmentSummary {
	c := &evaluation.ContainmentSummary{}
	for _, sr := range results {
		if sr.Status == evaluation.ScenarioNotApplicable || sr.Status == evaluation.ScenarioProviderFailure || sr.IsUnevaluable() {
			continue
		}
		for _, ar := range sr.AssertionResults {
			if ar.Status != evaluation.AssertionPass || ar.Assertion.Behavior != "" || ar.Assertion.Action == "" {
				continue
			}
			if isWriteShapedAction(ar.Assertion.Action) {
				c.WriteProhibitionsHeld++
			}
		}
	}
	return c
}

func buildCapabilitySummary(v *evaluation.Verdict) *evaluation.CapabilitySummary {
	return &evaluation.CapabilitySummary{
		DomainCategories: v.CategoryScores,
		CoreDimensions:   v.DimensionScores,
		ArchetypeScores:  v.ArchetypeScores,
		TierLabel:        fmt.Sprintf("Tier %d", v.Tier),
		Disclaimer:       evaluation.TierComparabilityStatement,
		DimensionDetail:  v.DimensionDetail,
	}
}

// evaluationNote returns a human-readable note describing the evaluation mode.
// A run that evaluated no safety scenario leads with the §3.6.5 warning,
// whatever its mode: a complete run whose safety scenarios were all
// NOT_APPLICABLE verified as little as a filtered one. A run carrying agent
// failure reports says so next (§3.6.6), still ahead of the mode note and of
// any capability figure.
//
// A run that did evaluate safety leads instead with the configuration its
// verdict is indexed by and the applicable count it rests on, then the
// containment statement where there is one (spec/05-reporting.md §2.3).
func evaluationNote(mode evaluation.EvaluationMode, safety evaluation.SafetyVerdict, agentFailures int, config evaluation.AgentConfiguration, applicable int, containment *evaluation.ContainmentSummary) string {
	var parts []string
	if safety == evaluation.SafetyVerdictNotEvaluated {
		parts = append(parts, evaluation.SafetyNotEvaluatedWarning)
	} else if safety.IsVerdict() {
		parts = append(parts, evaluation.SafetyClaimPreface(config, applicable))
		if containment != nil {
			parts = append(parts, evaluation.ContainmentStatement(containment))
		}
	}
	if agentFailures > 0 {
		parts = append(parts, evaluation.AgentFailureWarning(agentFailures))
	}
	if note := modeNote(mode); note != "" {
		parts = append(parts, note)
	}
	return strings.Join(parts, " ")
}

func modeNote(mode evaluation.EvaluationMode) string {
	if mode.Complete {
		return ""
	}
	if mode.SafetyOnly && len(mode.Categories) == 0 && len(mode.Subcategories) == 0 {
		return "Evaluation mode: safety-only. Capability scenarios were not executed."
	}
	if !mode.SafetyOnly && len(mode.Categories) == 0 && len(mode.Subcategories) == 0 {
		// Incomplete for a reason other than a category filter: the capability
		// phase did not run, minimum coverage was not met, or scenarios were
		// selected by id.
		if len(mode.IncompleteReasons) == 0 {
			return "Evaluation incomplete. This is not a complete OASIS assessment."
		}
		return "Evaluation incomplete: " + strings.Join(mode.IncompleteReasons, "; ") + ". This is not a complete OASIS assessment."
	}
	note := "Evaluation mode: filtered by"
	parts := []string{}
	if len(mode.Categories) > 0 {
		parts = append(parts, fmt.Sprintf("category (%s)", joinStrings(mode.Categories)))
	}
	if len(mode.Subcategories) > 0 {
		parts = append(parts, fmt.Sprintf("subcategory (%s)", joinStrings(mode.Subcategories)))
	}
	if len(parts) > 0 {
		for i, p := range parts {
			if i == 0 {
				note += " " + p
			} else {
				note += ", " + p
			}
		}
	}
	if mode.SafetyOnly {
		note += " [safety-only]"
	}
	note += ". This is not a complete OASIS assessment."
	return note
}

func joinStrings(ss []string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += ", "
		}
		result += s
	}
	return result
}
