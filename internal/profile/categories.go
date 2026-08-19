package profile

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// The profile states its category taxonomy and scoring model twice: as prose,
// and as a fenced YAML block beside that prose. This file reads the block.
//
// Parsing the prose was rejected deliberately — it would couple the runner to
// the markdown formatting of a public standard. The embedded-YAML idiom is the
// one profile.md already uses for intent promotion and the agent configuration
// schema, and parseProfileMD reads it the same way: scan the file, collect
// every ```yaml fence, and try each one against the wrapper this parser wants.
// A block that does not match is not an error; it belongs to something else.

// capabilityScoringWrapper is the YAML structure wrapping the capability
// category block in capability-categories.md.
type capabilityScoringWrapper struct {
	CapabilityScoring *struct {
		CoreDimensions []string           `yaml:"core_dimensions"`
		Categories     []capabilityCatDoc `yaml:"categories"`
	} `yaml:"capability_scoring"`
}

type capabilityCatDoc struct {
	ID               string             `yaml:"id"`
	Name             string             `yaml:"name"`
	Archetypes       []string           `yaml:"archetypes"`
	MapsToDimensions []string           `yaml:"maps_to_dimensions"`
	DimensionWeights map[string]float64 `yaml:"dimension_weights"`
	Aggregation      struct {
		Method           string             `yaml:"method"`
		ArchetypeWeights map[string]float64 `yaml:"archetype_weights"`
	} `yaml:"aggregation"`
}

// safetyScoringWrapper is the YAML structure wrapping the safety category
// block in safety-categories.md.
type safetyScoringWrapper struct {
	SafetyScoring *struct {
		SafetyTolerance float64        `yaml:"safety_tolerance"`
		Categories      []safetyCatDoc `yaml:"categories"`
	} `yaml:"safety_scoring"`
}

type safetyCatDoc struct {
	ID         string   `yaml:"id"`
	Name       string   `yaml:"name"`
	Archetypes []string `yaml:"archetypes"`
}

// capabilityScoring is the parsed capability block.
type capabilityScoring struct {
	CoreDimensions []string
	Categories     []evaluation.Category
}

// safetyScoring is the parsed safety block.
type safetyScoring struct {
	SafetyTolerance float64
	Categories      []evaluation.Category
}

// parseCapabilityCategories reads the capability category block from
// capability-categories.md. A file with no such block yields a nil result and
// no error: the block is optional, and a profile predating it still loads.
func parseCapabilityCategories(path string) (*capabilityScoring, error) {
	blocks, err := yamlBlocks(path)
	if err != nil {
		return nil, err
	}

	for _, raw := range blocks {
		var w capabilityScoringWrapper
		if err := yaml.Unmarshal([]byte(raw), &w); err != nil {
			continue // not this block
		}
		if w.CapabilityScoring == nil || len(w.CapabilityScoring.Categories) == 0 {
			continue
		}

		out := &capabilityScoring{CoreDimensions: w.CapabilityScoring.CoreDimensions}
		for _, doc := range w.CapabilityScoring.Categories {
			method, err := aggregationMethod(doc.Aggregation.Method)
			if err != nil {
				return nil, fmt.Errorf("category %q: %w", doc.ID, err)
			}
			out.Categories = append(out.Categories, evaluation.Category{
				ID:               doc.ID,
				Name:             doc.Name,
				Archetypes:       doc.Archetypes,
				MapsToDimensions: doc.MapsToDimensions,
				DimensionWeights: doc.DimensionWeights,
				Aggregation:      method,
				ArchetypeWeights: doc.Aggregation.ArchetypeWeights,
			})
		}
		return out, nil
	}

	return nil, nil
}

// parseSafetyCategories reads the safety category block from
// safety-categories.md. As above, absence is not an error.
func parseSafetyCategories(path string) (*safetyScoring, error) {
	blocks, err := yamlBlocks(path)
	if err != nil {
		return nil, err
	}

	for _, raw := range blocks {
		var w safetyScoringWrapper
		if err := yaml.Unmarshal([]byte(raw), &w); err != nil {
			continue // not this block
		}
		if w.SafetyScoring == nil || len(w.SafetyScoring.Categories) == 0 {
			continue
		}

		out := &safetyScoring{SafetyTolerance: w.SafetyScoring.SafetyTolerance}
		for _, doc := range w.SafetyScoring.Categories {
			out.Categories = append(out.Categories, evaluation.Category{
				ID:         doc.ID,
				Name:       doc.Name,
				Archetypes: doc.Archetypes,
			})
		}
		return out, nil
	}

	return nil, nil
}

// aggregationMethod maps the profile's declared method name onto the typed
// constant. An unrecognized method is an error rather than a silent default:
// silently averaging a category the profile said to minimize is the defect
// this parser exists to close.
func aggregationMethod(s string) (evaluation.AggregationMethod, error) {
	switch evaluation.AggregationMethod(s) {
	case evaluation.AggregationWeightedAverage:
		return evaluation.AggregationWeightedAverage, nil
	case evaluation.AggregationMinimum:
		return evaluation.AggregationMinimum, nil
	case "":
		return "", fmt.Errorf("no aggregation method declared")
	default:
		return "", fmt.Errorf("unknown aggregation method %q", s)
	}
}

// buildScoringModel inverts the per-category dimension weights into the
// dimension-keyed form the aggregator reads. The profile declares the weights
// per category, beside the category they belong to; the aggregation walks them
// per dimension.
func buildScoringModel(capability *capabilityScoring, safety *safetyScoring) evaluation.ScoringModel {
	model := evaluation.ScoringModel{}
	if safety != nil {
		model.SafetyTolerance = safety.SafetyTolerance
	}
	if capability == nil {
		return model
	}

	model.CoreDimensions = make(map[string]evaluation.DimensionConfig, len(capability.CoreDimensions))
	for _, dim := range capability.CoreDimensions {
		model.CoreDimensions[dim] = evaluation.DimensionConfig{
			ContributingCategories: map[string]float64{},
		}
	}
	for _, cat := range capability.Categories {
		for dim, weight := range cat.DimensionWeights {
			cfg, ok := model.CoreDimensions[dim]
			if !ok {
				// A weight naming a dimension the profile did not declare.
				cfg = evaluation.DimensionConfig{ContributingCategories: map[string]float64{}}
				model.CoreDimensions[dim] = cfg
			}
			cfg.ContributingCategories[cat.ID] = weight
		}
	}
	return model
}

// yamlBlocks returns the contents of every ```yaml fence in a markdown file.
func yamlBlocks(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var blocks []string
	var current []string
	var inBlock bool

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case !inBlock && strings.TrimSpace(line) == "```yaml":
			inBlock = true
			current = nil
		case inBlock && strings.TrimSpace(line) == "```":
			inBlock = false
			blocks = append(blocks, strings.Join(current, "\n"))
		case inBlock:
			current = append(current, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return blocks, nil
}
