package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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

// --- Report convert tests ---

func TestReportConvert_YAMLToJSON(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "verdict.yaml")
	outputPath := filepath.Join(dir, "verdict.json")

	err := os.WriteFile(inputPath, []byte(sampleReportYAML), 0644)
	require.NoError(t, err)

	root := NewRootCommand()
	root.SetArgs([]string{"report", "convert", "--input", inputPath, "--format", "json", "--output", outputPath})
	err = root.Execute()
	require.NoError(t, err)

	data, err := os.ReadFile(outputPath)
	require.NoError(t, err)

	// Verify valid JSON.
	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err, "output should be valid JSON")
	assert.Contains(t, string(data), "test-agent")
}

func TestReportConvert_JSONToYAML(t *testing.T) {
	dir := t.TempDir()

	// First create a JSON verdict from the sample YAML.
	jsonInput := filepath.Join(dir, "verdict.json")
	yamlOutput := filepath.Join(dir, "verdict.yaml")

	// Write a JSON verdict file.
	jsonContent := `{
  "metadata": {
    "agent_name": "test-agent",
    "agent_version": "1.0",
    "evaluator": "oasisctl 0.1.0",
    "profile_name": "test-profile"
  },
  "safety_summary": {
    "passed": true
  }
}`
	err := os.WriteFile(jsonInput, []byte(jsonContent), 0644)
	require.NoError(t, err)

	root := NewRootCommand()
	root.SetArgs([]string{"report", "convert", "--input", jsonInput, "--format", "yaml", "--output", yamlOutput})
	err = root.Execute()
	require.NoError(t, err)

	data, err := os.ReadFile(yamlOutput)
	require.NoError(t, err)

	// Verify valid YAML.
	var parsed map[string]interface{}
	err = yaml.Unmarshal(data, &parsed)
	require.NoError(t, err, "output should be valid YAML")
	assert.Contains(t, string(data), "test-agent")
}

func TestReportConvert_MissingInput(t *testing.T) {
	root := NewRootCommand()
	root.SetArgs([]string{"report", "convert", "--format", "json"})
	err := root.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--input is required")
}

func TestReportConvert_InvalidFormat(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "verdict.yaml")
	err := os.WriteFile(inputPath, []byte(sampleReportYAML), 0644)
	require.NoError(t, err)

	root := NewRootCommand()
	root.SetArgs([]string{"report", "convert", "--input", inputPath, "--format", "xml"})
	err = root.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--format must be yaml or json")
}

func TestReportConvert_ToStdout(t *testing.T) {
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
	root.SetArgs([]string{"report", "convert", "--input", inputPath, "--format", "json"})
	execErr := root.Execute()

	_ = w.Close()
	os.Stdout = old

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	_ = r.Close()
	output := string(buf[:n])

	require.NoError(t, execErr)
	// Should be valid JSON on stdout.
	var parsed map[string]interface{}
	err = json.Unmarshal([]byte(output), &parsed)
	require.NoError(t, err, "stdout should contain valid JSON")
	assert.Contains(t, output, "test-agent")
}

// --- Suite file tests ---

func TestLoadSuite_NonexistentFile(t *testing.T) {
	_, err := loadSuite("/tmp/nonexistent-suite-file.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read suite file")
}

func TestLoadSuite_EmptyScenarios(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.yaml")
	err := os.WriteFile(suitePath, []byte("id: test-suite\nname: Test\nscenarios: []\n"), 0644)
	require.NoError(t, err)

	_, err = loadSuite(suitePath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "suite file contains no scenarios")
}

func TestLoadSuite_ValidFile(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.yaml")
	content := `id: test-suite
name: Test Suite
version: "1.0"
domain_profile: software-infrastructure
scenarios:
  - s.001
  - c.001
`
	err := os.WriteFile(suitePath, []byte(content), 0644)
	require.NoError(t, err)

	suite, err := loadSuite(suitePath)
	require.NoError(t, err)
	assert.Equal(t, "test-suite", suite.ID)
	assert.Equal(t, []string{"s.001", "c.001"}, suite.Scenarios)
}

func TestRunCommand_SuiteFlag_CLIError(t *testing.T) {
	// Verify that --suite with a nonexistent file produces an error through the CLI.
	// The profile loads first, so we pass a nonexistent profile to get an early error
	// that proves the flag is wired up and the command accepts it.
	root := NewRootCommand()
	root.SetArgs([]string{"run",
		"--profile", "/tmp/fake-profile-dir",
		"--agent-url", "http://localhost",
		"--provider-url", "http://localhost",
		"--tier", "1",
		"--suite", "/tmp/nonexistent-suite.yaml",
	})
	err := root.Execute()
	// Should error (profile load fails before suite, but the flag is accepted).
	assert.Error(t, err)
}
