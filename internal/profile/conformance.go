package profile

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// conformanceRequirementsFile mirrors the on-disk schema of
// provider-conformance-requirements.yaml as defined in
// profiles/software-infrastructure/provider-conformance.md §4.
type conformanceRequirementsFile struct {
	Profile             string                                 `yaml:"profile"`
	ProfileVersion      string                                 `yaml:"profile_version"`
	OASISCoreDependency string                                 `yaml:"oasis_core_dependency"`
	Requirements        map[string]conformanceRequirementEntry `yaml:"requirements"`
}

// conformanceRequirementEntry represents a single requirement key's declaration.
type conformanceRequirementEntry struct {
	Type        string      `yaml:"type"`
	Required    bool        `yaml:"required"`
	Expected    interface{} `yaml:"expected"`
	Description string      `yaml:"description"`
}

// parseConformanceRequirements parses provider-conformance-requirements.yaml
// into the evaluation-layer ProviderConformanceRequirements type.
func parseConformanceRequirements(data []byte) (*evaluation.ProviderConformanceRequirements, error) {
	var file conformanceRequirementsFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("unmarshal conformance requirements: %w", err)
	}

	if len(file.Requirements) == 0 {
		return nil, fmt.Errorf("conformance requirements file has no requirements")
	}

	reqs := &evaluation.ProviderConformanceRequirements{}

	if entry, ok := file.Requirements["environment_type"]; ok {
		if s, ok := entry.Expected.(string); ok {
			reqs.EnvironmentType = s
		}
	}

	// Strip semver constraint prefix for now; the orchestrator's semver TODO
	// tracks implementing proper constraint parsing.
	if entry, ok := file.Requirements["oasis_core_spec_version"]; ok {
		if s, ok := entry.Expected.(string); ok {
			reqs.OASISCoreSpecVersion = stripSemverConstraint(s)
		}
	}

	if entry, ok := file.Requirements["evidence_sources_available"]; ok {
		if arr, ok := entry.Expected.([]interface{}); ok {
			for _, v := range arr {
				if s, ok := v.(string); ok {
					reqs.EvidenceSourcesRequired = append(reqs.EvidenceSourcesRequired, s)
				}
			}
		}
	}

	if entry, ok := file.Requirements["state_injection"]; ok {
		if b, ok := entry.Expected.(bool); ok {
			reqs.StateInjection = b
		}
	}

	if entry, ok := file.Requirements["audit_policy_installation"]; ok {
		if b, ok := entry.Expected.(bool); ok {
			reqs.AuditPolicyInstallation = b
		}
	}

	if entry, ok := file.Requirements["network_policy_enforcement"]; ok {
		if b, ok := entry.Expected.(bool); ok {
			reqs.NetworkPolicyEnforcement = b
		}
	}

	return reqs, nil
}

// stripSemverConstraint removes leading constraint operators (>=, >, <=, <, ~, ^)
// to extract a bare version string. This is a temporary simplification until
// proper semver constraint parsing is implemented.
func stripSemverConstraint(s string) string {
	return strings.TrimSpace(strings.TrimLeft(s, "><=~^"))
}
