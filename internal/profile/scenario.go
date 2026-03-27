package profile

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// ScenarioParser parses multi-document YAML scenario files.
type ScenarioParser struct{}

// NewScenarioParser creates a ScenarioParser.
func NewScenarioParser() *ScenarioParser {
	return &ScenarioParser{}
}

// Parse reads a YAML file containing multiple scenario documents separated by ---.
// Leading comment-only blocks (starting with #) are skipped.
func (p *ScenarioParser) Parse(_ context.Context, path string) ([]evaluation.Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	documents := splitYAMLDocuments(data)
	var scenarios []evaluation.Scenario

	for _, doc := range documents {
		trimmed := bytes.TrimSpace(doc)
		if len(trimmed) == 0 {
			continue
		}
		// Skip comment-only blocks.
		if isCommentBlock(trimmed) {
			continue
		}

		var s evaluation.Scenario
		if err := yaml.Unmarshal(trimmed, &s); err != nil {
			return nil, fmt.Errorf("parse scenario in %s: %w", path, err)
		}
		// Skip empty documents that unmarshal to zero value.
		if s.ID == "" {
			continue
		}
		scenarios = append(scenarios, s)
	}

	return scenarios, nil
}

// splitYAMLDocuments splits a YAML file on --- separators.
func splitYAMLDocuments(data []byte) [][]byte {
	var docs [][]byte
	var current []byte

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Bytes()
		if bytes.Equal(bytes.TrimSpace(line), []byte("---")) {
			docs = append(docs, current)
			current = nil
			continue
		}
		current = append(current, line...)
		current = append(current, '\n')
	}
	if len(current) > 0 {
		docs = append(docs, current)
	}
	return docs
}

// isCommentBlock returns true if every non-empty line starts with #.
func isCommentBlock(data []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			return false
		}
	}
	return true
}
