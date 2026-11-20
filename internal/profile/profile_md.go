package profile

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// profileMDResult holds everything parsed from profile.md.
type profileMDResult struct {
	Metadata                 evaluation.ProfileMetadata
	IntentPromotion          evaluation.IntentPromotionConfig
	AgentConfigurationSchema *evaluation.AgentConfigurationSchema
	ExecutionMinimums        evaluation.ExecutionMinimums
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

	// inExecutionCounts is true between the profile's "Execution counts"
	// heading and the next heading: the section spec/03-profiles.md §2.18
	// asks a profile for, where the minimums are stated in a table.
	var inExecutionCounts bool

	for scanner.Scan() {
		line := scanner.Text()

		if !inYAMLBlock && strings.HasPrefix(line, "#") {
			inExecutionCounts = isExecutionCountsHeading(line)
		}
		if !inYAMLBlock && inExecutionCounts {
			readExecutionMinimum(line, &result.ExecutionMinimums)
		}

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

// isExecutionCountsHeading reports whether a markdown heading opens the
// profile's execution-counts section (spec/03-profiles.md §2.18), whatever its
// level and number: "### 9.3 Execution counts" in the SI profile.
func isExecutionCountsHeading(line string) bool {
	title := strings.ToLower(strings.TrimSpace(strings.TrimLeft(line, "#")))
	return strings.HasSuffix(title, "execution counts")
}

// readExecutionMinimum reads one row of the execution-counts table,
// "| Safety | 3 | ... |" or "| Capability | 3 | ... |", into m.
//
// It fails closed: a row whose minimum is not a positive integer sets nothing,
// so a profile whose table cannot be read declares no capability minimum and
// an evaluation under it cannot be opened. The first row read for each count
// wins; a later table in the section does not overwrite it.
func readExecutionMinimum(line string, m *evaluation.ExecutionMinimums) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "|") {
		return
	}
	cells := strings.Split(strings.Trim(trimmed, "|"), "|")
	if len(cells) < 2 {
		return
	}
	name := strings.ToLower(strings.TrimSpace(cells[0]))
	n, err := strconv.Atoi(strings.TrimSpace(cells[1]))
	if err != nil || n < 1 {
		return
	}
	switch name {
	case "safety":
		if m.Safety == 0 {
			m.Safety = n
		}
	case "capability":
		if m.Capability == 0 {
			m.Capability = n
		}
	}
}
