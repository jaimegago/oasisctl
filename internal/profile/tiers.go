package profile

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// A profile states its minimum evaluation coverage per complexity tier
// (spec/03-profiles.md §2.7) as prose and nothing else: a "Tier N" heading, a
// "Minimum coverage:" label, and a list. There is no YAML block beside it the
// way there is beside the category taxonomy, so this file reads the prose.
//
// categories.go rejects prose parsing for the taxonomy, and the objection
// stands — it couples the runner to the markdown formatting of a public
// standard. It is accepted here because the alternative was a table of
// minimums written into the evaluator, which would be a second copy of
// normative text that nothing compares with the first. The coupling is kept
// narrow and it fails closed: a list item this parser does not recognise is
// carried verbatim as an uncomputed requirement, and a tier whose figure it
// cannot read is reported as having no declared minimum. Either way the
// evaluator reports minimum coverage as not met and says why; it never
// reports it met on a rule it did not read.

var (
	// "### Tier 1 — Minimal". Any heading level, so a profile that nests its
	// tiers differently still parses.
	tierHeadingRE = regexp.MustCompile(`^#{1,6}\s+Tier\s+([1-9][0-9]*)\b`)
	headingRE     = regexp.MustCompile(`^#{1,6}\s`)
	// "**Minimum coverage:**", bare or as a list item.
	minCoverageLabelRE = regexp.MustCompile(`(?i)^(?:[-*]\s+)?\*\*minimum coverage:?\*\*:?\s*$`)
	boldLabelRE        = regexp.MustCompile(`^\*\*[^*]+\*\*:?\s*$`)
	listItemRE         = regexp.MustCompile(`^[-*]\s+(.*)$`)
	// "All 7 safety categories: ≥1 archetype per category" and
	// "All 7 capability categories: all applicable archetypes, counted per §8.1".
	coverageFigureRE = regexp.MustCompile(
		`(?i)\b(safety|capability)\s+categories:\s*(?:(?:≥|>=)\s*([0-9]+)\s+archetypes?\b|(all)(?:\s+applicable)?\s+archetypes\b)`)
)

// parseTierRequirements reads each tier's minimum coverage from profile.md.
// A profile that states none yields an empty map and no error.
func parseTierRequirements(path string) (map[int]evaluation.TierRequirement, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	out := make(map[int]evaluation.TierRequirement)
	tier := 0           // the tier section being read; 0 outside one
	inCoverage := false // inside that tier's minimum-coverage list
	inFence := false

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if inFence || line == "" {
			continue
		}

		if m := tierHeadingRE.FindStringSubmatch(line); m != nil {
			tier, _ = strconv.Atoi(m[1])
			inCoverage = false
			continue
		}
		if headingRE.MatchString(line) {
			tier, inCoverage = 0, false
			continue
		}
		if tier == 0 {
			continue
		}

		if minCoverageLabelRE.MatchString(line) {
			inCoverage = true
			req := out[tier]
			req.Tier = tier
			out[tier] = req
			continue
		}
		if boldLabelRE.MatchString(line) {
			// Another labelled list in the same tier section — "Environment:".
			inCoverage = false
			continue
		}
		if !inCoverage {
			continue
		}

		item := listItemRE.FindStringSubmatch(line)
		if item == nil {
			// Prose after the list ends it.
			inCoverage = false
			continue
		}

		req := out[tier]
		m := coverageFigureRE.FindStringSubmatch(item[1])
		if m == nil {
			req.Uncomputed = append(req.Uncomputed, item[1])
			out[tier] = req
			continue
		}
		minimum := evaluation.CoverageMinimum{Declared: true}
		if m[3] != "" {
			minimum.All = true
		} else {
			minimum.Archetypes, _ = strconv.Atoi(m[2])
		}
		if strings.EqualFold(m[1], "safety") {
			req.Safety = minimum
		} else {
			req.Capability = minimum
		}
		out[tier] = req
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return out, nil
}
