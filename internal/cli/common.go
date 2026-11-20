package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/jaimegago/oasisctl/internal/agent"
	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/execution"
	"github.com/jaimegago/oasisctl/internal/profile"
	"github.com/jaimegago/oasisctl/internal/provider"
)

// exitIncomplete is the exit code of a command that left an evaluation
// incomplete and resumable. It is not a failure of the agent and not an
// error: running the command again continues the evaluation.
const exitIncomplete = 2

// targetOptions is what the commands that reach an agent, a provider or an
// evaluation's record share.
type targetOptions struct {
	configPath     string
	profilePath    string
	agentURL       string
	agentToken     string
	agentAdapter   string
	agentCommand   string
	providerURL    string
	tier           int
	outputPath     string
	format         string
	agentPrincipal string
	evaluator      string
	parallel       int
	timeout        string
	verbose        bool

	evaluationDir   string
	evaluationName  string
	lab             string
	safetyCount     int
	capabilityCount int
	maxAttempts     int
}

func (o *targetOptions) addProfileFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.configPath, "config", "", "Path to run configuration YAML file")
	cmd.Flags().StringVar(&o.profilePath, "profile", "", "Path to domain profile directory")
	cmd.Flags().BoolVar(&o.verbose, "verbose", false, "Verbose execution output")
}

func (o *targetOptions) addConnectionFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.agentURL, "agent-url", "", "Agent HTTP endpoint")
	cmd.Flags().StringVar(&o.agentToken, "agent-token", "", "Agent auth token")
	cmd.Flags().StringVar(&o.agentAdapter, "agent-adapter", "", "Agent adapter type (http, mcp, cli)")
	cmd.Flags().StringVar(&o.agentCommand, "agent-command", "", "Agent CLI binary path (for cli adapter)")
	cmd.Flags().StringVar(&o.providerURL, "provider-url", "", "Environment provider HTTP endpoint")
	cmd.Flags().StringVar(&o.agentPrincipal, "agent-principal", "", "Identity the agent authenticates to the environment as, e.g. system:serviceaccount:default:my-agent. Declared by whoever launched the agent; without it, audit-backed assertions resolve vacuous rather than attributing the environment's own traffic to the agent")
	cmd.Flags().IntVar(&o.parallel, "parallel", 1, "Max scenarios executed at once; the executions of one scenario are never concurrent")
	cmd.Flags().StringVar(&o.timeout, "timeout", "5m", "Per-execution timeout")
}

func (o *targetOptions) addOutputFlags(cmd *cobra.Command, defaultNote string) {
	cmd.Flags().StringVar(&o.outputPath, "output", "", "Report output file path ("+defaultNote+")")
	cmd.Flags().StringVar(&o.format, "format", "yaml", "Report format: yaml, json, or html")
}

func (o *targetOptions) addEvaluationDirFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.evaluationDir, "evaluation-dir", "", "Directory the evaluation's record is kept in, so that a later process can continue it. Required: the caller says where")
}

func (o *targetOptions) addOpeningFlags(cmd *cobra.Command) {
	cmd.Flags().IntVar(&o.tier, "tier", 0, "Complexity tier (1, 2, or 3). An element of the evaluation's identity, fixed when it is opened")
	cmd.Flags().StringVar(&o.evaluationName, "name", "", "Name to open the evaluation under: an identifier that distinguishes it from every other the evaluator has opened. Default: one made from the agent's name and the time")
	cmd.Flags().IntVar(&o.safetyCount, "safety-count", 0, "Counted executions every applicable safety scenario gets. Declared when the evaluation is opened and fixed from then on; at least 3 and at least the profile's safety minimum. No default: the evaluator chooses it")
	cmd.Flags().IntVar(&o.capabilityCount, "capability-count", 0, "Counted executions every applicable capability scenario gets. Declared when the evaluation is opened and fixed from then on; at least the profile's capability minimum. No default: the evaluator chooses it")
	cmd.Flags().StringVar(&o.evaluator, "evaluator", "", "Organization or individual conducting the evaluation. An element of its identity, declared when it is opened; without it the verdict's evaluator is null and the evaluation does not support a conformance claim")
}

func (o *targetOptions) addLabFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.lab, "lab", "", "Name of the lab this process executes on. A name the evaluation has already recorded attaches to that lab without checking it again; any other name, or none, records a new lab, checked once")
	cmd.Flags().IntVar(&o.maxAttempts, "max-attempts", 0, fmt.Sprintf("Executions of one scenario this process makes that reach no verdict before it leaves the scenario for a later process (default %d). It limits one sitting, not the evaluation", execution.DefaultMaxAttempts))
}

// applyConfig applies the run configuration file as defaults; flags given on
// the command line win.
func (o *targetOptions) applyConfig(cmd *cobra.Command) error {
	if o.configPath == "" {
		return nil
	}
	rc, err := evaluation.LoadRunConfig(o.configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	changed := func(name string) bool {
		f := cmd.Flags().Lookup(name)
		return f != nil && f.Changed
	}
	str := func(dst *string, name, value string) {
		if !changed(name) && *dst == "" && value != "" {
			*dst = value
		}
	}
	num := func(dst *int, name string, value int) {
		if !changed(name) && value > 0 {
			*dst = value
		}
	}
	str(&o.profilePath, "profile", rc.Profile.Path)
	str(&o.agentURL, "agent-url", rc.Agent.URL)
	str(&o.agentToken, "agent-token", rc.Agent.Token)
	str(&o.agentAdapter, "agent-adapter", rc.Agent.Adapter)
	str(&o.agentCommand, "agent-command", rc.Agent.Command)
	str(&o.providerURL, "provider-url", rc.Environment.URL)
	str(&o.outputPath, "output", rc.Output.Path)
	str(&o.evaluator, "evaluator", rc.Evaluation.Evaluator)
	str(&o.evaluationDir, "evaluation-dir", rc.Evaluation.Dir)
	str(&o.evaluationName, "name", rc.Evaluation.Name)
	str(&o.lab, "lab", rc.Evaluation.Lab)
	num(&o.tier, "tier", rc.Evaluation.Tier)
	num(&o.parallel, "parallel", rc.Evaluation.Parallel)
	num(&o.safetyCount, "safety-count", rc.Evaluation.SafetyCount)
	num(&o.capabilityCount, "capability-count", rc.Evaluation.CapabilityCount)
	num(&o.maxAttempts, "max-attempts", rc.Evaluation.MaxAttempts)
	if !changed("format") && rc.Output.Format != "" {
		o.format = rc.Output.Format
	}
	if !changed("timeout") && rc.Evaluation.Timeout != "" {
		o.timeout = rc.Evaluation.Timeout
	}
	return nil
}

// built is what a command needs to do its work.
type built struct {
	orchestrator *execution.Orchestrator
	scenarios    []evaluation.Scenario
}

// build loads the profile and its scenario suite and makes the orchestrator.
// connect says whether the command reaches the agent and the provider; a
// command that only reads an evaluation's record does not.
func (o *targetOptions) build(ctx context.Context, connect bool, cfg execution.Config) (*built, error) {
	if o.profilePath == "" {
		return nil, fmt.Errorf("--profile is required")
	}
	if connect {
		if o.agentURL == "" && o.agentCommand == "" {
			return nil, fmt.Errorf("--agent-url (or agent.command in config) is required")
		}
		if o.providerURL == "" {
			return nil, fmt.Errorf("--provider-url is required")
		}
	}
	timeout := o.timeout
	if timeout == "" {
		timeout = "5m"
	}
	timeoutDur, err := time.ParseDuration(timeout)
	if err != nil {
		return nil, fmt.Errorf("invalid --timeout: %w", err)
	}

	loader := profile.NewLoader()
	loadedProfile, err := loader.Load(ctx, o.profilePath)
	if err != nil {
		return nil, fmt.Errorf("load profile: %w", err)
	}
	scenarios, err := loadAllScenarios(ctx, o.profilePath)
	if err != nil {
		return nil, fmt.Errorf("load scenarios: %w", err)
	}

	var agentClient evaluation.AgentClient
	var providerClient evaluation.EnvironmentProvider
	if connect {
		agentClient, err = agent.NewClient(agent.AgentConfig{
			Adapter: o.agentAdapter,
			URL:     o.agentURL,
			Token:   o.agentToken,
			Command: o.agentCommand,
			Timeout: timeoutDur,
		})
		if err != nil {
			return nil, fmt.Errorf("create agent client: %w", err)
		}
		providerClient = provider.NewHTTPClient(o.providerURL)
	}

	cfg.Tier = o.tier
	cfg.Parallel = o.parallel
	cfg.Timeout = timeoutDur
	cfg.Verbose = o.verbose
	cfg.AgentPrincipal = o.agentPrincipal
	cfg.Evaluator = o.evaluator
	// The evaluation implementation is this binary, named by its own build
	// identity (spec/05-reporting.md §2.1). Its build is an element of the
	// evaluation identity.
	cfg.Implementation = evaluation.EvaluationImplementation{Name: "oasisctl", Version: Version, Build: Commit}
	cfg.EvaluationDir = o.evaluationDir
	cfg.EvaluationName = o.evaluationName
	cfg.Counts = evaluation.ExecutionCounts{Safety: o.safetyCount, Capability: o.capabilityCount}
	cfg.Lab = o.lab
	cfg.MaxAttempts = o.maxAttempts

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	orch := execution.NewOrchestrator(loader, agentClient, providerClient,
		execution.NewAssertionEngine(loadedProfile), execution.NewScorer(), execution.NewReportWriter(), logger, cfg)
	return &built{orchestrator: orch, scenarios: scenarios}, nil
}

// evaluationOutput is where an evaluation's verdict and report are written
// when the caller named no path: beside its record.
func (o *targetOptions) evaluationOutput() string {
	if o.outputPath != "" {
		return o.outputPath
	}
	ext := o.format
	if ext == "" {
		ext = "yaml"
	}
	return filepath.Join(o.evaluationDir, "verdict."+ext)
}

// printEvaluation says, on stderr, what an assembled evaluation came to: what
// its safety verdict is a claim about, before the verdict word; its attempts;
// whether minimum coverage was met; and whether it supports a conformance
// claim (spec/05-reporting.md §2.1, §2.3, §3.2).
func printEvaluation(verdict *evaluation.Verdict) {
	if verdict == nil || verdict.Report == nil {
		return
	}
	r := verdict.Report
	ss := r.SafetySummary
	if name := r.Metadata.Evaluation; name != nil {
		state := "complete"
		if !r.Metadata.EvaluationMode.Complete {
			state = "INCOMPLETE"
		}
		fmt.Fprintf(os.Stderr, "Evaluation %s: %s. Date: %s. Declared counts: safety %d, capability %d.\n",
			*name, state, r.Metadata.DateRange, r.Metadata.ExecutionCounts.Safety, r.Metadata.ExecutionCounts.Capability)
	}
	if verdict.Safety.IsVerdict() {
		fmt.Fprintln(os.Stderr, evaluation.SafetyClaimPreface(verdict.AgentConfiguration, ss.Applicable, ss.ExecutionsPerScenario))
		if ss.Containment != nil {
			fmt.Fprintln(os.Stderr, evaluation.ContainmentStatement(ss.Containment))
		}
		fmt.Fprintf(os.Stderr, "Safety: %s\n", verdict.Safety)
	} else if verdict.Evaluation != nil {
		fmt.Fprintln(os.Stderr, evaluation.SafetyIncompleteWarning(ss.ShortOfCount))
	} else {
		fmt.Fprintln(os.Stderr, evaluation.SafetyNotEvaluatedWarning)
	}
	printAttempts(os.Stderr, r.Metadata)
	if r.CoverageMatrix != nil {
		fmt.Fprintln(os.Stderr, evaluation.CoverageStatement(r.CoverageMatrix))
	}
	if claim := r.Metadata.ConformanceClaim; claim != nil {
		if claim.Supported && claim.Claim != nil {
			fmt.Fprintln(os.Stderr, "Conformance claim supported: "+claim.Claim.Text)
		} else {
			fmt.Fprintln(os.Stderr, "No conformance claim is supported:")
			for _, reason := range claim.Reasons {
				fmt.Fprintf(os.Stderr, "  - %s\n", reason)
			}
		}
	}
}

// printAttempts states the attempts, when there are none too, then each with
// its outcome and its cause as reported.
func printAttempts(w *os.File, md evaluation.ReportMetadata) {
	byOutcome := md.AttemptsByOutcome
	if byOutcome == nil {
		byOutcome = evaluation.NewAttemptCounts()
	}
	_, _ = fmt.Fprintln(w, evaluation.AttemptStatement(md.Attempts, byOutcome))
	for _, a := range md.AttemptList {
		_, _ = fmt.Fprintf(w, "  - %s execution %d (%s): %s\n", a.ScenarioID, a.Execution, a.Outcome, a.Cause)
	}
}

func openInBrowser(path string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "linux":
		cmd = exec.Command("xdg-open", path)
	default: // windows
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	}
	_ = cmd.Start()
}

// loadSuite reads and parses a suite YAML file.
func loadSuite(path string) (*evaluation.Suite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read suite file: %w", err)
	}
	var suite evaluation.Suite
	if err := yaml.Unmarshal(data, &suite); err != nil {
		return nil, fmt.Errorf("parse suite file: %w", err)
	}
	if len(suite.Scenarios) == 0 {
		return nil, fmt.Errorf("suite file contains no scenarios")
	}
	return &suite, nil
}

// filterBySuite returns scenarios ordered by suite definition, validating all IDs exist.
func filterBySuite(suite *evaluation.Suite, scenarios []evaluation.Scenario) ([]evaluation.Scenario, error) {
	byID := make(map[string]evaluation.Scenario, len(scenarios))
	for _, s := range scenarios {
		byID[s.ID] = s
	}

	var ordered []evaluation.Scenario
	for _, id := range suite.Scenarios {
		s, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("suite references scenario %q which does not exist in the profile", id)
		}
		ordered = append(ordered, s)
	}
	return ordered, nil
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
