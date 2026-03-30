package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

const evaluatorName = "oasisctl"
const evaluatorVersion = "0.1.0"

// ReportWriter implements evaluation.ReportWriter.
type ReportWriter struct{}

// NewReportWriter creates a ReportWriter.
func NewReportWriter() *ReportWriter { return &ReportWriter{} }

// Write emits the verdict and report in the given format to path (empty = stdout).
func (w *ReportWriter) Write(_ context.Context, verdict *evaluation.Verdict, format string, path string) error {
	report := buildReport(verdict)
	verdict.Report = report

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

func buildReport(v *evaluation.Verdict) *evaluation.Report {
	r := &evaluation.Report{}

	r.Metadata = evaluation.ReportMetadata{
		AgentName:      v.AgentID,
		AgentVersion:   v.AgentVersion,
		Evaluator:      evaluatorName + " " + evaluatorVersion,
		Date:           v.Date,
		ProfileName:    v.ProfileID,
		ProfileVersion: v.ProfileVersion,
		ProviderInfo:   v.ProviderInfo,
		EvaluationMode: v.EvaluationMode,
		EvaluationNote: evaluationNote(v.EvaluationMode),
	}

	r.Environment = evaluation.ReportEnvironment{
		TierClaimed: v.Tier,
	}

	r.SafetySummary = buildSafetySummary(v)

	if v.SafetyPassed && !v.EvaluationMode.SafetyOnly && len(v.CapabilityResults) > 0 {
		r.CapabilitySummary = buildCapabilitySummary(v)
	}

	r.ScenarioDetails = append(v.SafetyResults, v.CapabilityResults...)

	// Build coverage matrix: category -> list of archetypes evaluated.
	r.CoverageMatrix = make(map[string][]string)
	for _, sr := range r.ScenarioDetails {
		_ = sr // archetypes are on the Scenario, not the result; leave for future enrichment
	}

	return r
}

func buildSafetySummary(v *evaluation.Verdict) evaluation.SafetySummary {
	ss := evaluation.SafetySummary{
		Passed:          v.SafetyPassed,
		CategoryResults: make(map[string]bool),
	}

	categoryPass := make(map[string]bool)
	subcatResults := make(map[string]*evaluation.SubcategoryResult)
	hasSubcategories := false

	for _, sr := range v.SafetyResults {
		if sr.ToleranceFlag {
			ss.ToleranceFlags = append(ss.ToleranceFlags, sr.ScenarioID)
			ss.HumanReviewNeeded = true
		}

		// Use ScenarioResult.Category if populated, fall back to ID heuristic.
		cat := sr.Category
		if cat == "" {
			cat = categoryFromID(sr.ScenarioID)
		}
		if existing, ok := categoryPass[cat]; ok {
			categoryPass[cat] = existing && sr.Passed
		} else {
			categoryPass[cat] = sr.Passed
		}

		// Subcategory results.
		if sr.Subcategory != "" {
			hasSubcategories = true
			sub, ok := subcatResults[sr.Subcategory]
			if !ok {
				sub = &evaluation.SubcategoryResult{}
				subcatResults[sr.Subcategory] = sub
			}
			sub.Total++
			if sr.Passed {
				sub.Passed++
			} else {
				sub.Failed++
			}
		}
	}

	ss.CategoryResults = categoryPass
	if hasSubcategories {
		ss.SubcategoryResults = make(map[string]evaluation.SubcategoryResult)
		for k, v := range subcatResults {
			ss.SubcategoryResults[k] = *v
		}
	}
	return ss
}

func buildCapabilitySummary(v *evaluation.Verdict) *evaluation.CapabilitySummary {
	return &evaluation.CapabilitySummary{
		CategoryScores:  v.CategoryScores,
		ArchetypeScores: v.ArchetypeScores,
		DimensionScores: v.DimensionScores,
		TierLabel:       fmt.Sprintf("Tier %d", v.Tier),
		Disclaimer:      "Scores are only comparable between evaluations at the same tier",
	}
}

// evaluationNote returns a human-readable note describing the evaluation mode.
func evaluationNote(mode evaluation.EvaluationMode) string {
	if mode.Complete {
		return ""
	}
	if mode.SafetyOnly && len(mode.Categories) == 0 && len(mode.Subcategories) == 0 {
		return "Evaluation mode: safety-only. Capability scenarios were not executed."
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

// categoryFromID derives a rough category key from a scenario ID (e.g. "safety.sec.001" -> "sec").
func categoryFromID(id string) string {
	parts := splitDots(id)
	if len(parts) >= 2 {
		return parts[1]
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return id
}

func splitDots(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}
