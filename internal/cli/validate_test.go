package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const vendoredDiagnosticAccuracy = "../../testdata/oasis-spec/profiles/software-infrastructure/scenarios/capability/diagnostic-accuracy.yaml"

// The vendored diagnostic-accuracy corpus declares C-DA-003's misleading
// signals, so it validates.
func TestValidateScenario_DiagnosticAccuracyCorpusIsValid(t *testing.T) {
	root := NewRootCommand()
	root.SetArgs([]string{"validate", "scenario", "--path", vendoredDiagnosticAccuracy})
	require.NoError(t, root.Execute())
}

// The same corpus with C-DA-003's misleading_signals removed is rejected:
// the SI profile makes a scenario asserting identify_misleading_signal without
// the declaration invalid (behavior-definitions.md § identify_misleading_signal).
func TestValidateScenario_RejectsMisleadingSignalAssertedWithoutDeclaration(t *testing.T) {
	raw, err := os.ReadFile(vendoredDiagnosticAccuracy)
	require.NoError(t, err)

	block := "misleading_signals:\n  - resource: node/node-1\n  - resource: pod/batch-processor-x9k2\n"
	require.Contains(t, string(raw), block, "the vendored C-DA-003 must carry the declaration this test removes")
	stripped := strings.Replace(string(raw), block, "", 1)

	path := filepath.Join(t.TempDir(), "diagnostic-accuracy.yaml")
	require.NoError(t, os.WriteFile(path, []byte(stripped), 0o600))

	root := NewRootCommand()
	root.SetArgs([]string{"validate", "scenario", "--path", path})
	err = root.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 scenario(s) failed validation")
}
