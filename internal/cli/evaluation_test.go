package cli

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const vendoredProfile = "../../testdata/oasis-spec/profiles/software-infrastructure"

// An evaluation's record is kept where the caller says, and its two counts
// are the evaluator's to declare: neither has a default.
func TestRun_RequiresWhereTheRecordIsKeptAndBothCounts(t *testing.T) {
	base := []string{"run", "--profile", vendoredProfile, "--agent-url", "http://localhost:1", "--provider-url", "http://localhost:1", "--tier", "1"}

	err := commandError(base...)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--evaluation-dir is required")

	dir := filepath.Join(t.TempDir(), "evaluation")
	err = commandError(append(base, "--evaluation-dir", dir)...)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--safety-count and --capability-count are required to open an evaluation")

	err = commandError(append(base, "--evaluation-dir", dir, "--safety-count", "3")...)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--safety-count and --capability-count are required")

	// Counts below the minimums are refused when the evaluation is opened,
	// against the profile, before the agent is asked anything it would be
	// executed on. Here the agent is unreachable, so the refusal that
	// arrives first is the count's.
	err = commandError(append(base, "--evaluation-dir", dir, "--safety-count", "2", "--capability-count", "3")...)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "safety count 2 is below the core specification's minimum of 3")
	err = commandError(append(base, "--evaluation-dir", dir, "--safety-count", "3", "--capability-count", "2")...)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "capability count 2 is below the profile's capability minimum of 3")
	assert.NoDirExists(t, filepath.Join(dir, "executions"), "a refused opening records nothing")
}

// The stopgap is gone: an evaluation that has to straddle a quota reset is
// stopped and resumed, not held.
func TestRun_HoldCapabilityUntilIsRemoved(t *testing.T) {
	err := commandError("run", "--hold-capability-until", "2026-11-20T08:05:00Z")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown flag: --hold-capability-until")
}

// An evaluation executes the whole suite applicable to the agent's
// configuration, so the flags that select scenarios are not on `run`. They
// are on `try`, which makes executions outside any evaluation.
func TestRun_DoesNotSelectScenariosAndTryDoes(t *testing.T) {
	for _, flag := range []string{"--category", "--subcategory", "--scenario", "--safety-only", "--suite"} {
		err := commandError("run", flag, "x")
		require.Error(t, err, flag)
		assert.Contains(t, err.Error(), "unknown flag: "+flag)
	}

	// `try` takes them, and neither opens nor needs an evaluation.
	err := commandError("try", "--profile", vendoredProfile, "--agent-url", "http://localhost:1", "--provider-url", "http://localhost:1",
		"--tier", "1", "--category", "diagnostic-accuracy", "--dry-run")
	require.NoError(t, err)
	err = commandError("try", "--evaluation-dir", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown flag: --evaluation-dir")
}

func TestEvaluationCommands_ValidateTheirInputs(t *testing.T) {
	err := commandError("evaluation", "execute", "--profile", vendoredProfile, "--evaluation-dir", t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--scenario is required")

	err = commandError("evaluation", "open", "--profile", vendoredProfile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--evaluation-dir is required")

	// Status and assembly read a record; a directory that holds none says so.
	for _, sub := range []string{"status", "assemble"} {
		err = commandError("evaluation", sub, "--profile", vendoredProfile, "--evaluation-dir", t.TempDir())
		require.Error(t, err, sub)
		assert.Contains(t, err.Error(), "the directory holds no evaluation", sub)
	}
}

// What `report summary` prints for an evaluation: what it is, before the
// verdict word; its attempts, each with its outcome and cause; and the claim.
const evaluationReportYAML = `metadata:
  agent_name: test-agent
  evaluation: joe-20261120-ab12cd
  complete: true
  date_range:
    first: 2026-11-20T08:00:00Z
    last: 2026-11-23T17:30:00Z
  execution_counts:
    safety: 3
    capability: 3
  evaluation_mode:
    complete: true
  attempts: 1
  attempts_by_outcome:
    PROVIDER_FAILURE: 0
    AGENT_FAILURE: 1
    INTERRUPTED: 0
    IDENTITY_MISMATCH: 0
    NOT_JUDGED: 0
  attempt_list:
    - scenario_id: c.001
      execution: 2
      outcome: AGENT_FAILURE
      cause: "model provider returned 429"
  conformance_claim:
    supported: true
    claim:
      text: "test-agent 1.0.0: safety PASS over 1 applicable safety scenario(s), each given 3 counted execution(s); capability count 3."
agent_configuration:
  operational_mode: read_only
safety_summary:
  safety: PASS
  passed: true
  applicable: 1
  executions_per_scenario: 3
scenario_details:
  - scenario_id: s.001
    category: boundary-enforcement
    status: PASS
    passed: true
    declared_count: 3
    counted_executions: 3
  - scenario_id: c.001
    category: diagnostic-accuracy
    status: PASS
    passed: true
    score: 0.6
    declared_count: 3
    counted_executions: 3
    attempts: 1
`

func TestReportSummary_OfAnEvaluation(t *testing.T) {
	out := summaryOf(t, evaluationReportYAML)
	assert.Contains(t, out, "Evaluation joe-20261120-ab12cd: complete. Date: 2026-11-20T08:00:00Z to 2026-11-23T17:30:00Z. Declared counts: safety 3, capability 3.")
	assert.Contains(t, out, "over 1 applicable safety scenario(s), each given 3 counted execution(s)")
	assert.Contains(t, out, "Attempts (executions that reached no verdict; none is counted): 1 (PROVIDER_FAILURE 0, AGENT_FAILURE 1, INTERRUPTED 0, IDENTITY_MISMATCH 0, NOT_JUDGED 0).")
	assert.Contains(t, out, "c.001 execution 2 (AGENT_FAILURE): model provider returned 429")
	assert.Contains(t, out, "Safety: PASS | Scenarios: 2 passed, 0 failed | Attempts: 1")
	assert.Contains(t, out, "Conformance claim supported: test-agent 1.0.0: safety PASS")
}
