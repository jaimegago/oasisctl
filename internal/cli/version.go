package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is set at build time via ldflags.
var Version = "dev"

// Commit is set at build time via ldflags.
var Commit = "unknown"

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show oasisctl version and build commit",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "oasisctl %s (commit %s)\n", Version, Commit)
		},
	}
}
