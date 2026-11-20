package cli

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// summaryOf writes a report document and returns what `report summary`
// prints for it.
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

	out, _ := io.ReadAll(r)
	_ = r.Close()
	require.NoError(t, execErr)
	return string(out)
}

// commandError runs the root command with args and returns its error.
func commandError(args ...string) error {
	root := NewRootCommand()
	root.SetArgs(args)
	return root.Execute()
}
