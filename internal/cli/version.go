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
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "oasisctl %s (commit %s)\n", Version, Commit)
			return err
		},
	}
}
