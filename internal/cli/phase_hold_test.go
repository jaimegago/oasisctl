package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const heldReportYAML = `metadata:
  agent_name: test-agent
  phase_hold:
    before: capability
    requested_until: 2026-11-20T08:05:00Z
    began: 2026-11-20T06:10:12Z
    ended: 2026-11-20T08:05:03Z
safety_summary:
  safety: PASS
  passed: true
scenario_details:
  - scenario_id: s.001
    category: boundary-enforcement
    status: PASS
    passed: true
`

func summaryOf(t *testing.T, reportYAML string) string {
	t.Helper()
	inputPath := filepath.Join(t.TempDir(), "verdict.yaml")
	require.NoError(t, os.WriteFile(inputPath, []byte(reportYAML), 0644))

	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	root := NewRootCommand()
	root.SetArgs([]string{"report", "summary", "--input", inputPath})
	execErr := root.Execute()

	_ = w.Close()
	os.Stdout = old

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	_ = r.Close()
	require.NoError(t, execErr)
	return string(buf[:n])
}

// TestReportSummary_StatesAPhaseHold: the text rendering says there was a
// hold, the time asked for and when the wait began and ended. A report with no
// hold prints nothing about one.
func TestReportSummary_StatesAPhaseHold(t *testing.T) {
	held := summaryOf(t, heldReportYAML)
	assert.Contains(t, held, "The capability phase was held until 2026-11-20T08:05:00Z, as asked on the run: "+
		"the wait began at 2026-11-20T06:10:12Z and ended at 2026-11-20T08:05:03Z (1h54m51s).")

	plain := summaryOf(t, strings.Replace(heldReportYAML, `  phase_hold:
    before: capability
    requested_until: 2026-11-20T08:05:00Z
    began: 2026-11-20T06:10:12Z
    ended: 2026-11-20T08:05:03Z
`, "", 1))
	assert.NotContains(t, plain, "held")
}

// TestRun_HoldCapabilityUntilIsParsedBeforeAnythingStarts: a time that does
// not parse, or a hold asked for on a run with no capability phase, is refused
// before the profile is loaded or any endpoint is reached. The profile path
// below does not exist; reaching it would be a different error.
func TestRun_HoldCapabilityUntilIsParsedBeforeAnythingStarts(t *testing.T) {
	base := []string{"run", "--profile", "/does/not/exist", "--agent-url", "http://127.0.0.1:1", "--provider-url", "http://127.0.0.1:1", "--tier", "1"}

	for name, tt := range map[string]struct {
		args []string
		want string
	}{
		"not a time":         {[]string{"--hold-capability-until", "tomorrow"}, "invalid --hold-capability-until"},
		"no offset":          {[]string{"--hold-capability-until", "2026-11-20T08:05:00"}, "invalid --hold-capability-until"},
		"with safety-only":   {[]string{"--hold-capability-until", "2026-11-20T08:05:00Z", "--safety-only"}, "there is no capability phase to hold"},
		"a time that parses": {[]string{"--hold-capability-until", "2026-11-20T09:05:00+01:00"}, "load profile"},
	} {
		t.Run(name, func(t *testing.T) {
			root := NewRootCommand()
			root.SilenceErrors = true
			root.SilenceUsage = true
			root.SetArgs(append(append([]string{}, base...), tt.args...))
			err := root.Execute()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}
