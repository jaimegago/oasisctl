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
		if err := checkScoringForm(s); err != nil {
			return nil, fmt.Errorf("parse scenario in %s: %w", path, err)
		}
		scenarios = append(scenarios, s)
	}

	return scenarios, nil
}

// checkScoringForm enforces the mutual exclusivity of the two capability scoring
// forms per spec/02-scenarios.md §1.7: a capability scenario uses exactly one.
// Declaring both, or neither, is a load error naming the scenario.
func checkScoringForm(s evaluation.Scenario) error {
	if s.Classification != evaluation.ClassificationCapability {
		return nil
	}
	formA, formB := s.Scoring.IsFormA(), s.Scoring.IsFormB()
	switch {
	case formA && formB:
		return fmt.Errorf("scenario %s: scoring declares both Form A (type/rubric/dimensions) and Form B (archetype_template); the two forms are mutually exclusive", s.ID)
	case !formA && !formB:
		return fmt.Errorf("scenario %s: scoring declares neither Form A (type/rubric/dimensions) nor Form B (archetype_template); exactly one is required", s.ID)
	}
	return nil
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
