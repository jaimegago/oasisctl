package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/execution"
	"github.com/jaimegago/oasisctl/internal/record"
)

// validateOpening refuses, before anything is contacted, what an evaluation
// cannot be opened with.
func (o *targetOptions) validateOpening() error {
	if o.tier < 1 || o.tier > 3 {
		return fmt.Errorf("--tier must be 1, 2, or 3")
	}
	if o.safetyCount == 0 || o.capabilityCount == 0 {
		return fmt.Errorf("--safety-count and --capability-count are required to open an evaluation: the counts are declared when it is opened and are the evaluator's to choose (spec/01-core.md §2.4)")
	}
	return nil
}

func newRunCommand() *cobra.Command {
	var (
		opts        targetOptions
		dryRun      bool
		openBrowser bool
	)

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Drive an OASIS evaluation to completion, or resume one",
		Long: `run opens an evaluation in --evaluation-dir, or resumes the one already
there, makes every execution it is owed, and assembles the verdict and the
report.

An evaluation is not one process. It is opened with its identity and its two
execution counts declared, and then executed one scenario at a time: every
applicable safety scenario to the safety count, and, behind a safety verdict
of PASS and never before it, every applicable capability scenario to the
capability count. A process that stops anywhere loses at most the execution
it was making, which is recorded as an attempt; run the same command again to
continue from the record.

An evaluation executes the whole scenario suite applicable to the agent's
configuration at its tier. To execute a selection of scenarios, outside any
evaluation, use "oasisctl try".

Exit codes: 0 the evaluation is complete and its safety verdict is PASS; 1 an
error, or a safety verdict of FAIL; 2 the evaluation is incomplete and can be
resumed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.applyConfig(cmd); err != nil {
				return err
			}
			if opts.evaluationDir == "" {
				return fmt.Errorf("--evaluation-dir is required: an evaluation's record is kept where the caller says")
			}
			if opts.format == "html" && opts.outputPath == "" {
				return fmt.Errorf("html format requires --output path")
			}
			resuming := record.Exists(opts.evaluationDir)
			if !resuming {
				if err := opts.validateOpening(); err != nil {
					return err
				}
			}

			ctx := context.Background()
			b, err := opts.build(ctx, !dryRun, execution.Config{})
			if err != nil {
				return err
			}

			if dryRun {
				return dryRunSummary(ctx, &opts, b, resuming)
			}

			outputPath := opts.evaluationOutput()
			verdict, summary, runErr := b.orchestrator.Run(ctx, opts.profilePath, b.scenarios, opts.providerURL, opts.format, outputPath)
			if verdict != nil {
				absPath, _ := filepath.Abs(outputPath)
				fmt.Fprintf(os.Stderr, "verdict and report written to %s\n", absPath)
				if opts.format == "html" && openBrowser {
					openInBrowser(absPath)
				}
				printEvaluation(verdict)
			}
			if runErr != nil {
				return fmt.Errorf("run evaluation: %w", runErr)
			}
			return finishEvaluationCommand(verdict, summary)
		},
	}

	opts.addProfileFlags(cmd)
	opts.addConnectionFlags(cmd)
	opts.addEvaluationDirFlag(cmd)
	opts.addOpeningFlags(cmd)
	opts.addLabFlags(cmd)
	opts.addOutputFlags(cmd, "default: verdict.<format> in the evaluation directory")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate inputs and say what would be executed, without contacting the agent or the provider")
	cmd.Flags().BoolVar(&openBrowser, "open", false, "Open HTML report in default browser")

	return cmd
}

// finishEvaluationCommand sets the exit code from where the evaluation
// stands: a safety FAIL exits 1, an incomplete evaluation exits 2.
func finishEvaluationCommand(verdict *evaluation.Verdict, summary *execution.DriveSummary) error {
	if verdict == nil {
		return nil
	}
	if summary != nil && len(summary.Stalled) > 0 {
		fmt.Fprintf(os.Stderr, "%d scenario(s) were left short of their count because their executions kept reaching no verdict; run again to continue:\n", len(summary.Stalled))
		for _, id := range summary.Stalled {
			fmt.Fprintf(os.Stderr, "  - %s\n", id)
		}
	}
	if verdict.Safety == evaluation.SafetyVerdictFail {
		fmt.Fprintln(os.Stderr, "safety gate FAILED")
		os.Exit(1)
	}
	if verdict.Evaluation != nil && !verdict.Evaluation.Complete {
		fmt.Fprintln(os.Stderr, "the evaluation is incomplete; run again to continue it")
		os.Exit(exitIncomplete)
	}
	return nil
}

// dryRunSummary says what an evaluation would execute, without contacting
// the agent or the provider. Applicability depends on the configuration the
// agent reports, so the figures are for the whole suite at the tier.
func dryRunSummary(ctx context.Context, opts *targetOptions, b *built, resuming bool) error {
	scenarios := b.scenarios
	if resuming {
		session, err := b.orchestrator.LoadEvaluation(ctx, opts.profilePath, b.scenarios)
		if err != nil {
			return err
		}
		defer session.Close()
		st, err := session.Status()
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "dry-run: evaluation %s would be resumed: %d safety and %d capability execution(s) are owed\n",
			st.Evaluation, st.SafetyOwed, st.CapabilityOwed)
		return nil
	}
	safety, capability := 0, 0
	for _, s := range scenarios {
		if s.Tier > opts.tier {
			continue
		}
		if s.Classification == evaluation.ClassificationSafety {
			safety++
		} else {
			capability++
		}
	}
	fmt.Fprintf(os.Stderr, "dry-run: an evaluation would be opened at tier %d over %d safety and %d capability scenario(s), "+
		"less those the agent's configuration excludes: up to %d safety and %d capability counted execution(s)\n",
		opts.tier, safety, capability, safety*opts.safetyCount, capability*opts.capabilityCount)
	return nil
}

func newEvaluationCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "evaluation",
		Short: "Open an evaluation, execute it one scenario at a time, and assemble it",
		Long: `An evaluation is opened before its first execution, with its identity and
its two execution counts declared, and is then executed one scenario at a
time, in any order within safety and within capability, by any number of
processes on any number of days. These commands are the single steps;
"oasisctl run" is all of them in order.`,
	}
	cmd.AddCommand(newEvaluationOpenCommand())
	cmd.AddCommand(newEvaluationExecuteCommand())
	cmd.AddCommand(newEvaluationStatusCommand())
	cmd.AddCommand(newEvaluationAssembleCommand())
	return cmd
}

func newEvaluationOpenCommand() *cobra.Command {
	var opts targetOptions
	cmd := &cobra.Command{
		Use:   "open",
		Short: "Open an evaluation: record its name, its identity and its two execution counts",
		Long: `open reads the agent's identity and configuration, checks the provider it
is pointed at as the evaluation's first lab, and records the evaluation's
name, its identity and its two declared execution counts in --evaluation-dir.
None of them changes afterwards. No execution is made.

A safety count below 3 or below the profile's safety minimum is refused, and
so is a capability count below the profile's capability minimum.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.applyConfig(cmd); err != nil {
				return err
			}
			if opts.evaluationDir == "" {
				return fmt.Errorf("--evaluation-dir is required: an evaluation's record is kept where the caller says")
			}
			if err := opts.validateOpening(); err != nil {
				return err
			}
			ctx := context.Background()
			b, err := opts.build(ctx, true, execution.Config{})
			if err != nil {
				return err
			}
			session, err := b.orchestrator.OpenEvaluation(ctx, opts.profilePath, b.scenarios, opts.providerURL)
			if err != nil {
				return err
			}
			defer session.Close()
			return printYAML(session.Opening())
		},
	}
	opts.addProfileFlags(cmd)
	opts.addConnectionFlags(cmd)
	opts.addEvaluationDirFlag(cmd)
	opts.addOpeningFlags(cmd)
	cmd.Flags().StringVar(&opts.lab, "lab", "", "Name to record the provider under as the evaluation's first lab (default lab-1)")
	return cmd
}

func newEvaluationExecuteCommand() *cobra.Command {
	var (
		opts       targetOptions
		scenarioID string
	)
	cmd := &cobra.Command{
		Use:   "execute",
		Short: "Make one execution of one scenario for an evaluation",
		Long: `execute makes one execution of --scenario for the evaluation in
--evaluation-dir and prints the execution's record.

Before it executes, in this order: it reads the agent's identity and
configuration and makes no execution if any element differs from the
evaluation's; it makes no execution of a scenario that is not applicable; it
makes no execution of a scenario that already holds its declared count; and,
for a capability scenario, it makes no execution unless the evaluation's
safety verdict is PASS. In each of those cases nothing is added to the
evaluation and the command exits 1.

An execution that is made and reaches no verdict is an attempt: it is
recorded with its outcome and cause, it is not counted, and the command exits
0. Execute the scenario again.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.applyConfig(cmd); err != nil {
				return err
			}
			if opts.evaluationDir == "" {
				return fmt.Errorf("--evaluation-dir is required")
			}
			if scenarioID == "" {
				return fmt.Errorf("--scenario is required: one execution is made of one scenario")
			}
			ctx := context.Background()
			b, err := opts.build(ctx, true, execution.Config{})
			if err != nil {
				return err
			}
			session, err := b.orchestrator.ResumeEvaluation(ctx, opts.profilePath, b.scenarios, opts.providerURL)
			if err != nil {
				return err
			}
			defer session.Close()
			out, err := session.Execute(ctx, scenarioID)
			if err != nil {
				var mismatch *evaluation.IdentityMismatchError
				if errors.As(err, &mismatch) {
					return fmt.Errorf("the execution was not made and nothing was added to the evaluation: %w", err)
				}
				return fmt.Errorf("the execution was not made: %w", err)
			}
			if out.SafetyVerdict != nil {
				fmt.Fprintf(os.Stderr, "the last safety execution owed was made: the safety verdict record is written, safety %s\n", *out.SafetyVerdict)
			}
			return printYAML(out.Record)
		},
	}
	opts.addProfileFlags(cmd)
	opts.addConnectionFlags(cmd)
	opts.addEvaluationDirFlag(cmd)
	opts.addLabFlags(cmd)
	cmd.Flags().StringVar(&scenarioID, "scenario", "", "Identifier of the scenario to execute once")
	return cmd
}

func newEvaluationStatusCommand() *cobra.Command {
	var opts targetOptions
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Say what an evaluation holds and what it is still owed",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.applyConfig(cmd); err != nil {
				return err
			}
			if opts.evaluationDir == "" {
				return fmt.Errorf("--evaluation-dir is required")
			}
			ctx := context.Background()
			b, err := opts.build(ctx, false, execution.Config{})
			if err != nil {
				return err
			}
			session, err := b.orchestrator.LoadEvaluation(ctx, opts.profilePath, b.scenarios)
			if err != nil {
				return err
			}
			defer session.Close()
			st, err := session.Status()
			if err != nil {
				return err
			}
			return printYAML(st)
		},
	}
	opts.addProfileFlags(cmd)
	opts.addEvaluationDirFlag(cmd)
	return cmd
}

func newEvaluationAssembleCommand() *cobra.Command {
	var (
		opts        targetOptions
		openBrowser bool
	)
	cmd := &cobra.Command{
		Use:   "assemble",
		Short: "Assemble an evaluation's verdict and report from its record",
		Long: `assemble builds the verdict and the report from the record in
--evaluation-dir. It may be run at any time, including while the evaluation is
incomplete, in which case the verdict and the report say so; it changes
nothing in the record.

An evaluation that does not agree with itself is refused and no verdict is
emitted: a result whose identity differs from the evaluation's, a result that
names another evaluation, a scenario holding more counted executions than
declared, a capability result that does not cite the hash of the safety
verdict record the evaluation contains.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.applyConfig(cmd); err != nil {
				return err
			}
			if opts.evaluationDir == "" {
				return fmt.Errorf("--evaluation-dir is required")
			}
			if opts.format == "html" && opts.outputPath == "" {
				return fmt.Errorf("html format requires --output path")
			}
			ctx := context.Background()
			b, err := opts.build(ctx, false, execution.Config{})
			if err != nil {
				return err
			}
			session, err := b.orchestrator.LoadEvaluation(ctx, opts.profilePath, b.scenarios)
			if err != nil {
				return err
			}
			defer session.Close()
			outputPath := opts.evaluationOutput()
			verdict, err := session.Assemble(ctx, opts.format, outputPath)
			if err != nil {
				return err
			}
			absPath, _ := filepath.Abs(outputPath)
			fmt.Fprintf(os.Stderr, "verdict and report written to %s\n", absPath)
			if opts.format == "html" && openBrowser {
				openInBrowser(absPath)
			}
			printEvaluation(verdict)
			return nil
		},
	}
	opts.addProfileFlags(cmd)
	opts.addEvaluationDirFlag(cmd)
	opts.addOutputFlags(cmd, "default: verdict.<format> in the evaluation directory")
	cmd.Flags().BoolVar(&openBrowser, "open", false, "Open HTML report in default browser")
	return cmd
}

func printYAML(v interface{}) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}
