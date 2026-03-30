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
		configPath    string
		profilePath   string
		suitePath     string
		agentURL      string
		agentToken    string
		agentAdapter  string
		agentCommand  string
		providerURL   string
		tier          int
		outputPath    string
		format        string
		parallel      int
		timeout       string
		dryRun        bool
		verbose       bool
		safetyOnly    bool
		categories    []string
		subcategories []string
	)

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Execute an OASIS evaluation",
		RunE: func(cmd *cobra.Command, args []string) error {
			// 1. Load config file if provided.
			if configPath != "" {
				rc, err := evaluation.LoadRunConfig(configPath)
				if err != nil {
					return fmt.Errorf("load config: %w", err)
				}
				// Apply config as defaults; CLI flags override.
				if profilePath == "" {
					profilePath = rc.Profile.Path
				}
				if agentURL == "" {
					agentURL = rc.Agent.URL
				}
				if agentToken == "" {
					agentToken = rc.Agent.Token
				}
				if agentAdapter == "" {
					agentAdapter = rc.Agent.Adapter
				}
				if agentCommand == "" {
					agentCommand = rc.Agent.Command
				}
				if providerURL == "" {
					providerURL = rc.Environment.URL
				}
				if !cmd.Flags().Changed("tier") {
					tier = rc.Evaluation.Tier
				}
				if !cmd.Flags().Changed("format") && rc.Output.Format != "" {
					format = rc.Output.Format
				}
				if !cmd.Flags().Changed("output") && rc.Output.Path != "" {
					outputPath = rc.Output.Path
				}
				if !cmd.Flags().Changed("parallel") && rc.Evaluation.Parallel > 0 {
					parallel = rc.Evaluation.Parallel
				}
				if !cmd.Flags().Changed("timeout") && rc.Evaluation.Timeout != "" {
					timeout = rc.Evaluation.Timeout
				}
			}

			// 2. Validate required values.
			if profilePath == "" {
				return fmt.Errorf("--profile is required")
			}
			if agentURL == "" && agentCommand == "" {
				return fmt.Errorf("--agent-url (or agent.command in config) is required")
			}
			if providerURL == "" {
				return fmt.Errorf("--provider-url is required")
			}
			if tier < 1 || tier > 3 {
				return fmt.Errorf("--tier must be 1, 2, or 3")
			}

			// 3. Parse timeout.
			timeoutDur, err := time.ParseDuration(timeout)
			if err != nil {
				return fmt.Errorf("invalid --timeout: %w", err)
			}

			ctx := context.Background()

			// 4a. Load profile first (AssertionEngine needs it).
			loader := profile.NewLoader()
			loadedProfile, err := loader.Load(ctx, profilePath)
			if err != nil {
				return fmt.Errorf("load profile: %w", err)
			}

			// 4b. Load scenarios from profile directory.
			scenarios, err := loadAllScenarios(ctx, profilePath)
			if err != nil {
				return fmt.Errorf("load scenarios: %w", err)
			}

			// 4c. Create agent client via adapter factory.
			agentCfg := agent.AgentConfig{
				Adapter: agentAdapter,
				URL:     agentURL,
				Token:   agentToken,
				Command: agentCommand,
				Timeout: timeoutDur,
			}
			agentClient, err := agent.NewClient(agentCfg)
			if err != nil {
				return fmt.Errorf("create agent client: %w", err)
			}

			providerClient := provider.NewHTTPClient(providerURL)
			asserter := execution.NewAssertionEngine(loadedProfile)
			scorer := execution.NewScorer()
			reporter := execution.NewReportWriter()

			// 5. Create orchestrator.
			logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
			cfg := execution.Config{
				Tier:          tier,
				Parallel:      parallel,
				Timeout:       timeoutDur,
				DryRun:        dryRun,
				Verbose:       verbose,
				SafetyOnly:    safetyOnly,
				Categories:    categories,
				Subcategories: subcategories,
			}
			orch := execution.NewOrchestrator(loader, agentClient, providerClient, asserter, scorer, reporter, logger, cfg)

			// 6. Run evaluation.
			verdict, err := orch.Run(ctx, profilePath, scenarios, agentURL, providerURL, format, outputPath)
			if err != nil {
				return fmt.Errorf("run evaluation: %w", err)
			}

			// 7. Exit with appropriate code.
			if verdict != nil && !verdict.SafetyPassed && !dryRun {
				fmt.Fprintln(os.Stderr, "safety gate FAILED")
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "Path to run configuration YAML file")
	cmd.Flags().StringVar(&profilePath, "profile", "", "Path to domain profile directory")
	cmd.Flags().StringVar(&suitePath, "suite", "", "Path to suite YAML file")
	cmd.Flags().StringVar(&agentURL, "agent-url", "", "Agent HTTP endpoint")
	cmd.Flags().StringVar(&agentToken, "agent-token", "", "Agent auth token")
	cmd.Flags().StringVar(&agentAdapter, "agent-adapter", "", "Agent adapter type (http, mcp, cli)")
	cmd.Flags().StringVar(&agentCommand, "agent-command", "", "Agent CLI binary path (for cli adapter)")
	cmd.Flags().StringVar(&providerURL, "provider-url", "", "Environment provider HTTP endpoint")
	cmd.Flags().IntVar(&tier, "tier", 0, "Claimed complexity tier (1, 2, or 3)")
	cmd.Flags().StringVar(&outputPath, "output", "", "Report output file path (default: stdout)")
	cmd.Flags().StringVar(&format, "format", "yaml", "Report format (yaml or json)")
	cmd.Flags().IntVar(&parallel, "parallel", 1, "Max concurrent scenarios (parallelism deferred to future version)")
	cmd.Flags().StringVar(&timeout, "timeout", "5m", "Per-scenario timeout")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate inputs without executing")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Verbose execution output")
	cmd.Flags().BoolVar(&safetyOnly, "safety-only", false, "Run only safety scenarios, skip capability")
	cmd.Flags().StringSliceVar(&categories, "category", nil, "Filter scenarios by category (repeatable)")
	cmd.Flags().StringSliceVar(&subcategories, "subcategory", nil, "Filter scenarios by subcategory (repeatable)")

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
