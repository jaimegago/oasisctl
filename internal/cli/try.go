package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/execution"
)

func newTryCommand() *cobra.Command {
	var (
		opts          targetOptions
		suitePath     string
		evidenceDir   string
		dryRun        bool
		safetyOnly    bool
		categories    []string
		subcategories []string
		scenarioIDs   []string
		openBrowser   bool
	)

	cmd := &cobra.Command{
		Use:   "try",
		Short: "Execute selected scenarios once each, outside any evaluation",
		Long: `try makes one execution of each selected scenario OUTSIDE ANY EVALUATION.

It is a tool for a quick check of behaviour: a single category, a single
scenario. The executions are made for no evaluation and are never added to
one. Nothing they produce is a safety verdict, a capability score an
evaluation reports, or support for a conformance claim: one execution of a
scenario is short of every count the specification sets. The report says so
before anything else, carries no verdict block, and reports safety as
NOT_EVALUATED.

To evaluate an agent, use "oasisctl run".

Exit codes: 0 the executions were made; 1 an error, or a safety execution
that reached FAIL.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.applyConfig(cmd); err != nil {
				return err
			}
			if opts.tier < 1 || opts.tier > 3 {
				return fmt.Errorf("--tier must be 1, 2, or 3")
			}
			if opts.format == "html" && opts.outputPath == "" {
				return fmt.Errorf("html format requires --output path")
			}
			if opts.configPath != "" && !cmd.Flags().Changed("scenario") {
				if rc, err := evaluation.LoadRunConfig(opts.configPath); err == nil && len(rc.Evaluation.Scenarios) > 0 {
					scenarioIDs = rc.Evaluation.Scenarios
				}
			}
			if opts.configPath != "" && !cmd.Flags().Changed("evidence-dir") {
				if rc, err := evaluation.LoadRunConfig(opts.configPath); err == nil && rc.Output.EvidenceDir != "" {
					evidenceDir = rc.Output.EvidenceDir
				}
			}

			ctx := context.Background()
			b, err := opts.build(ctx, !dryRun, execution.Config{
				DryRun:        dryRun,
				SafetyOnly:    safetyOnly,
				Categories:    categories,
				Subcategories: subcategories,
				ScenarioIDs:   scenarioIDs,
				EvidenceDir:   evidenceDir,
			})
			if err != nil {
				return err
			}
			scenarios := b.scenarios
			if suitePath != "" {
				suite, serr := loadSuite(suitePath)
				if serr != nil {
					return fmt.Errorf("load suite: %w", serr)
				}
				scenarios, serr = filterBySuite(suite, scenarios)
				if serr != nil {
					return serr
				}
			}

			verdict, err := b.orchestrator.Try(ctx, opts.profilePath, scenarios, opts.agentURL, opts.providerURL, opts.format, opts.outputPath)
			if err != nil {
				return fmt.Errorf("execute scenarios: %w", err)
			}
			if dryRun {
				return nil
			}

			if opts.format == "html" && openBrowser && opts.outputPath != "" {
				absPath, _ := filepath.Abs(opts.outputPath)
				fmt.Fprintf(os.Stderr, "report written to %s\n", absPath)
				openInBrowser(absPath)
			}

			// Said where an operator watching will see it, and last, so it is
			// what is left on the screen: these executions are no evaluation.
			if verdict != nil && verdict.Report != nil {
				printAttempts(os.Stderr, verdict.Report.Metadata)
			}
			fmt.Fprintln(os.Stderr, evaluation.SafetyNotEvaluatedWarning)
			if verdict != nil {
				for _, r := range verdict.SafetyResults {
					if r.Status == evaluation.ScenarioFail {
						fmt.Fprintln(os.Stderr, "a safety execution reached FAIL")
						os.Exit(1)
					}
				}
			}
			return nil
		},
	}

	opts.addProfileFlags(cmd)
	opts.addConnectionFlags(cmd)
	opts.addOutputFlags(cmd, "default: stdout")
	cmd.Flags().IntVar(&opts.tier, "tier", 0, "Complexity tier (1, 2, or 3)")
	cmd.Flags().StringVar(&suitePath, "suite", "", "Path to suite YAML file")
	cmd.Flags().StringVar(&evidenceDir, "evidence-dir", "", "Directory for evidence artifacts (default: alongside --output, or the working directory)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate inputs without executing")
	cmd.Flags().BoolVar(&safetyOnly, "safety-only", false, "Execute only safety scenarios")
	cmd.Flags().StringSliceVar(&categories, "category", nil, "Select scenarios by category (repeatable)")
	cmd.Flags().StringSliceVar(&subcategories, "subcategory", nil, "Select scenarios by subcategory (repeatable)")
	cmd.Flags().StringSliceVar(&scenarioIDs, "scenario", nil, "Select scenarios by ID glob pattern (repeatable)")
	cmd.Flags().BoolVar(&openBrowser, "open", false, "Open HTML report in default browser")

	return cmd
}
