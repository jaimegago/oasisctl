package execution

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// A complexity tier's namespace, microservice and security-zone figures count
// what scenario preconditions declare, and the evaluation implementation
// judges them from the scenarios it runs (SI profile §8). What a scenario
// declares is not a property of the environment, so the provider cannot judge
// it; this file is the judging side.
//
// The rule, exactly:
//
//   - namespaces and microservices bound ONE scenario's environment. Each is
//     compared with the largest number any one executed scenario's
//     preconditions declare;
//   - security zones are met ACROSS the scenarios run. The figure is compared
//     with the number of distinct zones the executed scenarios' preconditions
//     declare.
//
// What is counted, from `preconditions.environment.state`:
//
//   - a namespace is a name a state entry declares, either as the resource
//     itself (`resource: namespace/payments`) or as the `namespace` of another
//     resource. Namespaces the provider adds for its platform or its own
//     bookkeeping are not in a scenario and are not counted;
//   - a microservice is a workload or service a state entry declares:
//     `deployment/`, `statefulset/`, `daemonset/` or `service/`. A deployment
//     and the service in front of it share a name and are one microservice,
//     so entries are counted once per namespace and name;
//   - a zone is the `zone` a state entry declares.

// workloadKinds are the resource kinds counted as a microservice.
var workloadKinds = map[string]bool{
	"deployment":  true,
	"statefulset": true,
	"daemonset":   true,
	"service":     true,
}

// scenarioDeclarations is what one scenario's preconditions declare toward
// the three figures, each sorted.
type scenarioDeclarations struct {
	namespaces    []string
	microservices []string
	zones         []string
}

// declarationsOf reads a scenario's `preconditions.environment.state`.
func declarationsOf(s *evaluation.Scenario) scenarioDeclarations {
	namespaces := map[string]struct{}{}
	microservices := map[string]struct{}{}
	zones := map[string]struct{}{}

	for _, entry := range s.Preconditions.Environment.State {
		resource, _ := entry["resource"].(string)
		kind, name, _ := strings.Cut(strings.TrimSpace(resource), "/")
		kind = strings.ToLower(strings.TrimSpace(kind))
		name = strings.TrimSpace(name)
		namespace, _ := entry["namespace"].(string)
		namespace = strings.TrimSpace(namespace)

		if kind == "namespace" && isConcreteName(name) {
			namespaces[name] = struct{}{}
		}
		if isConcreteName(namespace) {
			namespaces[namespace] = struct{}{}
		}
		if workloadKinds[kind] && isConcreteName(name) {
			microservices[namespace+"/"+name] = struct{}{}
		}
		if zone, _ := entry["zone"].(string); strings.TrimSpace(zone) != "" {
			zones[strings.TrimSpace(zone)] = struct{}{}
		}
	}
	return scenarioDeclarations{
		namespaces:    sortedKeys(namespaces),
		microservices: sortedKeys(microservices),
		zones:         sortedKeys(zones),
	}
}

// isConcreteName reports whether a declared name identifies one thing. A
// wildcard names a set and is not a declaration of any member of it.
func isConcreteName(name string) bool {
	return name != "" && !strings.ContainsAny(name, "*?")
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// JudgeScenarioFigures judges the claimed tier's three scenario-counted
// figures from the scenarios the run executed, and returns one check per
// figure, in a fixed order, each saying what was counted.
//
// requirement is the profile's requirement for the claimed tier; a figure it
// does not carry — the profile states none for the tier, or states it in a
// form the evaluator does not read — is reported not_assessed and is never
// read as met. So is every figure when no scenario was executed.
//
// It is a pure function of its arguments.
func JudgeScenarioFigures(requirement evaluation.TierRequirement, tier int, executed []evaluation.Scenario) []evaluation.TierCheck {
	type most struct {
		count    int
		scenario string
		names    []string
	}
	var mostNamespaces, mostMicroservices most
	zones := map[string]struct{}{}

	for i := range executed {
		d := declarationsOf(&executed[i])
		// Strictly greater, over scenarios in run order, so the scenario named
		// is the first to declare the largest number and the check is the same
		// on every run.
		if len(d.namespaces) > mostNamespaces.count {
			mostNamespaces = most{len(d.namespaces), executed[i].ID, d.namespaces}
		}
		if len(d.microservices) > mostMicroservices.count {
			mostMicroservices = most{len(d.microservices), executed[i].ID, d.microservices}
		}
		for _, z := range d.zones {
			zones[z] = struct{}{}
		}
	}
	distinctZones := sortedKeys(zones)
	n := len(executed)

	largest := func(m most, noun string) string {
		if m.count == 0 {
			return fmt.Sprintf("no scenario's preconditions declare a %s, over %d scenario(s) executed", noun, n)
		}
		return fmt.Sprintf("largest declared by one scenario: %d, in %s (%s); counted over %d scenario(s) executed",
			m.count, m.scenario, strings.Join(m.names, ", "), n)
	}

	return []evaluation.TierCheck{
		judgeFigure(evaluation.TierFigureNamespaces, "namespaces", requirement.Figures.Namespaces, tier, n,
			mostNamespaces.count, largest(mostNamespaces, "namespace")),
		judgeFigure(evaluation.TierFigureMicroservices, "microservices", requirement.Figures.Microservices, tier, n,
			mostMicroservices.count, largest(mostMicroservices, "microservice")),
		judgeFigure(evaluation.TierFigureSecurityZones, "security zones", requirement.Figures.SecurityZones, tier, n,
			len(distinctZones), fmt.Sprintf("%d distinct declared across %d scenario(s) executed%s",
				len(distinctZones), n, namesSuffix(distinctZones))),
	}
}

func namesSuffix(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return ": " + strings.Join(names, ", ")
}

// judgeFigure compares one count with the tier's figure.
func judgeFigure(requirement, noun string, figure *evaluation.FigureRange, tier, executed, count int, observed string) evaluation.TierCheck {
	check := evaluation.TierCheck{Requirement: requirement, Observed: observed}
	if figure == nil {
		check.Required = fmt.Sprintf("the profile states no %s figure for tier %d that this evaluator reads", noun, tier)
		check.Status = evaluation.TierCheckNotAssessed
		return check
	}
	check.Required = figure.Text
	if executed == 0 {
		check.Status = evaluation.TierCheckNotAssessed
		return check
	}
	check.Status = evaluation.TierCheckMet
	if figure.Min != nil && count < *figure.Min {
		check.Status = evaluation.TierCheckUnmet
	}
	if figure.Max != nil && count > *figure.Max {
		check.Status = evaluation.TierCheckUnmet
	}
	return check
}

// executedScenarios returns, in result order, the definitions of the
// scenarios the run executed: every scenario with a result other than
// NOT_APPLICABLE. A scenario the configuration excluded was never
// provisioned, so what its preconditions declare was never in an environment.
func executedScenarios(results []evaluation.ScenarioResult, definitions []evaluation.Scenario) []evaluation.Scenario {
	byID := make(map[string]int, len(definitions))
	for i := range definitions {
		byID[definitions[i].ID] = i
	}
	var out []evaluation.Scenario
	for _, r := range results {
		if r.Status == evaluation.ScenarioNotApplicable {
			continue
		}
		if i, ok := byID[r.ScenarioID]; ok {
			out = append(out, definitions[i])
		}
	}
	return out
}

// unjudgedFigures names the scenario-counted figures that are not met: unmet,
// or not assessed. A run with any does not have its claimed tier shown.
func unjudgedFigures(figures []evaluation.TierCheck) []string {
	var out []string
	for _, c := range figures {
		switch c.Status {
		case evaluation.TierCheckMet:
		case evaluation.TierCheckUnmet:
			out = append(out, fmt.Sprintf("tier figure %q is unmet: %s (required: %s)", c.Requirement, c.Observed, c.Required))
		default:
			out = append(out, fmt.Sprintf("tier figure %q was not judged: %s", c.Requirement, c.Required))
		}
	}
	return out
}
