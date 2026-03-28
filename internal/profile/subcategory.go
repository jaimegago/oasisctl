package profile

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// parseSubcategories extracts SubcategoryDefinition entries from the
// "Safety subcategories" table in safety-categories.md.
func parseSubcategories(path string) ([]evaluation.SubcategoryDefinition, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var inTable bool
	var headerSkipped bool
	var defs []evaluation.SubcategoryDefinition

	for scanner.Scan() {
		line := scanner.Text()

		// Look for the subcategories section header.
		if strings.Contains(line, "## Safety subcategories") {
			inTable = true
			continue
		}

		// Stop at the next section.
		if inTable && strings.HasPrefix(line, "## ") && !strings.Contains(line, "Safety subcategories") {
			break
		}

		if !inTable {
			continue
		}

		// Skip non-table lines and the separator row.
		if !strings.HasPrefix(line, "|") {
			continue
		}
		if strings.Contains(line, "---") {
			headerSkipped = true
			continue
		}
		// Skip the header row (first pipe row before separator).
		if !headerSkipped {
			continue
		}

		def, err := parseSubcategoryRow(line)
		if err != nil {
			continue // skip malformed rows
		}
		defs = append(defs, def)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}

	return defs, nil
}

// parseSubcategoryRow parses a single markdown table row into a SubcategoryDefinition.
// Expected format: | Subcategory | Parent category(ies) | Description |
func parseSubcategoryRow(line string) (evaluation.SubcategoryDefinition, error) {
	// Split on | and trim.
	parts := strings.Split(line, "|")
	// A valid row has at least 4 parts: empty, col1, col2, col3, empty
	var cells []string
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			cells = append(cells, trimmed)
		}
	}
	if len(cells) < 3 {
		return evaluation.SubcategoryDefinition{}, fmt.Errorf("malformed row: %s", line)
	}

	// Parse parent categories (comma-separated).
	rawParents := strings.Split(cells[1], ",")
	var parents []string
	for _, p := range rawParents {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			parents = append(parents, trimmed)
		}
	}

	return evaluation.SubcategoryDefinition{
		Identifier:       cells[0],
		ParentCategories: parents,
		Description:      cells[2],
	}, nil
}
