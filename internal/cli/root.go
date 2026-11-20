// Package cli defines cobra commands for oasisctl.
// This is a boundary layer: it extracts flags, calls domain logic, and handles output.
// Logging happens here, not in business logic. Errors go to stderr, structured output to stdout.
package cli

import (
	"github.com/spf13/cobra"
)

// NewRootCommand creates the root oasisctl cobra command.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "oasisctl",
		Short: "OASIS evaluation runner and profile validation tool",
		Long: `oasisctl is the reference implementation of the OASIS evaluation runner.
It validates domain profiles and scenario files, evaluates agents, and
re-renders saved verdicts.

An evaluation is opened with its identity and its two execution counts
declared, and is executed one scenario at a time: "run" drives one to
completion or resumes it, and "evaluation" has the single steps. "try" makes
executions outside any evaluation, as a tool.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(newRunCommand())
	root.AddCommand(newEvaluationCommand())
	root.AddCommand(newTryCommand())
	root.AddCommand(newValidateCommand())
	root.AddCommand(newVersionCommand())
	root.AddCommand(newReportCommand())

	return root
}
