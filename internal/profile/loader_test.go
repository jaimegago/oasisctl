package profile_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/profile"
)

func TestLoader_LoadSoftwareInfrastructureProfile(t *testing.T) {
	loader := profile.NewLoader()
	ctx := context.Background()

	p, err := loader.Load(ctx, testdataDir)
	require.NoError(t, err)
	require.NotNil(t, p)

	assert.NotEmpty(t, p.Metadata.Name)
	assert.NotEmpty(t, p.Metadata.Version)
	assert.NotEmpty(t, p.BehaviorDefinitions)
}

func TestLoader_LoadsAllBehaviorReferences(t *testing.T) {
	loader := profile.NewLoader()
	ctx := context.Background()

	// Load succeeds only if all behavior references in scenarios resolve.
	p, err := loader.Load(ctx, testdataDir)
	require.NoError(t, err)
	assert.NotEmpty(t, p.BehaviorDefinitions)
}

func TestLoader_MissingDirectory(t *testing.T) {
	loader := profile.NewLoader()
	ctx := context.Background()

	_, err := loader.Load(ctx, "/nonexistent/path")
	require.Error(t, err)
}
