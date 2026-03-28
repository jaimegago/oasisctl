package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/jaimegago/oasisctl/internal/agent"
	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/execution"
	"github.com/jaimegago/oasisctl/internal/profile"
	"github.com/jaimegago/oasisctl/internal/provider"
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
		Short: "Execute an OASIS evaluation",
		RunE: func(cmd *cobra.Command, args []string) error {
			// 1. Validate required flags.
			if profilePath == "" {
				return fmt.Errorf("--profile is required")
			}
			if suitePath == "" {
				return fmt.Errorf("--suite is required")
			}
			if agentURL == "" {
				return fmt.Errorf("--agent-url is required")
			}
			if providerURL == "" {
				return fmt.Errorf("--provider-url is required")
			}
			if tier < 1 || tier > 3 {
				return fmt.Errorf("--tier must be 1, 2, or 3")
			}

			// 2. Parse timeout.
			timeoutDur, err := time.ParseDuration(timeout)
			if err != nil {
				return fmt.Errorf("invalid --timeout: %w", err)
			}

			ctx := context.Background()

			// 3a. Load profile first (AssertionEngine needs it).
			loader := profile.NewLoader()
			loadedProfile, err := loader.Load(ctx, profilePath)
			if err != nil {
				return fmt.Errorf("load profile: %w", err)
			}

			// 3b. Load scenarios from profile directory.
			scenarios, err := loadAllScenarios(ctx, profilePath)
			if err != nil {
				return fmt.Errorf("load scenarios: %w", err)
			}

			// 3c. Create dependencies.
			agentClient := agent.NewHTTPClient(agentURL, agentToken)
			providerClient := provider.NewHTTPClient(providerURL)
			asserter := execution.NewAssertionEngine(loadedProfile)
			scorer := execution.NewScorer()
			reporter := execution.NewReportWriter()

			// 4. Create orchestrator.
			logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
			cfg := execution.Config{
				Tier:     tier,
				Parallel: parallel,
				Timeout:  timeoutDur,
				DryRun:   dryRun,
				Verbose:  verbose,
			}
			orch := execution.NewOrchestrator(loader, agentClient, providerClient, asserter, scorer, reporter, logger, cfg)

			// 5. Run evaluation.
			verdict, err := orch.Run(ctx, profilePath, scenarios, agentURL, providerURL, format, outputPath)
			if err != nil {
				return err
			}

			// 6. Exit with appropriate code.
			if verdict != nil && !verdict.SafetyPassed && !dryRun {
				fmt.Fprintln(os.Stderr, "safety gate FAILED")
				os.Exit(1)
			}
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
	cmd.Flags().IntVar(&parallel, "parallel", 1, "Max concurrent scenarios (parallelism deferred to future version)")
	cmd.Flags().StringVar(&timeout, "timeout", "5m", "Per-scenario timeout")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate inputs without executing")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Verbose execution output")

	return cmd
}

// loadAllScenarios loads all safety and capability scenarios from the profile directory.
func loadAllScenarios(ctx context.Context, profilePath string) ([]evaluation.Scenario, error) {
	sp := profile.NewScenarioParser()
	var all []evaluation.Scenario
	for _, subdir := range []string{"scenarios/safety", "scenarios/capability"} {
		dir := filepath.Join(profilePath, subdir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
				continue
			}
			ss, err := sp.Parse(ctx, filepath.Join(dir, e.Name()))
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", e.Name(), err)
			}
			all = append(all, ss...)
		}
	}
	return all, nil
}
