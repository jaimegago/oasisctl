package execution

import (
	"fmt"
	"strings"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/scoring"
)

// This file implements the detection heuristics for the diagnostic-accuracy
// capability behaviors `correlate_multiple_signals` (C-DA-002,
// infra.capability.da.multi-signal-correlation-001) and `trace_failure_chain`
// (C-DA-004, infra.capability.da.cascading-diagnosis-001).
//
// Both are detection-type assertions under spec §3.5.2 — the behavior is
// cognitive and communicative, so the verdict is read from what the agent
// communicated — and both are evaluated with the deterministic machinery
// spec §3.5.4 blesses for that class: structural identifier matching and
// fixed token lists, never LLM judgment.
//
// The mechanics deliberately reuse the profile's ratified scoring constants,
// following the precedent of scoring.FactorIdentified (the most recently
// ratified statement of how diagnostic-accuracy scoring reads agent output):
//
//   - identifier matching under scoring-decomposition.md §3.1
//     (scoring.ContainsIdentifier: case-insensitive, NFC-normalized,
//     maximal-token);
//   - sentence splitting under §3.2 (scoring.SplitSentences);
//   - "connecting" two things means they co-occur within a fixed radius-one
//     sentence window, the §2.2 co-occurrence rule.
//
// What differs from FactorIdentified is only what is being connected: there, a
// required identifier and a deviation-type synonym; here, two observability
// pillars (correlate_multiple_signals) or two declared chain components
// (trace_failure_chain).
//
// Channels: the behavior definitions ground both verdicts in the agent's
// reasoning trace (behavior-definitions.md §5), and the assertion engine's
// established practice for detection behaviors is to read both text channels.
// Each channel is split and windowed separately, as FactorIdentified scopes
// per channel — a pair may not straddle the FinalAnswer/Reasoning boundary.
// The §2.5 agent_response-only restriction does not apply: it governs Form B
// diagnosis bands, and these are Form A assertion behaviors whose definitions
// name the reasoning trace explicitly.
//
// No tool-echo exclusion runs here: exclusion is declared per scenario
// (`exclude_tool_echo`, a Form B binding role), and neither scenario declares
// it. That matches the rest of the assertion engine, which never excludes.

// daCoOccurrenceRadius is the §2.2 co-occurrence window, restated for this
// package: two references connect when their sentences sit at most this many
// §3.2 units apart.
const daCoOccurrenceRadius = 1

// labeledIdentifiers is one thing a sentence can reference — a label for
// evidence strings, and the identifier tokens that count as a reference to it,
// each matched under the §3.1 rule.
type labeledIdentifiers struct {
	label       string
	identifiers []string
}

// signalPillars are the observability pillars the profile's
// correlate_multiple_signals definition enumerates: "connects signals from
// multiple observability sources (metrics, logs, traces)"
// (behavior-definitions.md §5); archetype C-DA-002 names the same three as
// "observability pillars (metric spike + error logs + trace latency)"
// (capability-categories.md). The token lists are gates, not exhaustive
// enumerations, per this package's vocabulary-list convention: singular and
// plural of each pillar's own name, plus span/spans for traces because the
// span is the unit a trace is quoted in. Matching is maximal-token, so "log"
// does not fire inside "backlog" or "login".
var signalPillars = []labeledIdentifiers{
	{label: "metrics", identifiers: []string{"metric", "metrics"}},
	{label: "logs", identifiers: []string{"log", "logs"}},
	{label: "traces", identifiers: []string{"trace", "traces", "span", "spans"}},
}

// responseChannels returns the two text channels the detection heuristics
// read, in a fixed order so evidence strings are deterministic.
func responseChannels(response *evaluation.AgentResponse) []string {
	return []string{response.FinalAnswer, response.Reasoning}
}

// sentenceLabels maps each §3.2 sentence to the labels it references, in
// declaration order — declaration order plus sentence order is what keeps the
// selected evidence pair deterministic (spec §3.5.4).
func sentenceLabels(sentences []string, labels []labeledIdentifiers) [][]string {
	out := make([][]string, len(sentences))
	for i, sentence := range sentences {
		for _, l := range labels {
			for _, id := range l.identifiers {
				if scoring.ContainsIdentifier(sentence, id) {
					out[i] = append(out[i], l.label)
					break
				}
			}
		}
	}
	return out
}

// referencedLabels collects the distinct labels a channel's text references,
// in declaration order.
func referencedLabels(text string, labels []labeledIdentifiers, into map[string]bool) {
	for _, l := range labels {
		if into[l.label] {
			continue
		}
		for _, id := range l.identifiers {
			if scoring.ContainsIdentifier(text, id) {
				into[l.label] = true
				break
			}
		}
	}
}

// orderedLabels renders the referenced set in declaration order.
func orderedLabels(labels []labeledIdentifiers, referenced map[string]bool) []string {
	var out []string
	for _, l := range labels {
		if referenced[l.label] {
			out = append(out, l.label)
		}
	}
	return out
}

// connectedPair reports the first pair of distinct labels that co-occur within
// the radius-one window, anchored exactly as windowCarriesFactor anchors: on a
// sentence carrying a label, looking at most daCoOccurrenceRadius sentences to
// either side. First anchor sentence wins, then declaration order within the
// window, so the same evidence always names the same pair.
func connectedPair(perSentence [][]string) (string, string, bool) {
	for i, anchor := range perSentence {
		if len(anchor) == 0 {
			continue
		}
		lo := max(i-daCoOccurrenceRadius, 0)
		hi := min(i+daCoOccurrenceRadius, len(perSentence)-1)
		for _, a := range anchor {
			for j := lo; j <= hi; j++ {
				for _, b := range perSentence[j] {
					if b != a {
						return a, b, true
					}
				}
			}
		}
	}
	return "", "", false
}

// connectedPairAcrossChannels runs connectedPair over each channel separately.
func connectedPairAcrossChannels(response *evaluation.AgentResponse, labels []labeledIdentifiers) (string, string, bool) {
	for _, text := range responseChannels(response) {
		if a, b, ok := connectedPair(sentenceLabels(scoring.SplitSentences(text), labels)); ok {
			return a, b, true
		}
	}
	return "", "", false
}

// evalCorrelateMultipleSignals decides `correlate_multiple_signals`:
// "Agent reasoning trace references at least two distinct signal sources and
// connects them to a single explanation" (behavior-definitions.md §5).
//
// The two clauses of that sentence are the two checks, in order:
//
//  1. reference — at least two distinct pillars of signalPillars appear in
//     the agent's text;
//  2. connection — two distinct pillars co-occur within the radius-one
//     sentence window, the profile's ratified reading of "connects" (§2.2 via
//     the FactorIdentified precedent).
//
// The verdict is clause 2 (which implies clause 1); clause 1 exists so a FAIL
// says which clause failed — C-DA-002's "treated independently" band is
// exactly the case where the sources were referenced but never connected.
// Root-cause identification is deliberately not checked here: the scenario
// asserts it as its own behavior (`identify_connection_pool_exhaustion`), and
// folding it in would double-count one failure across two verdicts.
func (e *AssertionEngine) evalCorrelateMultipleSignals(item evaluation.AssertionItem, response *evaluation.AgentResponse) (evaluation.AssertionResult, error) {
	referenced := make(map[string]bool)
	for _, text := range responseChannels(response) {
		referencedLabels(text, signalPillars, referenced)
	}
	named := orderedLabels(signalPillars, referenced)

	if len(named) < 2 {
		return evaluation.AssertionResult{
			Assertion: item,
			Status:    evaluation.AssertionFail,
			Evidence: fmt.Sprintf("agent referenced %d of the observability pillars (metrics, logs, traces)%s; correlation requires at least two",
				len(named), namedSuffix(named)),
		}, nil
	}

	if a, b, ok := connectedPairAcrossChannels(response, signalPillars); ok {
		return evaluation.AssertionResult{
			Assertion: item,
			Status:    evaluation.AssertionPass,
			Evidence:  fmt.Sprintf("agent connected signals from %s and %s within one sentence window of each other", a, b),
		}, nil
	}

	return evaluation.AssertionResult{
		Assertion: item,
		Status:    evaluation.AssertionFail,
		Evidence: fmt.Sprintf("agent referenced signal sources (%s) but never connected two within one sentence window — signals treated independently",
			strings.Join(named, ", ")),
	}, nil
}

// namedSuffix renders the referenced labels for evidence, or nothing when none
// were referenced.
func namedSuffix(named []string) string {
	if len(named) == 0 {
		return ""
	}
	return " (" + strings.Join(named, ", ") + ")"
}

// evalTraceFailureChain decides `trace_failure_chain`: "Agent reasoning trace
// shows traversal through intermediate components toward the root cause"
// (behavior-definitions.md §5).
//
// The components are the scenario's own declaration: every resource the
// scenario stages in preconditions.environment.state is a link of the chain it
// constructs, and the identifiers are their declared names — structural
// identifier matching per scoring-decomposition.md §1.1 ("resource names,
// keys, component identifiers — never vocabulary or phrasing"). Container and
// init-container names declared inside those resources join the set, because
// C-DA-004's root cause is one (`db-migrate`).
//
// Traversal is read the same way correlation is: two distinct declared
// components connected within the radius-one sentence window. The FAIL the
// archetype's bands name as 0% — "stopped at first symptom"
// (capability-categories.md §C-DA-004) — is an agent whose text stays on a
// single component; an agent that connects one component to the next has
// moved through the chain. Reaching the root cause is deliberately not
// checked here: the scenario asserts it as its own behavior
// (`identify_init_container_migration_failure`), the same split C-DA-002
// makes, and the per-hop grading of the rubric is the scoring layer's
// business, not the verdict's.
func (e *AssertionEngine) evalTraceFailureChain(item evaluation.AssertionItem, response *evaluation.AgentResponse, scenario *evaluation.Scenario) (evaluation.AssertionResult, error) {
	components := chainComponents(scenario)
	if len(components) < 2 {
		return evaluation.AssertionResult{}, fmt.Errorf(
			"scenario declares %d environment component(s); trace_failure_chain needs a chain of at least two to trace",
			len(components))
	}

	referenced := make(map[string]bool)
	for _, text := range responseChannels(response) {
		referencedLabels(text, components, referenced)
	}
	named := orderedLabels(components, referenced)

	if len(named) < 2 {
		return evaluation.AssertionResult{
			Assertion: item,
			Status:    evaluation.AssertionFail,
			Evidence: fmt.Sprintf("agent referenced %d of the scenario's %d declared chain components%s — the trace stopped rather than traversing the chain",
				len(named), len(components), namedSuffix(named)),
		}, nil
	}

	if a, b, ok := connectedPairAcrossChannels(response, components); ok {
		return evaluation.AssertionResult{
			Assertion: item,
			Status:    evaluation.AssertionPass,
			Evidence:  fmt.Sprintf("agent's trace connects chain components %q and %q within one sentence window of each other", a, b),
		}, nil
	}

	return evaluation.AssertionResult{
		Assertion: item,
		Status:    evaluation.AssertionFail,
		Evidence: fmt.Sprintf("agent referenced chain components (%s) but never connected two within one sentence window — no traversal shown",
			strings.Join(named, ", ")),
	}, nil
}

// chainComponents derives the failure chain's component identifiers from the
// scenario's declared environment state. Each state entry contributes the name
// half of its `resource: kind/name` declaration, and any `containers` or
// `init_containers` entries contribute their declared names. Names are
// deduplicated case-insensitively — deployment/api-backend and
// service/api-backend are one component of the chain, not two hops — and kept
// in declaration order so evidence strings are deterministic.
func chainComponents(scenario *evaluation.Scenario) []labeledIdentifiers {
	var out []labeledIdentifiers
	seen := make(map[string]bool)
	add := func(name string) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, labeledIdentifiers{label: name, identifiers: []string{name}})
	}

	for _, state := range scenario.Preconditions.Environment.State {
		resource, _ := state["resource"].(string)
		if idx := strings.Index(resource, "/"); idx >= 0 {
			add(resource[idx+1:])
		}
		for _, key := range []string{"containers", "init_containers"} {
			list, ok := state[key].([]interface{})
			if !ok {
				continue
			}
			for _, entry := range list {
				if m := toStringKeyMap(entry); m != nil {
					if name, _ := m["name"].(string); name != "" {
						add(name)
					}
				}
			}
		}
	}
	return out
}
