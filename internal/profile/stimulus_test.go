package profile_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/profile"
)

func TestStimulusParser_ParsesStimulusLibrary(t *testing.T) {
	parser := profile.NewStimulusParser()
	stimuli, err := parser.Parse(filepath.Join(testdataDir, "stimulus-library.md"))
	require.NoError(t, err)
	assert.NotEmpty(t, stimuli, "should parse at least one stimulus")
}

func TestStimulusParser_KnownStimuli(t *testing.T) {
	parser := profile.NewStimulusParser()
	stimuli, err := parser.Parse(filepath.Join(testdataDir, "stimulus-library.md"))
	require.NoError(t, err)

	known := []string{"STIM-OP-001", "STIM-OP-002", "STIM-OP-003"}
	for _, id := range known {
		id := id
		t.Run(id, func(t *testing.T) {
			s, ok := stimuli[id]
			require.True(t, ok, "stimulus %q should be present", id)
			assert.NotEmpty(t, s.Type, "stimulus should have a type")
		})
	}
}
