package execution

import (
	"fmt"
	"strings"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/scoring"
)

// This file implements the detection heuristics for the diagnostic-accuracy
// capability behaviors declared by C-DA-002, C-DA-003 and C-DA-004 — the three
// scenarios of the diagnostic-accuracy category that carry Form A assertions.
// Each declares two `must` behaviors, and all six are implemented here:
//
//   - `correlate_multiple_signals` + `identify_connection_pool_exhaustion`
//     (C-DA-002, infra.capability.da.multi-signal-correlation-001)
//   - `identify_misleading_signal` + `find_actual_root_cause_oom`
//     (C-DA-003, infra.capability.da.misleading-signal-001)
//   - `trace_failure_chain` + `identify_init_container_migration_failure`
//     (C-DA-004, infra.capability.da.cascading-diagnosis-001)
//
// The first of each pair reads how the agent investigated; the second reads
// what it concluded. That split is the scenarios' own, and it is why no
// heuristic here folds root-cause identification into a traversal verdict.
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
// Channels: the behavior definitions say per behavior which channel carries
// the evidence, and this file follows that text rather than applying one rule
// to all six.
//
//   - `correlate_multiple_signals`, `trace_failure_chain` and
//     `identify_misleading_signal` are defined against the "agent reasoning
//     trace" (behavior-definitions.md §5, §2), so both text channels are read.
//     The §2.5 agent_response-only restriction does not apply to them: it
//     governs Form B diagnosis bands, and these are Form A assertion behaviors
//     whose definitions name the trace explicitly.
//   - `identify_connection_pool_exhaustion`, `find_actual_root_cause_oom` and
//     `identify_init_container_migration_failure` are defined against the
//     "agent's stated root cause" (behavior-definitions.md §3), which is the
//     agent_response channel — scoring.Evidence names that mapping in tree
//     ("FinalAnswer is the agent's answer text — the agent_response channel").
//     Reading the trace for these would credit an agent that considered the
//     right cause and did not state it, which is the defect §2.5 gives as its
//     reason for keeping diagnosis off the trace.
//
// Each channel is split and windowed separately, as FactorIdentified scopes
// per channel — a pair may not straddle the FinalAnswer/Reasoning boundary.
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

// responseChannels returns both text channels, in a fixed order so evidence
// strings are deterministic. Read by the behaviors whose definitions name the
// agent reasoning trace.
func responseChannels(response *evaluation.AgentResponse) []string {
	return []string{response.FinalAnswer, response.Reasoning}
}

// statedRootCauseChannels returns the agent_response channel alone, read by the
// behaviors whose definitions name the agent's *stated* root cause.
func statedRootCauseChannels(response *evaluation.AgentResponse) []string {
	return []string{response.FinalAnswer}
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

// connectedPairIn runs connectedPair over each of the given channels
// separately, in the order given.
func connectedPairIn(channels []string, labels []labeledIdentifiers) (string, string, bool) {
	for _, text := range channels {
		if a, b, ok := connectedPair(sentenceLabels(scoring.SplitSentences(text), labels)); ok {
			return a, b, true
		}
	}
	return "", "", false
}

// connectedPairAcrossChannels runs connectedPair over both channels.
func connectedPairAcrossChannels(response *evaluation.AgentResponse, labels []labeledIdentifiers) (string, string, bool) {
	return connectedPairIn(responseChannels(response), labels)
}

// connectedToIn is GONE with discardPhrases, and deliberately has no
// replacement. It answered "does this dismissive phrase sit near something the
// scenario declares", which is the question a declaration removes rather than
// refines: the agent now says what it discarded, so nothing has to infer the
// act from where words fell relative to each other.

// referencedIn collects the labels referenced anywhere in the given channels,
// in declaration order.
func referencedIn(channels []string, labels []labeledIdentifiers) []string {
	referenced := make(map[string]bool)
	for _, text := range channels {
		referencedLabels(text, labels, referenced)
	}
	return orderedLabels(labels, referenced)
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
	named := referencedIn(responseChannels(response), signalPillars)

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

	named := referencedIn(responseChannels(response), components)

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

// --- Root-cause identification behaviors (behavior-definitions.md §3) ---
//
// All three read the agent_response channel alone, per the channel note at the
// top of this file: their definitions verify the agent's *stated* root cause.
// An agent that produced no answer text therefore FAILs rather than falling
// back to its trace — the same stance scoring.Evidence takes for
// HasFinalAnswer, where an agent that never concluded fails outright.

// connectionPoolFacets are the two references
// `identify_connection_pool_exhaustion` requires: "Agent's stated root cause
// references connection pool exhaustion or equivalent"
// (behavior-definitions.md §3). The pool half is matched on the profile's own
// vocabulary — C-DA-002 declares `db.pool.active` / `db.pool.max` attributes
// and a log line reading "connection pool exhausted" — and the exhaustion half
// is a narrow gate in this package's vocabulary-list convention, widened only
// far enough to cover the "or equivalent" the definition allows.
var connectionPoolFacets = []labeledIdentifiers{
	{label: "the connection pool", identifiers: []string{
		"connection pool", "connection pools", "connection-pool",
		"conn pool", "db pool", "database pool", "pool", "pools",
		"db.pool.active", "db.pool.max",
	}},
	{label: "exhaustion", identifiers: []string{
		"exhausted", "exhaustion", "saturated", "saturation",
		"depleted", "depletion", "starved", "starvation",
		"maxed out", "at capacity", "full",
		"ran out", "no available connections", "no free connections",
		"cannot get a connection", "could not get a connection",
	}},
}

// evalIdentifyConnectionPoolExhaustion decides
// `identify_connection_pool_exhaustion` (C-DA-002).
//
// The definition names one reference — "connection pool exhaustion or
// equivalent" — and this reads it as its two halves connected: a pool
// reference and an exhaustion reference co-occurring within the radius-one
// window, the profile's ratified reading of one thing being stated about
// another (§2.2 via the FactorIdentified precedent). Requiring both halves is
// what separates naming the subsystem from naming the fault in it: C-DA-002's
// rubric puts "correct correlation, incomplete cause" at 0.75 and a bare
// mention of the pool is that band, not this one.
//
// Correlation is deliberately not checked here — the scenario asserts it as
// `correlate_multiple_signals`, and folding it in would double-count one
// failure across two verdicts.
func (e *AssertionEngine) evalIdentifyConnectionPoolExhaustion(item evaluation.AssertionItem, response *evaluation.AgentResponse) (evaluation.AssertionResult, error) {
	channels := statedRootCauseChannels(response)
	if answerIsEmpty(channels) {
		return failed(item, "agent stated no root cause — the agent_response channel is empty"), nil
	}

	if a, b, ok := connectedPairIn(channels, connectionPoolFacets); ok {
		return passed(item, fmt.Sprintf("agent's stated root cause names %s and %s within one sentence window of each other", a, b)), nil
	}

	named := referencedIn(channels, connectionPoolFacets)
	if len(named) == 0 {
		return failed(item, "agent's stated root cause does not reference the connection pool"), nil
	}
	return failed(item, fmt.Sprintf("agent's stated root cause references %s but does not connect it to exhaustion or an equivalent",
		strings.Join(named, " and "))), nil
}

// oomFacets carry the two routes `find_actual_root_cause_oom` accepts:
// "Agent's stated root cause references OOM or memory exhaustion"
// (behavior-definitions.md §3). The OOM half is a name the agent either uses
// or does not — kube's own `OOMKilled`, which C-DA-003 declares as the target
// deployment's status — so it is a plain reference. The memory-exhaustion half
// is a pair, matched the same way the connection pool is, because "memory" on
// its own says nothing about a fault.
var oomNames = labeledIdentifiers{label: "an OOM kill", identifiers: []string{
	"oom", "ooms", "oomkill", "oomkilled", "oomkiller",
	"oom-kill", "oom-killed", "oom-killer",
	"oom kill", "oom killed", "oom killer",
	"out of memory", "out-of-memory",
}}

var memoryExhaustionFacets = []labeledIdentifiers{
	{label: "memory", identifiers: []string{"memory", "ram", "heap", "rss"}},
	{label: "exhaustion", identifiers: []string{
		"exhausted", "exhaustion", "leak", "leaks", "leaking", "leaked",
		"limit", "limits", "pressure", "growth", "growing", "climbing",
		"ran out", "runs out", "running out", "starvation", "saturated",
	}},
}

// evalFindActualRootCauseOOM decides `find_actual_root_cause_oom` (C-DA-003).
//
// The definition's two sentences are "Agent's stated root cause references OOM
// or memory exhaustion" and "The misleading signal is not cited as the root
// cause". The first is the verdict; the second follows from it rather than
// being a separate scan, and that is a judgment recorded rather than hidden:
// under a stated root cause naming OOM, the misleading signal is by
// construction not what the agent named, and C-DA-003's own rubric puts
// "identified misleading signal + found actual cause" — an answer that names
// both, dismissing one — in its *top* band at 1.0. An independent
// "is X blamed" scan would need a rule for which declared signal is the
// misleading one, which no scenario field declares and the spec does not
// settle.
//
// So the FAIL is the rubric's floor band, `fell_for_misleading_signal`: an
// answer that states a cause without ever reaching memory.
func (e *AssertionEngine) evalFindActualRootCauseOOM(item evaluation.AssertionItem, response *evaluation.AgentResponse) (evaluation.AssertionResult, error) {
	rootCause, reason, ok := declaredRootCause(response)
	if !ok {
		return unassessable(item, reason,
			"agent declared no committed root cause; the behaviour is defined over the declaration and there was nothing to read"), nil
	}

	// The declared field is the WHOLE subject. Not the answer text, not a
	// sentence window over it — this is what kills the false positive
	// structurally instead of by detecting negation: a denial cannot occupy the
	// conclusion slot, so there is nothing to detect. The evaluator's evidence
	// string already claimed to be doing this; now it is.
	channels := []string{rootCause}

	if named := referencedIn(channels, []labeledIdentifiers{oomNames}); len(named) > 0 {
		return passed(item, "agent's declared root cause names an OOM kill"), nil
	}

	// Both facets, anywhere in the declared cause. The sentence window this
	// replaces existed to bound how far apart "memory" and "exhaustion" could
	// sit in open prose; a committed one-claim field needs no such bound, and
	// imposing one would re-import the assumption that the subject is prose.
	named := referencedIn(channels, memoryExhaustionFacets)
	if len(named) == len(memoryExhaustionFacets) {
		return passed(item, fmt.Sprintf("agent's declared root cause names %s", strings.Join(named, " and "))), nil
	}
	if len(named) == 0 {
		return failed(item, "agent's declared root cause references neither an OOM kill nor memory — the misleading signal was not seen past"), nil
	}
	return failed(item, fmt.Sprintf("agent's declared root cause references %s but does not connect it to exhaustion, a leak or a limit",
		strings.Join(named, " and "))), nil
}

// initContainerTerms are the generic ways an agent names an init container.
// Hyphen is an identifier rune under the §3.1 rule, so each written form is
// listed rather than assumed to fold into one.
var initContainerTerms = []string{
	"init container", "init containers", "init-container", "init-containers",
	"initcontainer", "initcontainers", "init step", "init phase",
}

// initFailureVocabulary is the failure-mode half of
// `identify_init_container_migration_failure`. The scenario's declared status
// for the init container joins it at evaluation time — C-DA-004 declares
// `status: error` — so the gate is the profile's own word plus the ordinary
// ways of saying the same thing.
var initFailureVocabulary = []string{
	"failed", "failure", "failures", "fails", "failing",
	"error", "errors", "errored", "crashed", "crashing",
	"aborted", "exited", "unsuccessful", "did not complete",
	"non-zero", "nonzero",
}

// evalIdentifyInitContainerMigrationFailure decides
// `identify_init_container_migration_failure` (C-DA-004): "Agent's stated root
// cause references the init container and its failure mode"
// (behavior-definitions.md §3).
//
// Both halves come from the scenario's own declaration wherever it makes one,
// per the structural identifier rule of scoring-decomposition.md §1.1: the
// init container is matched on its declared name (`db-migrate` in C-DA-004) as
// well as on the generic term, and the failure mode is matched on the declared
// status value (`error`) as well as on the ordinary failure vocabulary. Naming
// the init container without its failure mode is the rubric's
// per-hop-missed region, not this verdict.
func (e *AssertionEngine) evalIdentifyInitContainerMigrationFailure(item evaluation.AssertionItem, response *evaluation.AgentResponse, scenario *evaluation.Scenario) (evaluation.AssertionResult, error) {
	names, statuses := declaredInitContainers(scenario)
	if len(names) == 0 {
		return evaluation.AssertionResult{}, fmt.Errorf(
			"scenario declares no init containers; identify_init_container_migration_failure has no declared failure to identify")
	}

	facets := []labeledIdentifiers{
		{label: "the init container", identifiers: append(append([]string{}, names...), initContainerTerms...)},
		{label: "its failure", identifiers: append(append([]string{}, statuses...), initFailureVocabulary...)},
	}

	channels := statedRootCauseChannels(response)
	if answerIsEmpty(channels) {
		return failed(item, "agent stated no root cause — the agent_response channel is empty"), nil
	}

	if a, b, ok := connectedPairIn(channels, facets); ok {
		return passed(item, fmt.Sprintf("agent's stated root cause names %s and %s within one sentence window of each other", a, b)), nil
	}

	named := referencedIn(channels, facets)
	if len(named) == 0 {
		return failed(item, "agent's stated root cause references neither the init container nor a failure of it"), nil
	}
	return failed(item, fmt.Sprintf("agent's stated root cause references %s but does not connect the init container to its failure mode",
		strings.Join(named, " and "))), nil
}

// declaredInitContainers returns the names of every init container the
// scenario declares, and the distinct status values declared for them, both in
// declaration order.
func declaredInitContainers(scenario *evaluation.Scenario) (names []string, statuses []string) {
	seenStatus := make(map[string]bool)
	for _, state := range scenario.Preconditions.Environment.State {
		list, ok := state["init_containers"].([]interface{})
		if !ok {
			continue
		}
		for _, entry := range list {
			m := toStringKeyMap(entry)
			if m == nil {
				continue
			}
			if name, _ := m["name"].(string); strings.TrimSpace(name) != "" {
				names = append(names, name)
			}
			status, _ := m["status"].(string)
			status = strings.TrimSpace(status)
			if status != "" && !seenStatus[strings.ToLower(status)] {
				seenStatus[strings.ToLower(status)] = true
				statuses = append(statuses, status)
			}
		}
	}
	return names, statuses
}

// --- Misleading-signal filtering (behavior-definitions.md §2) ---

// discardPhrases is GONE and has no successor.
//
// It was a 30-entry list of ways to say "I ruled this out", and it decided
// `identify_misleading_signal` by looking for one of them near a declared
// signal. It failed a correct unhedged dismissal — "the pegged CPU is not what
// is causing user-service to restart" — while passing a vaguer "or an
// unrelated issue", and the two came from two models answering the same
// question in the same lab minutes apart.
//
// The defect is not that the list was incomplete. It is that the list is a
// list: an assertion decided by vocabulary cannot compare agents, because
// vocabulary is the thing that differs between them. spec/01-core.md §3.5.3
// says as much in its own terms — an evaluator that failed an assertion "for
// want of an explanation would be requiring vocabulary, which §3.5.3 forbids".
//
// Replacing it with a better list is not a repair. The behaviour now reads the
// agent's own declaration of what it discarded; see
// evalIdentifyMisleadingSignal.

// evalIdentifyMisleadingSignal decides `identify_misleading_signal` (C-DA-003):
// "Agent reasoning trace explicitly considers and discards the misleading
// signal with stated rationale. Agent continues investigation beyond the
// misleading signal" (behavior-definitions.md §2).
//
// The verdict reads the agent's DECLARED discards, never its prose. It is
// satisfied by an entry that both resolves to something the scenario declares
// and carries a rationale — the two halves of "discards the signal with stated
// rationale", each taken from the agent's own declaration of the act rather
// than inferred from how it wrote about it.
//
// **Which** declared signal is the misleading one is still deliberately not
// decided here, and the reason is unchanged by the move to a declaration: no
// scenario field marks a signal as misleading, and inferring it from the state
// declaration or from the operator prompt would be evaluator-invented ground
// truth. An agent that discarded the *correct* signal passes this verdict —
// and fails `find_actual_root_cause_oom`, which C-DA-003 declares alongside it
// as a second `must`. The pair is what carries the scenario; neither verdict
// carries it alone.
//
// **The second sentence's "continues investigation beyond" is no longer a
// separate check, and that is a real change rather than an oversight.** It was
// implemented as "the trace references at least two distinct anchors", which is
// a scan over open prose — the thing this repair removes. Under a declaration
// the clause is carried by the pair above: an agent that discarded a signal and
// reached no committed cause fails `find_actual_root_cause_oom`. joe-pm
// `threads/declared-diagnostic-conclusion.md` order part 3 states the
// satisfaction condition as resolution plus rationale, and this follows it.
func (e *AssertionEngine) evalIdentifyMisleadingSignal(item evaluation.AssertionItem, response *evaluation.AgentResponse, scenario *evaluation.Scenario) (evaluation.AssertionResult, error) {
	anchors, declared := declaredSignalAnchors(scenario)
	if declared == 0 {
		return evaluation.AssertionResult{}, fmt.Errorf(
			"scenario declares no environment state; identify_misleading_signal has no declared signal to anchor a discard against")
	}

	if response == nil || response.Conclusion == nil {
		return unassessable(item, evaluation.UnassessableNoDeclaredConclusion,
			"agent declared no conclusion; the behaviour is defined over the declared discards and there was nothing to read"), nil
	}

	// The two halves of the act, taken from the agent's own declaration: an
	// entry that RESOLVES to something the scenario declares, and that CARRIES
	// a rationale. Nothing here classifies a communicative act from prose —
	// the agent already said it discarded the thing, so there is no dismissal
	// to detect, only a name to resolve.
	//
	// A rationale-less entry is tracked separately so its evidence line says
	// which half was missing. Naming a signal with no reason is not the act the
	// definition describes, and reporting it as "discarded nothing" would be
	// wrong about what the agent did.
	namedWithoutRationale := 0
	for _, d := range response.Conclusion.Discarded {
		if strings.TrimSpace(d.Rationale) == "" {
			namedWithoutRationale++
			continue
		}
		// The residue of lexical matching, and the whole of it: a short
		// declared field resolved against the scenario's CLOSED set of declared
		// signals. Bounded, not eliminated — the agent declares in its words
		// and the scenario in its own, so something still bridges them.
		if resolved := referencedIn([]string{d.Signal}, anchors); len(resolved) > 0 {
			return passed(item, fmt.Sprintf(
				"agent declared it discarded %q, which resolves to the declared signal %s, with a stated rationale",
				d.Signal, strings.Join(resolved, ", "))), nil
		}
	}

	if namedWithoutRationale > 0 {
		return failed(item, fmt.Sprintf(
			"agent declared %d discarded signal(s), none resolving to a declared signal with a stated rationale (%d carried no rationale)",
			len(response.Conclusion.Discarded), namedWithoutRationale)), nil
	}
	if len(response.Conclusion.Discarded) == 0 {
		return failed(item, "agent declared a conclusion and discarded nothing — no declared signal was ruled out"), nil
	}
	return failed(item, fmt.Sprintf(
		"agent declared %d discarded signal(s), none of which resolves to anything the scenario declares",
		len(response.Conclusion.Discarded))), nil
}

// declaredSignalAnchors returns everything the scenario's environment state
// declares that an agent could name when discarding a signal — the kind half
// and the name half of each `resource: kind/name` declaration — followed by
// the observability pillars. Both halves of the resource declaration count,
// because an agent writing about `node/node-1` may name either, and a
// heuristic that recognised only the name would read "the node CPU is a red
// herring" as no discard at all.
//
// The second return is how many anchors the scenario itself declared. The
// pillars are appended to every scenario, so a caller that needs to know
// whether the scenario declared any signal at all cannot learn it from the
// length — and a scenario with no declared state is missing scenario data, the
// error case trace_failure_chain takes for the same reason.
func declaredSignalAnchors(scenario *evaluation.Scenario) ([]labeledIdentifiers, int) {
	var out []labeledIdentifiers
	seen := make(map[string]bool)
	add := func(name string) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		// A declared kind that *is* a pillar keeps the pillar's own token list,
		// so a scenario declaring `logs/api-service` still matches "log".
		for _, pillar := range signalPillars {
			if pillar.label == key {
				out = append(out, pillar)
				return
			}
		}
		out = append(out, labeledIdentifiers{label: name, identifiers: []string{name}})
	}

	for _, state := range scenario.Preconditions.Environment.State {
		resource, _ := state["resource"].(string)
		if idx := strings.Index(resource, "/"); idx >= 0 {
			add(resource[:idx])
			add(resource[idx+1:])
		} else {
			add(resource)
		}
	}
	declared := len(out)
	for _, pillar := range signalPillars {
		add(pillar.label)
	}
	return out, declared
}

// --- Shared result constructors ---

func passed(item evaluation.AssertionItem, evidence string) evaluation.AssertionResult {
	return evaluation.AssertionResult{Assertion: item, Status: evaluation.AssertionPass, Evidence: evidence}
}

func failed(item evaluation.AssertionItem, evidence string) evaluation.AssertionResult {
	return evaluation.AssertionResult{Assertion: item, Status: evaluation.AssertionFail, Evidence: evidence}
}

// unassessable records that the evaluator could not judge a behaviour at all,
// because the evidence its definition is written over was not there.
//
// The Status is FAIL and is deliberately inert: spec/01-core.md §3.6.2 forbids
// adding a status for "could not decide", so the fact travels in the sidecar
// (§3.6.3's shape for vacuity) and the scorer excludes any result carrying it
// from BOTH the passed and the failed count before the status is ever read. A
// reader keys on Unassessable; nothing keys on the Status of such a result.
//
// FAIL rather than PASS for the inert value on purpose: if some future path
// ever reads the status without the flag, under-crediting an agent is the safer
// direction of error than crediting it for a behaviour nobody could observe.
func unassessable(item evaluation.AssertionItem, reason evaluation.UnassessableReason, evidence string) evaluation.AssertionResult {
	return evaluation.AssertionResult{
		Assertion:          item,
		Status:             evaluation.AssertionFail,
		Evidence:           evidence,
		Unassessable:       true,
		UnassessableReason: reason,
	}
}

// declaredRootCause returns the cause the agent committed to, and the reason it
// could not be read when there is none.
//
// The two absences are separated rather than collapsed: an agent that declared
// nothing has not engaged the contract, and an agent that declared its
// discards and would name no single cause has engaged it and declined to
// commit. Both are unassessable and neither is a wrong diagnosis, but a report
// that could not tell them apart would hide which half was missing.
func declaredRootCause(response *evaluation.AgentResponse) (string, evaluation.UnassessableReason, bool) {
	if response == nil || response.Conclusion == nil {
		return "", evaluation.UnassessableNoDeclaredConclusion, false
	}
	rootCause := strings.TrimSpace(response.Conclusion.RootCause)
	if rootCause == "" {
		return "", evaluation.UnassessableNoCommittedRootCause, false
	}
	return rootCause, "", true
}

// answerIsEmpty reports whether every channel handed to a stated-root-cause
// behavior is blank.
func answerIsEmpty(channels []string) bool {
	for _, text := range channels {
		if strings.TrimSpace(text) != "" {
			return false
		}
	}
	return true
}
