package evaluation

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConformanceResponse_RoundTrip_SpecSection5_1 parses the worked conformant
// example from profiles/software-infrastructure/provider-conformance.md §5.1
// verbatim and asserts every field. This is the safety net: any future struct
// change that diverges from the spec wire format will fail here.
func TestConformanceResponse_RoundTrip_SpecSection5_1(t *testing.T) {
	// Literal copy of the spec §5.1 worked conformant example.
	// Do NOT build this from a Go struct — the test must fail if the struct
	// silently drops, misnames, or mistypes a field.
	const specJSON = `{
  "provider": "petri",
  "provider_version": "0.2.0",
  "oasis_core_spec_versions": ["0.4.0"],
  "profile": "oasis-profile-software-infrastructure",
  "profile_version": "0.2.0-draft",
  "supported": true,
  "requirements": {
    "environment_type": "kubernetes-cluster",
    "complexity_tier_supported": 1,
    "oasis_core_spec_version": ["0.4.0"],
    "evidence_sources_available": ["audit_log", "resource_state", "state_diff", "response_content"],
    "state_injection": true,
    "audit_policy_installation": true,
    "network_policy_enforcement": true
  },
  "unmet_requirements": []
}`

	var resp ConformanceResponse
	err := json.Unmarshal([]byte(specJSON), &resp)
	require.NoError(t, err, "spec §5.1 JSON must unmarshal into ConformanceResponse without error")

	// Top-level fields.
	assert.Equal(t, "petri", resp.Provider)
	assert.Equal(t, "0.2.0", resp.ProviderVersion)
	require.Len(t, resp.OASISCoreSpecVersions, 1)
	assert.Equal(t, "0.4.0", resp.OASISCoreSpecVersions[0])
	assert.Equal(t, "oasis-profile-software-infrastructure", resp.Profile)
	assert.Equal(t, "0.2.0-draft", resp.ProfileVersion)
	assert.True(t, resp.Supported)

	// Nested requirements — seven peer fields.
	assert.Equal(t, "kubernetes-cluster", resp.Requirements.EnvironmentType)
	assert.Equal(t, 1, resp.Requirements.ComplexityTierSupported)
	require.Len(t, resp.Requirements.OASISCoreSpecVersion, 1)
	assert.Equal(t, "0.4.0", resp.Requirements.OASISCoreSpecVersion[0])
	assert.Contains(t, resp.Requirements.EvidenceSourcesAvailable, "audit_log")
	assert.Contains(t, resp.Requirements.EvidenceSourcesAvailable, "resource_state")
	assert.Contains(t, resp.Requirements.EvidenceSourcesAvailable, "state_diff")
	assert.Contains(t, resp.Requirements.EvidenceSourcesAvailable, "response_content")
	assert.True(t, resp.Requirements.StateInjection)
	assert.True(t, resp.Requirements.AuditPolicyInstallation)
	assert.True(t, resp.Requirements.NetworkPolicyEnforcement)

	// Unmet requirements — empty for a conformant provider.
	assert.Len(t, resp.UnmetRequirements, 0)
}
