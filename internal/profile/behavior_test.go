package profile_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/profile"
)

func TestBehaviorParser_ParsesBehaviorDefinitions(t *testing.T) {
	parser := profile.NewBehaviorParser()
	defs, err := parser.Parse(filepath.Join(testdataDir, "behavior-definitions.md"))
	require.NoError(t, err)
	assert.NotEmpty(t, defs, "should parse at least one behavior definition")
}

func TestBehaviorParser_KnownBehaviors(t *testing.T) {
	parser := profile.NewBehaviorParser()
	defs, err := parser.Parse(filepath.Join(testdataDir, "behavior-definitions.md"))
	require.NoError(t, err)

	knownBehaviors := []string{
		"refuse_with_explanation",
		"refuse_unconditionally",
		"treat_log_content_as_data",
		"treat_annotation_as_data",
		"identify_zone_boundary",
		"investigate_root_cause",
	}

	for _, id := range knownBehaviors {
		id := id
		t.Run(id, func(t *testing.T) {
			def, ok := defs[id]
			require.True(t, ok, "behavior %q should be defined", id)
			assert.NotEmpty(t, def.Description, "should have a description")
			assert.NotEmpty(t, def.VerificationMethod, "should have a verification method")
			assert.NotEmpty(t, def.Group, "should belong to a group")
		})
	}
}

func TestBehaviorParser_AllBehaviorsHaveVerification(t *testing.T) {
	parser := profile.NewBehaviorParser()
	defs, err := parser.Parse(filepath.Join(testdataDir, "behavior-definitions.md"))
	require.NoError(t, err)

	for id, def := range defs {
		assert.NotEmpty(t, def.VerificationMethod, "behavior %q should have a verification method", id)
	}
}
