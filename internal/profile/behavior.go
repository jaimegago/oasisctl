package profile

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// BehaviorParser parses behavior-definitions.md.
type BehaviorParser struct{}

// NewBehaviorParser creates a BehaviorParser.
func NewBehaviorParser() *BehaviorParser {
	return &BehaviorParser{}
}

// Parse reads behavior-definitions.md and returns a map of identifier -> BehaviorDefinition.
// Format: H2 headers are groups, H3 headers with backtick identifiers are behaviors,
// paragraphs are definitions, bold "Verification:" labels introduce verification methods.
func (p *BehaviorParser) Parse(path string) (map[string]evaluation.BehaviorDefinition, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	defs := make(map[string]evaluation.BehaviorDefinition)
	scanner := bufio.NewScanner(f)

	var currentGroup string
	var currentID string
	var currentDesc strings.Builder
	var currentVerif strings.Builder
	var inVerification bool

	flush := func() {
		if currentID == "" {
			return
		}
		defs[currentID] = evaluation.BehaviorDefinition{
			Identifier:         currentID,
			Description:        strings.TrimSpace(currentDesc.String()),
			VerificationMethod: strings.TrimSpace(currentVerif.String()),
			Group:              currentGroup,
		}
		currentID = ""
		currentDesc.Reset()
		currentVerif.Reset()
		inVerification = false
	}

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "## ") {
			flush()
			currentGroup = strings.TrimPrefix(line, "## ")
			continue
		}

		if strings.HasPrefix(line, "### ") {
			flush()
			// Extract identifier from backticks: ### `identifier`
			rest := strings.TrimPrefix(line, "### ")
			rest = strings.TrimSpace(rest)
			if strings.HasPrefix(rest, "`") && strings.Contains(rest, "`") {
				end := strings.Index(rest[1:], "`")
				if end >= 0 {
					currentID = rest[1 : end+1]
				}
			}
			inVerification = false
			continue
		}

		if currentID == "" {
			continue
		}

		// Detect "**Verification:**" line.
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "**Verification:**") {
			inVerification = true
			verifText := strings.TrimPrefix(trimmed, "**Verification:**")
			verifText = strings.TrimSpace(verifText)
			if verifText != "" {
				currentVerif.WriteString(verifText)
				currentVerif.WriteString(" ")
			}
			continue
		}

		if inVerification {
			if trimmed == "" {
				inVerification = false
				continue
			}
			currentVerif.WriteString(trimmed)
			currentVerif.WriteString(" ")
		} else {
			if trimmed != "" && !strings.HasPrefix(trimmed, "---") {
				currentDesc.WriteString(trimmed)
				currentDesc.WriteString(" ")
			}
		}
	}

	flush()

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}

	return defs, nil
}
