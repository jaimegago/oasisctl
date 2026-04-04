package profile

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// profileMDResult holds everything parsed from profile.md.
type profileMDResult struct {
	Metadata                 evaluation.ProfileMetadata
	IntentPromotion          evaluation.IntentPromotionConfig
	AgentConfigurationSchema *evaluation.AgentConfigurationSchema
}

// parseProfileMD parses the profile.md file for metadata and intent promotion config.
func parseProfileMD(path string) (profileMDResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return profileMDResult{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var result profileMDResult
	scanner := bufio.NewScanner(f)
	var inYAMLBlock bool
	var yamlLines []string

	for scanner.Scan() {
		line := scanner.Text()

		// Collect YAML code blocks for intent promotion parsing.
		if strings.TrimSpace(line) == "```yaml" {
			inYAMLBlock = true
			yamlLines = nil
			continue
		}
		if inYAMLBlock && strings.TrimSpace(line) == "```" {
			inYAMLBlock = false
			// Try to parse as intent promotion config.
			cfg, err := parseIntentPromotionYAML(yamlLines)
			if err == nil && (len(cfg.RequiredFor) > 0 || len(cfg.RecommendedFor) > 0) {
				result.IntentPromotion = cfg
			}
			// Try to parse as agent configuration schema.
			schema, err := parseAgentConfigSchemaYAML(yamlLines)
			if err == nil && schema != nil && len(schema.Dimensions) > 0 {
				result.AgentConfigurationSchema = schema
			}
			continue
		}
		if inYAMLBlock {
			yamlLines = append(yamlLines, line)
			continue
		}

		// Parse H1 title as profile name.
		if strings.HasPrefix(line, "# ") && result.Metadata.Name == "" {
			result.Metadata.Name = strings.TrimPrefix(line, "# ")
			continue
		}

		// Parse bold key-value pairs like **Version:** 0.1.0-draft
		if strings.HasPrefix(line, "**Version:**") {
			result.Metadata.Version = strings.TrimSpace(strings.TrimPrefix(line, "**Version:**"))
			continue
		}
		if strings.HasPrefix(line, "**Domain:**") {
			result.Metadata.Domain = strings.TrimSpace(strings.TrimPrefix(line, "**Domain:**"))
			continue
		}
		if strings.HasPrefix(line, "**OASIS Core Dependency:**") {
			result.Metadata.OASISCore = strings.TrimSpace(strings.TrimPrefix(line, "**OASIS Core Dependency:**"))
			continue
		}

		// Parse profile identifier from metadata list: - **Profile identifier:** `foo`
		if strings.Contains(line, "**Profile identifier:**") {
			parts := strings.SplitN(line, "**Profile identifier:**", 2)
			if len(parts) == 2 {
				id := strings.TrimSpace(parts[1])
				id = strings.Trim(id, "`")
				result.Metadata.Identifier = id
			}
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		return profileMDResult{}, fmt.Errorf("scan %s: %w", path, err)
	}

	return result, nil
}

// intentPromotionWrapper is the YAML structure wrapping the intent config.
type intentPromotionWrapper struct {
	ProfileValidation struct {
		Intent evaluation.IntentPromotionConfig `yaml:"intent"`
	} `yaml:"profile_validation"`
}

// parseIntentPromotionYAML parses the intent promotion YAML block from profile.md.
func parseIntentPromotionYAML(lines []string) (evaluation.IntentPromotionConfig, error) {
	raw := strings.Join(lines, "\n")
	var wrapper intentPromotionWrapper
	if err := yaml.Unmarshal([]byte(raw), &wrapper); err != nil {
		return evaluation.IntentPromotionConfig{}, err
	}
	return wrapper.ProfileValidation.Intent, nil
}

// agentConfigSchemaWrapper is the YAML structure wrapping the agent configuration schema.
type agentConfigSchemaWrapper struct {
	AgentConfigurationSchema *evaluation.AgentConfigurationSchema `yaml:"agent_configuration_schema"`
}

// parseAgentConfigSchemaYAML parses the agent configuration schema from a YAML block in profile.md.
func parseAgentConfigSchemaYAML(lines []string) (*evaluation.AgentConfigurationSchema, error) {
	raw := strings.Join(lines, "\n")
	var wrapper agentConfigSchemaWrapper
	if err := yaml.Unmarshal([]byte(raw), &wrapper); err != nil {
		return nil, err
	}
	return wrapper.AgentConfigurationSchema, nil
}
