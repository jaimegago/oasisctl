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

			// Scenarios by what each came to. A NOT_APPLICABLE scenario was
			// not executed and is neither a pass nor a failure
			// (spec/01-core.md §3.6.1); neither is one short of its count, or
			// an execution that reached no verdict (§2.5).
			var passCount, failCount, shortCount, notApplicableCount int
			for _, sr := range report.ScenarioDetails {
				switch {
				case sr.Status == evaluation.ScenarioNotApplicable:
					notApplicableCount++
				// An attempt, a scenario short of its count, and a status a
				// file written before core 1.0.0-rc1.14 may carry
				// (UNEVALUABLE, PROVIDER_FAILURE) have no result. A record
				// with no status at all is one from before statuses were
				// written, and is read by its passed flag as it always was.
				case sr.IsAttempt() || sr.ShortOfCount || (sr.Status != "" && !sr.HasResult()):
					shortCount++
				case sr.Passed:
					passCount++
				default:
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

			// What the document is, before anything else: an evaluation by
			// name, complete or incomplete, with its date as a range and its
			// declared counts; or executions made outside any evaluation.
			md := report.Metadata
			if md.Evaluation != nil {
				state := "complete"
				if !md.EvaluationMode.Complete {
					state = "INCOMPLETE"
				}
				fmt.Printf("Evaluation %s: %s.", *md.Evaluation, state)
				if md.DateRange != nil {
					fmt.Printf(" Date: %s.", md.DateRange)
				}
				if md.ExecutionCounts != nil {
					fmt.Printf(" Declared counts: safety %d, capability %d.", md.ExecutionCounts.Safety, md.ExecutionCounts.Capability)
				}
				fmt.Println()
			}
			switch {
			case evaluation.SafetyVerdict(safetyVerdict).IsVerdict():
				// Before the verdict word: what the verdict is a claim about
				// (spec/05-reporting.md §2.3).
				fmt.Println(evaluation.SafetyClaimPreface(report.AgentConfiguration, report.SafetySummary.Applicable, report.SafetySummary.ExecutionsPerScenario))
				if report.SafetySummary.Containment != nil {
					fmt.Println(evaluation.ContainmentStatement(report.SafetySummary.Containment))
				}
			case md.Evaluation != nil:
				fmt.Println(evaluation.SafetyIncompleteWarning(report.SafetySummary.ShortOfCount))
			default:
				fmt.Println(evaluation.SafetyNotEvaluatedWarning)
			}
			printAttempts(os.Stdout, md)
			short := ""
			if shortCount > 0 {
				short = fmt.Sprintf(", %d with no result", shortCount)
			}
			notApplicable := ""
			if notApplicableCount > 0 {
				notApplicable = fmt.Sprintf(", %d not applicable", notApplicableCount)
			}
			fmt.Printf("Safety: %s | Scenarios: %d passed, %d failed%s%s | Attempts: %d | Categories: %s%s\n",
				safetyVerdict, passCount, failCount, short, notApplicable, md.Attempts, strings.Join(catParts, ", "), uncategorized)

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

			// Whether what was rendered supports a conformance claim, and
			// the claim where it does (spec/05-reporting.md §3.2). A file
			// written before this was recorded carries none, and prints none.
			if claim := report.Metadata.ConformanceClaim; claim != nil {
				if claim.Supported && claim.Claim != nil {
					fmt.Println("Conformance claim supported: " + claim.Claim.Text)
				} else {
					fmt.Printf("Conformance claim: NOT supported — %s.\n", strings.Join(claim.Reasons, "; "))
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&inputPath, "input", "", "Path to verdict YAML or JSON file")

	return cmd
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
