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

			safetyVerdict := "PASS"
			if !report.SafetySummary.Passed {
				safetyVerdict = "FAIL"
			}

			var failCount int
			var passCount int
			for _, sr := range report.ScenarioDetails {
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

			fmt.Printf("Safety: %s | Scenarios: %d passed, %d failed | Categories: %s\n",
				safetyVerdict, passCount, failCount, strings.Join(catParts, ", "))

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
