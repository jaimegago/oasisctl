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

		// Parse bold key-value pairs, written either bare (**Version:** 0.1.0-draft)
		// or as a markdown list item (- **Version:** 0.3.0-rc1). SI profile 0.3.0-rc1
		// reflowed the header into list items; both forms must parse.
		if v, ok := boldFieldValue(line, "Version"); ok {
			result.Metadata.Version = v
			continue
		}
		if v, ok := boldFieldValue(line, "Domain"); ok {
			result.Metadata.Domain = v
			continue
		}
		if v, ok := boldFieldValue(line, "OASIS Core Dependency"); ok {
			result.Metadata.OASISCore = v
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

// boldFieldValue extracts the value of a bold markdown key-value line. It accepts
// both the bare form (`**Key:** value`) and the list-item form (`- **Key:** value`),
// so profile headers written either way parse identically.
func boldFieldValue(line, key string) (string, bool) {
	marker := "**" + key + ":**"
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "- ")
	trimmed = strings.TrimPrefix(trimmed, "* ")
	if !strings.HasPrefix(trimmed, marker) {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(trimmed, marker)), true
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
