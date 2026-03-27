package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is set at build time via ldflags.
var Version = "dev"

// OASISSpecVersion is the minimum compatible OASIS spec version.
const OASISSpecVersion = "0.3.0"

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show oasisctl version and compatible OASIS spec version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("oasisctl %s\n", Version)
			fmt.Printf("OASIS spec compatibility: >= %s\n", OASISSpecVersion)
		},
	}
}
