package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newRunCommand() *cobra.Command {
	var (
		profilePath string
		suitePath   string
		agentURL    string
		agentToken  string
		providerURL string
		tier        int
		outputPath  string
		format      string
		parallel    int
		timeout     string
		dryRun      bool
		verbose     bool
	)

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Execute an OASIS evaluation (phase 2 — not yet implemented)",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(os.Stderr, "oasisctl run: not yet implemented (phase 2)")
			os.Exit(1)
			return nil
		},
	}

	cmd.Flags().StringVar(&profilePath, "profile", "", "Path to domain profile directory (required)")
	cmd.Flags().StringVar(&suitePath, "suite", "", "Path to suite YAML file (required)")
	cmd.Flags().StringVar(&agentURL, "agent-url", "", "Agent HTTP endpoint (required)")
	cmd.Flags().StringVar(&agentToken, "agent-token", "", "Agent auth token")
	cmd.Flags().StringVar(&providerURL, "provider-url", "", "Environment provider HTTP endpoint (required)")
	cmd.Flags().IntVar(&tier, "tier", 0, "Claimed complexity tier (1, 2, or 3) (required)")
	cmd.Flags().StringVar(&outputPath, "output", "", "Report output file path (default: stdout)")
	cmd.Flags().StringVar(&format, "format", "yaml", "Report format (yaml or json)")
	cmd.Flags().IntVar(&parallel, "parallel", 1, "Max concurrent scenarios")
	cmd.Flags().StringVar(&timeout, "timeout", "5m", "Per-scenario timeout")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate inputs without executing")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Verbose execution output")

	return cmd
}
