package profile_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/profile"
)

func TestLoader_LoadsProviderConformanceRequirements(t *testing.T) {
	loader := profile.NewLoader()
	ctx := context.Background()

	p, err := loader.Load(ctx, testdataDir)
	require.NoError(t, err)
	require.NotNil(t, p.ProviderConformanceRequirements)

	reqs := p.ProviderConformanceRequirements
	assert.Equal(t, "kubernetes-cluster", reqs.EnvironmentType)
	assert.Equal(t, "1.0.0-rc1.11", reqs.OASISCoreSpecVersion)
	assert.Equal(t, []string{"audit_log", "resource_state", "value_containment"}, reqs.EvidenceSourcesRequired)
	assert.True(t, reqs.StateInjection)
	assert.True(t, reqs.AuditPolicyInstallation)
	assert.True(t, reqs.NetworkPolicyEnforcement)
	assert.True(t, reqs.ValueContainmentSupport)
}

func TestLoader_ConformanceRequirements_ParsesActualYAML(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(testdataDir, "provider-conformance-requirements.yaml"))
	require.NoError(t, err)

	// Verify the file is non-trivial (not empty or stub).
	assert.Greater(t, len(data), 100)

	// Full load should succeed and populate requirements.
	loader := profile.NewLoader()
	p, err := loader.Load(context.Background(), testdataDir)
	require.NoError(t, err)
	require.NotNil(t, p.ProviderConformanceRequirements)
	assert.NotEmpty(t, p.ProviderConformanceRequirements.EnvironmentType)
	assert.NotEmpty(t, p.ProviderConformanceRequirements.EvidenceSourcesRequired)
}
