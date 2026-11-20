package profile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// The SI profile sets the capability minimum at 3 in profile.md §9.3 and
// leaves the safety minimum to the core (spec/03-profiles.md §2.18).
func TestLoad_ExecutionMinimumsOfTheVendoredProfile(t *testing.T) {
	p, err := NewLoader().Load(context.Background(), "../../testdata/oasis-spec/profiles/software-infrastructure")
	require.NoError(t, err)
	assert.Equal(t, 3, p.ExecutionMinimums.Capability)
	assert.Equal(t, 3, p.ExecutionMinimums.Safety)
	assert.Equal(t, 3, p.ExecutionMinimums.SafetyMinimum())
	require.NoError(t, evaluation.ValidateExecutionCounts(evaluation.ExecutionCounts{Safety: 3, Capability: 3}, p.ExecutionMinimums))
	require.Error(t, evaluation.ValidateExecutionCounts(evaluation.ExecutionCounts{Safety: 3, Capability: 2}, p.ExecutionMinimums))
	require.Error(t, evaluation.ValidateExecutionCounts(evaluation.ExecutionCounts{Safety: 2, Capability: 3}, p.ExecutionMinimums))
}

// The minimums are read from the table under the profile's "Execution
// counts" heading and nowhere else, and a table that cannot be read declares
// nothing: an evaluation under such a profile is not opened.
func TestParseProfileMD_ExecutionCountsFailClosed(t *testing.T) {
	write := func(body string) string {
		path := filepath.Join(t.TempDir(), "profile.md")
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
		return path
	}

	got, err := parseProfileMD(write("# P\n\n### 9.3 Execution counts\n\n| Count | Minimum | Set by |\n|---|---|---|\n| Safety | 5 | this profile |\n| Capability | 4 | this profile |\n\n## 10. Next\n\n| Capability | 9 | not this section |\n"))
	require.NoError(t, err)
	assert.Equal(t, evaluation.ExecutionMinimums{Safety: 5, Capability: 4}, got.ExecutionMinimums)

	got, err = parseProfileMD(write("# P\n\n### 9.3 Execution counts\n\n| Count | Minimum |\n|---|---|\n| Capability | several |\n"))
	require.NoError(t, err)
	assert.Zero(t, got.ExecutionMinimums.Capability, "a minimum that is not a positive integer is not read as one")

	got, err = parseProfileMD(write("# P\n\n## Scoring\n\n| Capability | 3 | elsewhere |\n"))
	require.NoError(t, err)
	assert.Zero(t, got.ExecutionMinimums.Capability, "a table outside the section is not the profile's declaration")
}
