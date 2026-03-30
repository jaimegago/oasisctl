package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleReportYAML = `metadata:
  agent_name: test-agent
  agent_version: "1.0"
  evaluator: oasisctl 0.1.0
  date: 2026-01-01T00:00:00Z
  profile_name: test-profile
  profile_version: "0.1"
  provider_info: local
  evaluation_mode:
    safety_only: false
    complete: true
environment:
  tier_claimed: 2
safety_summary:
  passed: true
  category_results:
    sec: true
  human_review_needed: false
scenario_details:
  - scenario_id: safety.sec.001
    category: sec
    passed: true
    needs_review: false
    tolerance_flag: false
    score: 1.0
`

func TestReportHTML_ReadsYAMLAndProducesHTML(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "verdict.yaml")
	outputPath := filepath.Join(dir, "report.html")

	err := os.WriteFile(inputPath, []byte(sampleReportYAML), 0644)
	require.NoError(t, err)

	root := NewRootCommand()
	root.SetArgs([]string{"report", "html", "--input", inputPath, "--output", outputPath})
	err = root.Execute()
	require.NoError(t, err)

	data, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "<!DOCTYPE html>")
	assert.Contains(t, string(data), "test-agent")
}

func TestReportSummary_PrintsOneLine(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "verdict.yaml")

	err := os.WriteFile(inputPath, []byte(sampleReportYAML), 0644)
	require.NoError(t, err)

	// Capture stdout.
	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	root := NewRootCommand()
	root.SetArgs([]string{"report", "summary", "--input", inputPath})
	execErr := root.Execute()

	_ = w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	_ = r.Close()
	output := string(buf[:n])

	require.NoError(t, execErr)
	assert.Contains(t, output, "Safety: PASS")
	assert.Contains(t, output, "1 passed")
	assert.Contains(t, output, "0 failed")
}

func TestReportHTML_MissingInput(t *testing.T) {
	root := NewRootCommand()
	root.SetArgs([]string{"report", "html", "--output", "/tmp/out.html"})
	err := root.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--input is required")
}

func TestReportHTML_MissingOutput(t *testing.T) {
	root := NewRootCommand()
	root.SetArgs([]string{"report", "html", "--input", "/tmp/in.yaml"})
	err := root.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--output is required")
}

func TestRunCommand_HTMLFormatRequiresOutput(t *testing.T) {
	root := NewRootCommand()
	root.SetArgs([]string{"run",
		"--profile", "/tmp/fake",
		"--agent-url", "http://localhost",
		"--provider-url", "http://localhost",
		"--tier", "1",
		"--format", "html",
	})
	err := root.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "html format requires --output")
}
