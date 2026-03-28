package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/profile"
	"github.com/jaimegago/oasisctl/internal/validation"
)

func newValidateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate OASIS artifacts",
	}

	cmd.AddCommand(newValidateProfileCommand())
	cmd.AddCommand(newValidateScenarioCommand())
	return cmd
}

func newValidateProfileCommand() *cobra.Command {
	var (
		path   string
		report bool
	)

	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Validate a domain profile directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			if path == "" {
				return fmt.Errorf("--path is required")
			}

			ctx := context.Background()
			logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

			loader := profile.NewLoader()
			p, err := loader.Load(ctx, path)
			if err != nil {
				logger.Error("profile validation failed", "path", path, "error", err)
				return err
			}

			// Load scenarios for intent/subcategory analysis.
			scenarioParser := profile.NewScenarioParser()
			var allScenarios []evaluation.Scenario
			for _, subdir := range []string{"safety", "capability"} {
				dir := filepath.Join(path, "scenarios", subdir)
				if info, statErr := os.Stat(dir); statErr == nil && info.IsDir() {
					entries, readErr := os.ReadDir(dir)
					if readErr != nil {
						continue
					}
					for _, e := range entries {
						if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
							continue
						}
						scenarios, parseErr := scenarioParser.Parse(ctx, filepath.Join(dir, e.Name()))
						if parseErr != nil {
							continue
						}
						allScenarios = append(allScenarios, scenarios...)
					}
				}
			}

			if report {
				fmt.Printf("Profile: %s (%s)\n", p.Metadata.Name, p.Metadata.Version)
				fmt.Printf("Behaviors defined: %d\n", len(p.BehaviorDefinitions))
				fmt.Printf("Stimuli in library: %d\n", len(p.StimulusLibrary))
				fmt.Printf("Subcategories defined: %d\n", len(p.Subcategories))

				if len(allScenarios) > 0 {
					// Intent coverage analysis.
					intentReport := validation.ComputeIntentCoverage(allScenarios, p.IntentPromotion)
					fmt.Printf("\nIntent coverage:\n")
					fmt.Printf("  Total scenarios: %d\n", intentReport.Total)
					fmt.Printf("  With intent: %d\n", intentReport.WithIntent)
					if len(intentReport.MissingRequired) > 0 {
						fmt.Printf("  Missing (required): %d\n", len(intentReport.MissingRequired))
						for _, id := range intentReport.MissingRequired {
							fmt.Printf("    - %s\n", id)
						}
					}
					if len(intentReport.MissingRecommended) > 0 {
						fmt.Printf("  Missing (recommended): %d\n", len(intentReport.MissingRecommended))
					}
					if len(intentReport.DuplicateIntents) > 0 {
						fmt.Printf("  Duplicate intents: %d\n", len(intentReport.DuplicateIntents))
					}

					// Subcategory distribution analysis.
					if len(p.Subcategories) > 0 {
						subcatReport := validation.ComputeSubcategoryDistribution(allScenarios, p.Subcategories)
						fmt.Printf("\nSubcategory distribution:\n")
						for name, count := range subcatReport.PerSubcategory {
							fmt.Printf("  %s: %d scenarios\n", name, count)
						}
						if len(subcatReport.UnusedSubcategories) > 0 {
							fmt.Printf("  Unused subcategories: %v\n", subcatReport.UnusedSubcategories)
						}
						if len(subcatReport.Unassigned) > 0 {
							fmt.Printf("  Scenarios without subcategory: %d\n", len(subcatReport.Unassigned))
						}
					}
				}
			}

			fmt.Fprintf(os.Stderr, "profile %s: valid\n", path)
			return nil
		},
	}

	cmd.Flags().StringVar(&path, "path", "", "Path to domain profile directory (required)")
	cmd.Flags().BoolVar(&report, "report", false, "Output detailed quality analysis")
	return cmd
}

func newValidateScenarioCommand() *cobra.Command {
	var (
		path        string
		profilePath string
	)

	cmd := &cobra.Command{
		Use:   "scenario",
		Short: "Lint a scenario YAML file",
		RunE: func(cmd *cobra.Command, args []string) error {
			if path == "" {
				return fmt.Errorf("--path is required")
			}

			ctx := context.Background()
			logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

			parser := profile.NewScenarioParser()
			scenarios, err := parser.Parse(ctx, path)
			if err != nil {
				logger.Error("scenario parse failed", "path", path, "error", err)
				return err
			}

			var errCount int
			for _, s := range scenarios {
				if verr := validation.ValidateScenario(s); verr != nil && verr.HasIssues() {
					for _, issue := range verr.Issues {
						fmt.Fprintf(os.Stderr, "scenario %s: %s: %s\n", s.ID, issue.Field, issue.Message)
					}
					errCount++
				}
			}

			if profilePath != "" {
				loader := profile.NewLoader()
				p, err := loader.Load(ctx, profilePath)
				if err != nil {
					logger.Error("profile load failed", "path", profilePath, "error", err)
					return err
				}
				if verr := validation.ValidateProfile(p, scenarios); verr != nil && verr.HasIssues() {
					for _, issue := range verr.Issues {
						fmt.Fprintf(os.Stderr, "%s: %s\n", issue.Field, issue.Message)
					}
					errCount++
				}
			}

			if errCount > 0 {
				return fmt.Errorf("%d scenario(s) failed validation", errCount)
			}

			fmt.Fprintf(os.Stderr, "scenario file %s: valid (%d scenarios)\n", path, len(scenarios))
			return nil
		},
	}

	cmd.Flags().StringVar(&path, "path", "", "Path to scenario YAML file (required)")
	cmd.Flags().StringVar(&profilePath, "profile", "", "Profile directory for cross-reference validation (optional)")
	return cmd
}
