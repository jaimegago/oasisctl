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

	// "**Environment:**", bare or as a list item.
	environmentLabelRE = regexp.MustCompile(`(?i)^(?:[-*]\s+)?\*\*environment:?\*\*:?\s*$`)

	// The three environment figures a profile says count what scenario
	// preconditions declare (SI profile §8). Each pattern is anchored at the
	// start of the list item: a figure that appears inside a longer clause —
	// "Multi-cluster (≥2) or multi-namespace with team isolation (≥3
	// namespaces, ...)" — is one arm of an alternative, not a bound, and is
	// left unread rather than applied as if it stood alone.
	//
	// "≤3 namespaces", "≥3 namespaces".
	namespaceFigureRE = regexp.MustCompile(`(?i)^(≤|<=|≥|>=)\s*([0-9]+)\s+namespaces?\b`)
	// "1–5 microservices or single monolithic application", "8–15 microservices".
	microserviceRangeRE = regexp.MustCompile(`(?i)^([0-9]+)\s*[–-]\s*([0-9]+)\s+microservices?\b`)
	// "≥15 microservices with realistic dependency topology".
	microserviceBoundRE = regexp.MustCompile(`(?i)^(≤|<=|≥|>=)\s*([0-9]+)\s+microservices?\b`)
	// "At least 2 security zones".
	securityZoneFigureRE = regexp.MustCompile(`(?i)^at least\s+([0-9]+)\s+security zones?\b`)
)

// readEnvironmentFigure reads one item of a tier's Environment list into the
// tier's scenario-counted figures. An item that is none of the three figures,
// or states one in a form these patterns do not anchor on, leaves the figures
// as they were: the evaluator then reports that figure as not judged at the
// tier, never as met.
func readEnvironmentFigure(item string, figures *evaluation.TierFigures) {
	bound := func(op string, n int) *evaluation.FigureRange {
		r := &evaluation.FigureRange{Text: item}
		if op == "≤" || op == "<=" {
			r.Max = &n
		} else {
			r.Min = &n
		}
		return r
	}
	if m := namespaceFigureRE.FindStringSubmatch(item); m != nil {
		n, _ := strconv.Atoi(m[2])
		figures.Namespaces = bound(m[1], n)
		return
	}
	if m := microserviceRangeRE.FindStringSubmatch(item); m != nil {
		lo, _ := strconv.Atoi(m[1])
		hi, _ := strconv.Atoi(m[2])
		figures.Microservices = &evaluation.FigureRange{Min: &lo, Max: &hi, Text: item}
		return
	}
	if m := microserviceBoundRE.FindStringSubmatch(item); m != nil {
		n, _ := strconv.Atoi(m[2])
		figures.Microservices = bound(m[1], n)
		return
	}
	if m := securityZoneFigureRE.FindStringSubmatch(item); m != nil {
		n, _ := strconv.Atoi(m[1])
		figures.SecurityZones = &evaluation.FigureRange{Min: &n, Text: item}
	}
}

// parseTierRequirements reads each tier's minimum coverage from profile.md.
// A profile that states none yields an empty map and no error.
func parseTierRequirements(path string) (map[int]evaluation.TierRequirement, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	out := make(map[int]evaluation.TierRequirement)
	tier := 0              // the tier section being read; 0 outside one
	inCoverage := false    // inside that tier's minimum-coverage list
	inEnvironment := false // inside that tier's environment list
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
			inCoverage, inEnvironment = false, false
			continue
		}
		if headingRE.MatchString(line) {
			tier, inCoverage, inEnvironment = 0, false, false
			continue
		}
		if tier == 0 {
			continue
		}

		if minCoverageLabelRE.MatchString(line) {
			inCoverage, inEnvironment = true, false
			req := out[tier]
			req.Tier = tier
			out[tier] = req
			continue
		}
		if environmentLabelRE.MatchString(line) {
			inEnvironment, inCoverage = true, false
			continue
		}
		if boldLabelRE.MatchString(line) {
			// Another labelled list in the same tier section.
			inCoverage, inEnvironment = false, false
			continue
		}
		if inEnvironment {
			item := listItemRE.FindStringSubmatch(line)
			if item == nil {
				inEnvironment = false
				continue
			}
			// The figures are read whether or not the tier states a minimum
			// coverage: they are a different requirement. A tier that states
			// only figures still gets an entry, with no coverage declared.
			req := out[tier]
			req.Tier = tier
			readEnvironmentFigure(item[1], &req.Figures)
			out[tier] = req
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
