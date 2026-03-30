package profile

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// StimulusParser parses stimulus-library.md.
type StimulusParser struct{}

// NewStimulusParser creates a StimulusParser.
func NewStimulusParser() *StimulusParser {
	return &StimulusParser{}
}

// Parse reads stimulus-library.md and returns a map of ID -> Stimulus.
// Each entry has an H3 header with "STIM-XX-NNN: Name", followed by a YAML code block.
func (p *StimulusParser) Parse(path string) (map[string]evaluation.Stimulus, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	stimuli := make(map[string]evaluation.Stimulus)
	scanner := bufio.NewScanner(f)

	var currentID string
	var inCodeBlock bool
	var codeContent bytes.Buffer

	flushCode := func() {
		if currentID == "" || codeContent.Len() == 0 {
			return
		}
		var s evaluation.Stimulus
		if err := yaml.Unmarshal(codeContent.Bytes(), &s); err == nil {
			stimuli[currentID] = s
		}
		codeContent.Reset()
	}

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "### ") {
			flushCode()
			currentID = ""
			// Extract STIM-XX-NNN identifier from header.
			header := strings.TrimPrefix(line, "### ")
			if idx := strings.Index(header, ":"); idx > 0 {
				candidate := strings.TrimSpace(header[:idx])
				if strings.HasPrefix(candidate, "STIM-") {
					currentID = candidate
				}
			}
			inCodeBlock = false
			continue
		}

		if strings.TrimSpace(line) == "```yaml" {
			inCodeBlock = true
			continue
		}

		if strings.TrimSpace(line) == "```" && inCodeBlock {
			inCodeBlock = false
			flushCode()
			continue
		}

		if inCodeBlock && currentID != "" {
			codeContent.WriteString(line)
			codeContent.WriteByte('\n')
		}
	}

	flushCode()

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}

	return stimuli, nil
}
