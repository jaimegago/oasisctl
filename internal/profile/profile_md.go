package profile

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// parseProfileMD parses the profile.md file for metadata.
func parseProfileMD(path string) (evaluation.ProfileMetadata, error) {
	f, err := os.Open(path)
	if err != nil {
		return evaluation.ProfileMetadata{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var meta evaluation.ProfileMetadata
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := scanner.Text()

		// Parse H1 title as profile name.
		if strings.HasPrefix(line, "# ") && meta.Name == "" {
			meta.Name = strings.TrimPrefix(line, "# ")
			continue
		}

		// Parse bold key-value pairs like **Version:** 0.1.0-draft
		if strings.HasPrefix(line, "**Version:**") {
			meta.Version = strings.TrimSpace(strings.TrimPrefix(line, "**Version:**"))
			continue
		}
		if strings.HasPrefix(line, "**Domain:**") {
			meta.Domain = strings.TrimSpace(strings.TrimPrefix(line, "**Domain:**"))
			continue
		}
		if strings.HasPrefix(line, "**OASIS Core Dependency:**") {
			meta.OASISCore = strings.TrimSpace(strings.TrimPrefix(line, "**OASIS Core Dependency:**"))
			continue
		}

		// Parse profile identifier from metadata list: - **Profile identifier:** `foo`
		if strings.Contains(line, "**Profile identifier:**") {
			parts := strings.SplitN(line, "**Profile identifier:**", 2)
			if len(parts) == 2 {
				id := strings.TrimSpace(parts[1])
				id = strings.Trim(id, "`")
				meta.Identifier = id
			}
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		return evaluation.ProfileMetadata{}, fmt.Errorf("scan %s: %w", path, err)
	}

	return meta, nil
}
