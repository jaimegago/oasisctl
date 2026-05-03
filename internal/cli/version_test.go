package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVersion_Defaults(t *testing.T) {
	assert.Equal(t, "dev", Version)
	assert.Equal(t, "unknown", Commit)
}

func TestVersionCommand_Output(t *testing.T) {
	tests := []struct {
		name    string
		version string
		commit  string
		want    string
	}{
		{
			name:    "defaults",
			version: "dev",
			commit:  "unknown",
			want:    "oasisctl dev (commit unknown)\n",
		},
		{
			name:    "injected values",
			version: "1.2.3",
			commit:  "abc1234",
			want:    "oasisctl 1.2.3 (commit abc1234)\n",
		},
	}

	origVersion, origCommit := Version, Commit
	t.Cleanup(func() {
		Version = origVersion
		Commit = origCommit
	})

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			Version = tc.version
			Commit = tc.commit

			cmd := newVersionCommand()
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs(nil)

			err := cmd.Execute()
			assert.NoError(t, err)
			assert.Equal(t, tc.want, buf.String())
		})
	}
}
