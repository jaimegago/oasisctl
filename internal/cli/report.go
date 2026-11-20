package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/execution"
)

func newReportCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Re-render a saved evaluation verdict",
	}

	cmd.AddCommand(newReportHTMLCommand())
	cmd.AddCommand(newReportSummaryCommand())
	cmd.AddCommand(newReportConvertCommand())

	return cmd
}

func newReportHTMLCommand() *cobra.Command {
	var (
		inputPath  string
		outputPath string
		open       bool
	)

	cmd := &cobra.Command{
		Use:   "html",
		Short: "Render a verdict file as an HTML report",
		RunE: func(cmd *cobra.Command, args []string) error {
			if inputPath == "" {
				return fmt.Errorf("--input is required")
			}
			if outputPath == "" {
				return fmt.Errorf("--output is required")
			}

			report, err := loadReport(inputPath)
			if err != nil {
				return err
			}

			html, err := execution.RenderHTML(report)
			if err != nil {
				return fmt.Errorf("render html: %w", err)
			}

			f, err := os.Create(outputPath)
			if err != nil {
				return fmt.Errorf("create output file: %w", err)
			}
			defer func() { _ = f.Close() }()
			if _, err := f.WriteString(html); err != nil {
				return err
			}

			absPath, _ := filepath.Abs(outputPath)
			fmt.Fprintf(os.Stderr, "report written to %s\n", absPath)

			if open {
				openInBrowser(absPath)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&inputPath, "input", "", "Path to verdict YAML or JSON file")
	cmd.Flags().StringVar(&outputPath, "output", "", "Path to write HTML report")
	cmd.Flags().BoolVar(&open, "open", false, "Open HTML report in default browser")

	return cmd
}

func newReportSummaryCommand() *cobra.Command {
	var inputPath string

	cmd := &cobra.Command{
		Use:   "summary",
		Short: "Print a concise text summary of a verdict file",
		RunE: func(cmd *cobra.Command, args []string) error {
			if inputPath == "" {
				return fmt.Errorf("--input is required")
			}

			report, err := loadReport(inputPath)
			if err != nil {
				return err
			}

			safetyVerdict := string(report.SafetySummary.Safety)
			if safetyVerdict == "" {
				// Backwards compatibility with older verdict files.
				if report.SafetySummary.Passed {
					safetyVerdict = "PASS"
				} else {
					safetyVerdict = "FAIL"
				}
			}

			var failCount int
			var passCount int
			var unevaluableCount int
			var notApplicableCount int
			for _, sr := range report.ScenarioDetails {
				// A NOT_APPLICABLE scenario was not run. Its record carries
				// passed=true, and it is neither a pass nor a failure: it
				// "does not contribute to PASS counts, FAIL counts, or
				// PROVIDER_FAILURE counts" (spec/01-core.md §3.6.1). Counted
				// on its own, as an unevaluable one is.
				if sr.Status == evaluation.ScenarioNotApplicable {
					notApplicableCount++
					continue
				}
				// An unevaluable scenario carries passed=false so it can never
				// read as a pass; it is not a failure either, and is counted
				// on its own (spec/01-core.md §3.6.6).
				if sr.IsUnevaluable() {
					unevaluableCount++
					continue
				}
				if sr.Passed {
					passCount++
				} else {
					failCount++
				}
			}

			// Category results.
			var catParts []string
			for cat, passed := range report.SafetySummary.CategoryResults {
				status := "PASS"
				if !passed {
					status = "FAIL"
				}
				catParts = append(catParts, fmt.Sprintf("%s:%s", cat, status))
			}

			// The category list no longer accounts for every scenario, so say
			// so when it does not. An uncategorized scenario used to appear in
			// this line under a category name invented from its id; dropping
			// it silently would trade a wrong entry for a missing one.
			uncategorized := ""
			if n := len(report.SafetySummary.UncategorizedIDs); n > 0 {
				uncategorized = fmt.Sprintf(" | Uncategorized: %d", n)
			}

			if safetyVerdict == string(evaluation.SafetyVerdictNotEvaluated) {
				fmt.Println(evaluation.SafetyNotEvaluatedWarning)
			} else if evaluation.SafetyVerdict(safetyVerdict).IsVerdict() {
				// Before the verdict word: what the verdict is a claim about
				// (spec/05-reporting.md §2.3).
				fmt.Println(evaluation.SafetyClaimPreface(report.AgentConfiguration, report.SafetySummary.Applicable))
				if report.SafetySummary.Containment != nil {
					fmt.Println(evaluation.ContainmentStatement(report.SafetySummary.Containment))
				}
			}
			printAgentFailures(report.Metadata)
			unevaluable := ""
			if unevaluableCount > 0 {
				unevaluable = fmt.Sprintf(", %d unevaluable", unevaluableCount)
			}
			notApplicable := ""
			if notApplicableCount > 0 {
				notApplicable = fmt.Sprintf(", %d not applicable", notApplicableCount)
			}
			fmt.Printf("Safety: %s | Scenarios: %d passed, %d failed%s%s | Agent failures: %d | Categories: %s%s\n",
				safetyVerdict, passCount, failCount, unevaluable, notApplicable, report.Metadata.AgentFailures, strings.Join(catParts, ", "), uncategorized)

			// The tier the verdict claims, the evidence it rests on, and
			// whether minimum coverage for it was met
			// (spec/05-reporting.md §2.2, §2.5). A verdict file written before
			// these were recorded carries neither, and prints neither.
			if report.Environment.Evidence != "" {
				fmt.Printf("Tier %d: %s\n", report.Environment.TierClaimed, report.Environment.Evidence)
			}
			if report.CoverageMatrix != nil {
				fmt.Println(evaluation.CoverageStatement(report.CoverageMatrix))
			}

			// The heterogeneity flag, and whether the run supports a
			// conformance claim (spec/05-reporting.md §2.1, §3.2). A verdict
			// file written before these were recorded carries neither, and
			// prints neither.
			if id := report.Metadata.AgentIdentity; id != nil {
				fmt.Println(evaluation.HeterogeneityStatement(*id))
			}
			if claim := report.Metadata.ConformanceClaim; claim != nil {
				if claim.Supported {
					fmt.Println("Conformance claim: supported by this run.")
				} else {
					fmt.Printf("Conformance claim: NOT supported by this run — %s.\n", strings.Join(claim.Reasons, "; "))
				}
			}
			// A wait between the phases, where the run made one. A run that
			// made none prints nothing.
			if hold := report.Metadata.PhaseHold; hold != nil {
				fmt.Println(evaluation.PhaseHoldStatement(hold))
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&inputPath, "input", "", "Path to verdict YAML or JSON file")

	return cmd
}

// printAgentFailures states a run's agent failures — the warning, then each
// scenario and its cause — ahead of every other line about the run's results
// (spec/01-core.md §3.6.6). It prints nothing for a run that has none.
func printAgentFailures(md evaluation.ReportMetadata) {
	if md.AgentFailures == 0 {
		return
	}
	fmt.Println(evaluation.AgentFailureWarning(md.AgentFailures))
	for _, f := range md.AgentFailureScenarios {
		fmt.Printf("  - %s (%s): %s\n", f.ScenarioID, f.Status, f.Cause)
	}
}

func newReportConvertCommand() *cobra.Command {
	var (
		inputPath  string
		outputPath string
		format     string
	)

	cmd := &cobra.Command{
		Use:   "convert",
		Short: "Convert a verdict file between YAML and JSON formats",
		RunE: func(cmd *cobra.Command, args []string) error {
			if inputPath == "" {
				return fmt.Errorf("--input is required")
			}
			if format == "" {
				return fmt.Errorf("--format is required")
			}
			if format != "yaml" && format != "json" {
				return fmt.Errorf("--format must be yaml or json")
			}

			report, err := loadReport(inputPath)
			if err != nil {
				return err
			}

			var data []byte
			switch format {
			case "json":
				data, err = json.MarshalIndent(report, "", "  ")
			case "yaml":
				data, err = yaml.Marshal(report)
			}
			if err != nil {
				return fmt.Errorf("marshal report: %w", err)
			}

			if outputPath == "" {
				_, err = os.Stdout.Write(data)
				return err
			}

			f, err := os.Create(outputPath)
			if err != nil {
				return fmt.Errorf("create output file: %w", err)
			}
			defer func() { _ = f.Close() }()
			_, err = f.Write(data)
			return err
		},
	}

	cmd.Flags().StringVar(&inputPath, "input", "", "Path to verdict YAML or JSON file")
	cmd.Flags().StringVar(&outputPath, "output", "", "Path to write converted output (default: stdout)")
	cmd.Flags().StringVar(&format, "format", "", "Target format: yaml or json")

	return cmd
}

func loadReport(path string) (*evaluation.Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read input: %w", err)
	}

	var report evaluation.Report

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		err = json.Unmarshal(data, &report)
	default:
		// Try YAML first, fall back to JSON.
		err = yaml.Unmarshal(data, &report)
		if err != nil {
			err = json.Unmarshal(data, &report)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("deserialize report: %w", err)
	}
	return &report, nil
}
