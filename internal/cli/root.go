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
It validates domain profiles and scenario files, and (in phase 2) executes
full OASIS evaluations against agents.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(newRunCommand())
	root.AddCommand(newValidateCommand())
	root.AddCommand(newVersionCommand())
	root.AddCommand(newReportCommand())

	return root
}
