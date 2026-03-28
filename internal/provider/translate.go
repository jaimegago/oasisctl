package provider

import (
	"fmt"
	"strings"
)

// topLevelFields are the fields that Petri expects at the top level of a StateEntry,
// aside from kind and name (which come from the resource field).
var topLevelFields = map[string]bool{
	"namespace":   true,
	"zone":        true,
	"labels":      true,
	"annotations": true,
	"data":        true,
}

// TranslateState converts raw scenario state entries (OASIS format) into the
// normalized format expected by the Petri provider API.
//
// OASIS format uses a "resource" field with "kind/name" (e.g. "deployment/payment-service")
// plus arbitrary fields. Petri expects separate "kind" and "name" fields, with known
// top-level fields preserved and everything else placed in a "spec" map.
func TranslateState(raw []map[string]interface{}) ([]map[string]interface{}, error) {
	out := make([]map[string]interface{}, 0, len(raw))
	for i, entry := range raw {
		translated, err := translateEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("state entry %d: %w", i, err)
		}
		out = append(out, translated)
	}
	return out, nil
}

func translateEntry(entry map[string]interface{}) (map[string]interface{}, error) {
	resourceVal, ok := entry["resource"]
	if !ok {
		return nil, fmt.Errorf("missing required field \"resource\"")
	}
	resource, ok := resourceVal.(string)
	if !ok {
		return nil, fmt.Errorf("\"resource\" field must be a string")
	}

	parts := strings.SplitN(resource, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("\"resource\" field %q must be in \"kind/name\" format", resource)
	}

	result := map[string]interface{}{
		"kind": parts[0],
		"name": parts[1],
	}

	spec := map[string]interface{}{}

	for k, v := range entry {
		if k == "resource" {
			continue
		}
		if topLevelFields[k] {
			result[k] = v
		} else {
			spec[k] = v
		}
	}

	if len(spec) > 0 {
		result["spec"] = spec
	}

	return result, nil
}
