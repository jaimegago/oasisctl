package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The shape run 20261119-234640-ca82d6 had, in small: safety and capability
// passes, a capability failure, and scenarios the agent's configuration made
// NOT_APPLICABLE, whose records carry passed: true.
const notApplicableReportYAML = `metadata:
  agent_name: test-agent
safety_summary:
  safety: PASS
  passed: true
scenario_details:
  - scenario_id: s.001
    category: boundary-enforcement
    status: PASS
    passed: true
  - scenario_id: s.002
    category: boundary-enforcement
    status: PASS
    passed: true
  - scenario_id: c.001
    category: diagnostic-accuracy
    status: PASS
    passed: true
    score: 1
  - scenario_id: c.002
    category: diagnostic-accuracy
    status: FAIL
    passed: false
    score: 0.5
  - scenario_id: c.003
    category: operational-execution
    status: NOT_APPLICABLE
    passed: true
  - scenario_id: c.004
    category: operational-execution
    status: NOT_APPLICABLE
    passed: true
  - scenario_id: c.005
    category: operational-execution
    status: NOT_APPLICABLE
    passed: true
`

// TestReportSummary_NotApplicableIsNotCountedAsPassed: a NOT_APPLICABLE
// scenario was not run and "does not contribute to PASS counts"
// (spec/01-core.md §3.6.1). The summary line counted it as passed: it printed
// "36 passed, 13 failed" for 26 passes, 13 failures and 10 not-applicable
// records. It is counted on its own.
func TestReportSummary_NotApplicableIsNotCountedAsPassed(t *testing.T) {
	out := summaryOf(t, notApplicableReportYAML)
	assert.Contains(t, out, "Scenarios: 3 passed, 1 failed, 3 not applicable |")
	assert.NotContains(t, out, "6 passed")

	// A report with none says nothing about them.
	assert.Contains(t, summaryOf(t, evaluationReportYAML), "Scenarios: 2 passed, 0 failed |")
}
