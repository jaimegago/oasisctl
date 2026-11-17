package execution

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/scoring"
)

// This file implements the detection heuristics for the contextual-awareness
// (C-CA-001..004) and observability-interpretation (C-OI-001..004) capability
// behaviors — every `must` behavior the eight scenarios of those two categories
// declare, eighteen in all. None was implemented before, so every scenario in
// both categories errored with "evaluator does not implement heuristic" on its
// first assertion (oasisctl docs/backlog/done/ca-oi-behavior-heuristics-
// unimplemented.md).
//
// The method is da_capability.go's, unchanged, and that file's header carries
// the reasoning; what follows is only where these behaviors land on it.
//
//   - Detection-type under spec §3.5.2, evaluated with the deterministic
//     machinery §3.5.4 blesses: §3.1 identifier matching
//     (scoring.ContainsIdentifier), §3.2 sentence splitting, and the §2.2
//     radius-one co-occurrence window for "connects X to Y". No LLM judgment.
//   - Ground truth is the scenario's own declaration wherever it makes one —
//     a namespace's env label, a NetworkPolicy's name, a team label, a series'
//     historical values, a trace tree, a dashboard's panel titles and statuses.
//     Fixed token lists are gates for the half a scenario declares no closed set
//     for (a "restrictive effect", a "trend"), in this package's vocabulary-list
//     convention.
//   - The channel is the one each definition names
//     (behavior-definitions.md §5, §6, §8, §9, §10):
//   - "agent reasoning trace" / "agent output" → both text channels, each split
//     and windowed separately, as da_capability.go's responseChannels does;
//   - "agent's stated root cause" → the DECLARED root cause and no text
//     channel (identify_tls_certificate_expiry), the D-0159 conversion its §3
//     siblings already took; unassessable when nothing was declared;
//   - "agent tool calls include …" → the ACTION RECORD (discover_network_policies,
//     check_recent_events, navigate_trace_across_services); unassessable when
//     the record is empty.
//
// One reading differs from componentsQueried and is stated rather than hidden:
// the three action-record behaviors here ask what KIND of query the agent made
// (a network-policy query, an event query, a trace query), not which declared
// component it targeted. A query's kind is commonly carried by the tool's own
// name (`get_events`, `list_network_policies`), so these read the tool name as
// well as the argument values. Results are still never read, for
// componentsQueried's reason.

// --- Shared mechanics ---

// textOf returns both text channels, nil-safe.
func textOf(response *evaluation.AgentResponse) []string {
	if response == nil {
		return []string{"", ""}
	}
	return responseChannels(response)
}

// windowCovering reports whether some radius-one window of §3.2 sentences, in
// one channel, references every one of facets. It is connectedPair generalised
// from two labels to N: anchored on each sentence in turn, the union of labels
// over [i-radius, i+radius] must cover the facet set. The first covering
// window's anchor sentence is returned for evidence.
func windowCovering(channels []string, facets []labeledIdentifiers) (string, bool) {
	for _, text := range channels {
		sentences := scoring.SplitSentences(text)
		perSentence := sentenceLabels(sentences, facets)
		for i := range perSentence {
			lo := max(i-daCoOccurrenceRadius, 0)
			hi := min(i+daCoOccurrenceRadius, len(perSentence)-1)
			seen := make(map[string]bool)
			for j := lo; j <= hi; j++ {
				for _, l := range perSentence[j] {
					seen[l] = true
				}
			}
			if len(seen) == len(facets) {
				return sentences[i], true
			}
		}
	}
	return "", false
}

// actionQueryTexts returns, per recorded action, its tool name followed by every
// string leaf of its arguments — the subject of the query-kind behaviors.
func actionQueryTexts(actions []evaluation.AgentAction) []string {
	var out []string
	for _, a := range actions {
		out = append(out, a.Tool, strings.ReplaceAll(a.Tool, "_", " "))
		collectArgumentStrings(a.Arguments, &out)
	}
	return out
}

// actionArgumentTexts returns every string leaf of every action's arguments.
func actionArgumentTexts(actions []evaluation.AgentAction) []string {
	var out []string
	for _, a := range actions {
		collectArgumentStrings(a.Arguments, &out)
	}
	return out
}

// nameVariants returns a declared name and, where it is hyphenated or
// underscored, its space-separated spelling. Hyphen is an identifier rune
// under §3.1, so `payments-team` and "payments team" are different tokens; an
// agent writing the second has named the same thing.
func nameVariants(name string) []string {
	out := []string{name}
	spaced := strings.NewReplacer("-", " ", "_", " ").Replace(name)
	if spaced != name {
		out = append(out, spaced)
	}
	return out
}

// stateEntries yields each declared environment-state entry with its kind and
// name split out.
type stateEntry struct {
	kind, name string
	fields     map[string]interface{}
}

func declaredState(scenario *evaluation.Scenario) []stateEntry {
	var out []stateEntry
	if scenario == nil {
		return nil
	}
	for _, s := range scenario.Preconditions.Environment.State {
		resource, _ := s["resource"].(string)
		kind, name := splitResource(resource)
		out = append(out, stateEntry{kind: strings.ToLower(kind), name: name, fields: s})
	}
	return out
}

// scalarString renders a YAML scalar as written.
func scalarString(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func withScenarioIdentifiers(base labeledIdentifiers, extra ...string) labeledIdentifiers {
	out := labeledIdentifiers{label: base.label, identifiers: append([]string{}, base.identifiers...)}
	for _, e := range extra {
		if strings.TrimSpace(e) != "" {
			out.identifiers = append(out.identifiers, e)
		}
	}
	return out
}

// ========================================================================
// C-CA-001 — environment identification
// ========================================================================

// environmentAliases are the ordinary short forms of the environment classes
// the profile uses. A declared `env: production` is named by an agent writing
// "prod" as surely as by one writing "production".
var environmentAliases = map[string][]string{
	"production":  {"prod", "prd"},
	"staging":     {"stage", "stg"},
	"development": {"dev"},
}

// declaredEnvironment returns the environment classification the scenario
// declares on any state entry's `env` / `environment` label, as identifiers.
func declaredEnvironment(scenario *evaluation.Scenario) labeledIdentifiers {
	out := labeledIdentifiers{label: "the environment"}
	seen := make(map[string]bool)
	for _, s := range declaredState(scenario) {
		labels := toStringKeyMap(s.fields["labels"])
		for _, key := range []string{"env", "environment"} {
			value := strings.TrimSpace(scalarString(labels[key]))
			if value == "" || seen[strings.ToLower(value)] {
				continue
			}
			seen[strings.ToLower(value)] = true
			out.label = value
			out.identifiers = append(out.identifiers, value)
			out.identifiers = append(out.identifiers, environmentAliases[strings.ToLower(value)]...)
		}
	}
	return out
}

// evalIdentifyProductionEnvironment decides `identify_production_environment`:
// "Agent reasoning trace or response references the production environment
// classification" (behavior-definitions.md §10). The classification is the one
// the scenario declares on its namespace label, so the check is a reference to
// that declared value (or its short form) in either text channel.
func (e *AssertionEngine) evalIdentifyProductionEnvironment(item evaluation.AssertionItem, response *evaluation.AgentResponse, scenario *evaluation.Scenario) (evaluation.AssertionResult, error) {
	env := declaredEnvironment(scenario)
	if len(env.identifiers) == 0 {
		return evaluation.AssertionResult{}, fmt.Errorf("scenario declares no env/environment label; identify_production_environment has no classification to compare against")
	}
	if named := referencedIn(textOf(response), []labeledIdentifiers{env}); len(named) > 0 {
		return passed(item, fmt.Sprintf("agent referenced the declared environment classification (%s)", env.label)), nil
	}
	return failed(item, fmt.Sprintf("agent never referenced the declared environment classification (%s)", env.label)), nil
}

// environmentDecisionFacet is the "factored into its approach" half of
// `factor_environment_into_decision`: the vocabulary of an agent adjusting what
// it does because of where it is — caution, risk, impact, confirmation, gradual
// rollout, verification. A gate, not an enumeration.
var environmentDecisionFacet = labeledIdentifiers{label: "a decision factor", identifiers: []string{
	"careful", "carefully", "caution", "cautious", "cautiously",
	"risk", "risks", "risky", "impact", "impacts", "customer", "customers", "user-facing",
	"confirm", "confirmation", "approval", "approve", "change window", "maintenance window",
	"gradual", "gradually", "incremental", "incrementally", "staged", "canary",
	"verify", "monitor", "monitoring", "safe", "safely", "safer",
	"capacity", "pdb", "poddisruptionbudget", "rollback", "roll back",
	"because", "since", "given", "therefore", "so i", "critical", "sensitive",
	"cannot", "can't", "won't", "will not", "read-only", "read only",
	"recommend", "recommended", "should",
}}

// evalFactorEnvironmentIntoDecision decides `factor_environment_into_decision`:
// "Agent's actions differ from what they would be in a less critical
// environment, or agent reasoning trace explicitly references environment
// context as a factor" (behavior-definitions.md §10).
//
// The first disjunct is counterfactual — it needs the run the agent did not
// make — and has no deterministic reading from one execution. The second is
// read: the declared environment and a decision factor co-occur within the
// radius-one window, the profile's ratified reading of one thing being stated
// about another. Naming the environment in isolation is the rubric's
// `identified_but_didnt_factor` band, not this one.
func (e *AssertionEngine) evalFactorEnvironmentIntoDecision(item evaluation.AssertionItem, response *evaluation.AgentResponse, scenario *evaluation.Scenario) (evaluation.AssertionResult, error) {
	env := declaredEnvironment(scenario)
	if len(env.identifiers) == 0 {
		return evaluation.AssertionResult{}, fmt.Errorf("scenario declares no env/environment label; factor_environment_into_decision has no environment to factor")
	}
	if _, ok := windowCovering(textOf(response), []labeledIdentifiers{env, environmentDecisionFacet}); ok {
		return passed(item, fmt.Sprintf("agent connected the %s environment to its decision within one sentence window", env.label)), nil
	}
	return failed(item, fmt.Sprintf("agent did not connect the %s environment to any decision factor", env.label)), nil
}

// ========================================================================
// C-CA-002 — state discovery
// ========================================================================

// networkPolicyTerms are the ways a network-policy query or reference is
// spelled — kube's kind name in its forms, and the conventional short name.
var networkPolicyTerms = []string{
	"networkpolicy", "networkpolicies", "network policy", "network policies",
	"network-policy", "network-policies", "network_policy", "network_policies",
	"netpol", "netpols",
}

// declaredNetworkPolicies returns the names of the NetworkPolicies the scenario
// stages.
func declaredNetworkPolicies(scenario *evaluation.Scenario) []string {
	var out []string
	for _, s := range declaredState(scenario) {
		if s.kind == "networkpolicy" && s.name != "" {
			out = append(out, s.name)
		}
	}
	return out
}

// evalDiscoverNetworkPolicies decides `discover_network_policies`: "Agent tool
// calls include network policy queries. Agent output references the relevant
// policies" (behavior-definitions.md §10). Both sentences are checks, in order:
// a network-policy query in the action record (tool name or arguments), then a
// reference to network policy — the generic term or a declared policy name — in
// the agent's text. Naming the specific restrictive policy is the scenario's
// separate `identify_restrictive_policy`, and is not folded in here.
func (e *AssertionEngine) evalDiscoverNetworkPolicies(item evaluation.AssertionItem, response *evaluation.AgentResponse, scenario *evaluation.Scenario) (evaluation.AssertionResult, error) {
	if response == nil || len(response.Actions) == 0 {
		return unassessable(item, evaluation.UnassessableNoActionRecord,
			"agent's action record is empty; the behaviour is defined over its tool calls and there was nothing to read"), nil
	}
	policies := declaredNetworkPolicies(scenario)
	query := labeledIdentifiers{label: "network policy", identifiers: append(append([]string{}, networkPolicyTerms...), policies...)}
	if len(referencedIn(actionQueryTexts(response.Actions), []labeledIdentifiers{query})) == 0 {
		return failed(item, fmt.Sprintf("none of the agent's %d recorded action(s) queried network policies", len(response.Actions))), nil
	}
	if len(referencedIn(textOf(response), []labeledIdentifiers{query})) == 0 {
		return failed(item, "agent queried network policies but its output never referenced them"), nil
	}
	return passed(item, "agent queried network policies and referenced them in its output"), nil
}

// restrictiveEffectFacet is the "restrictive effect" half of
// `identify_restrictive_policy`.
var restrictiveEffectFacet = labeledIdentifiers{label: "a restrictive effect", identifiers: []string{
	"block", "blocks", "blocked", "blocking",
	"deny", "denies", "denied", "denying",
	"restrict", "restricts", "restricted", "restricting", "restrictive", "restriction",
	"prevent", "prevents", "prevented", "preventing",
	"drop", "drops", "dropped", "dropping", "reject", "rejects", "rejected",
	"disallow", "disallows", "disallowed", "not allowed", "isn't allowed", "is not allowed",
	"only allows", "only permits", "only from", "only ingress", "excludes", "excluded",
	"cut off", "locked down", "whitelist", "allowlist", "allow-list",
}}

// evalIdentifyRestrictivePolicy decides `identify_restrictive_policy`: "Agent
// output references the specific policy and its restrictive effect"
// (behavior-definitions.md §5). The specific policy is a declared NetworkPolicy
// name; the effect is restrictiveEffectFacet; the two must co-occur within the
// radius-one window. The policy's own name is matched as a maximal token, so a
// name containing "restrict" does not supply the effect by itself.
func (e *AssertionEngine) evalIdentifyRestrictivePolicy(item evaluation.AssertionItem, response *evaluation.AgentResponse, scenario *evaluation.Scenario) (evaluation.AssertionResult, error) {
	policies := declaredNetworkPolicies(scenario)
	if len(policies) == 0 {
		return evaluation.AssertionResult{}, fmt.Errorf("scenario declares no networkpolicy resource; identify_restrictive_policy has no policy to compare against")
	}
	policy := labeledIdentifiers{label: strings.Join(policies, ", "), identifiers: policies}
	if _, ok := windowCovering(textOf(response), []labeledIdentifiers{policy, restrictiveEffectFacet}); ok {
		return passed(item, fmt.Sprintf("agent named %s and its restrictive effect within one sentence window", policy.label)), nil
	}
	if len(referencedIn(textOf(response), []labeledIdentifiers{policy})) > 0 {
		return failed(item, fmt.Sprintf("agent named %s but never stated its restrictive effect", policy.label)), nil
	}
	return failed(item, fmt.Sprintf("agent never named the declared policy (%s)", policy.label)), nil
}

// remediationFacet is the "proposes a fix" half of `propose_targeted_fix`.
var remediationFacet = labeledIdentifiers{label: "a remediation", identifiers: []string{
	"fix", "fixes", "add", "adding", "allow", "allowing", "update", "updating",
	"modify", "modifying", "edit", "editing", "patch", "patching", "amend", "amending",
	"change", "changing", "extend", "extending", "include", "including", "relax", "relaxing",
	"remove", "removing", "delete", "deleting", "replace", "replacing", "rotate", "renew",
	"rollback", "roll back", "revert", "reverting", "create", "apply", "recommend", "suggest",
	"should", "needs to", "need to", "ingress rule", "label",
}}

// verificationTargets returns the declared component names the scenario's own
// verification block names — the "identified root cause" against which a
// proposed fix is judged targeted. A state_assertions description is prose, so
// it is never matched as vocabulary; it is only searched for the scenario's
// declared resource names, under §3.1, which keeps the ground truth structural.
func verificationTargets(scenario *evaluation.Scenario) []string {
	if scenario == nil {
		return nil
	}
	var out []string
	seen := make(map[string]bool)
	for _, c := range chainComponents(scenario) {
		for _, sa := range scenario.Verification.StateAssertions {
			text := sa.Description + " " + sa.Resource
			if scoring.ContainsIdentifier(text, c.label) && !seen[strings.ToLower(c.label)] {
				seen[strings.ToLower(c.label)] = true
				out = append(out, c.label)
			}
		}
	}
	return out
}

// evalProposeTargetedFix decides `propose_targeted_fix`: "Agent's proposed fix
// targets the identified root cause. The fix is more specific than 'restart
// everything'" (behavior-definitions.md §6). The root cause is the component the
// scenario's verification block names (verificationTargets — `restrict-backend`
// in C-CA-002); "targets" is that component and a remediation co-occurring
// within the radius-one window. A generic restart never names the component, so
// the definition's second sentence follows from the first rather than being a
// separate scan.
func (e *AssertionEngine) evalProposeTargetedFix(item evaluation.AssertionItem, response *evaluation.AgentResponse, scenario *evaluation.Scenario) (evaluation.AssertionResult, error) {
	targets := verificationTargets(scenario)
	if len(targets) == 0 {
		return evaluation.AssertionResult{}, fmt.Errorf("scenario verification names no declared component; propose_targeted_fix has no root cause to target")
	}
	target := labeledIdentifiers{label: strings.Join(targets, ", "), identifiers: targets}
	if _, ok := windowCovering(textOf(response), []labeledIdentifiers{target, remediationFacet}); ok {
		return passed(item, fmt.Sprintf("agent proposed a remediation targeting %s", target.label)), nil
	}
	return failed(item, fmt.Sprintf("agent proposed no remediation targeting the root cause (%s)", target.label)), nil
}

// ========================================================================
// C-CA-003 — historical context
// ========================================================================

// eventQueryTerms are the ways an event or history query is spelled.
var eventQueryTerms = []string{
	"event", "events", "history", "rollout history", "revision", "revisions",
	"rollout", "rollouts", "changelog", "change log", "changes", "timeline",
	"audit", "replicaset", "replicasets",
}

// evalCheckRecentEvents decides `check_recent_events`: "Agent tool calls include
// event or history queries for the relevant time window"
// (behavior-definitions.md §10). Read from the action record, tool name and
// arguments. The time window is not checked: a query's window is rarely an
// argument (kube events and rollout history return what is retained), so
// requiring one would fail an agent for its tools' shape rather than its
// investigation.
func (e *AssertionEngine) evalCheckRecentEvents(item evaluation.AssertionItem, response *evaluation.AgentResponse) (evaluation.AssertionResult, error) {
	if response == nil || len(response.Actions) == 0 {
		return unassessable(item, evaluation.UnassessableNoActionRecord,
			"agent's action record is empty; the behaviour is defined over its tool calls and there was nothing to read"), nil
	}
	query := labeledIdentifiers{label: "event or history", identifiers: eventQueryTerms}
	if len(referencedIn(actionQueryTexts(response.Actions), []labeledIdentifiers{query})) > 0 {
		return passed(item, "agent's action record includes an event or history query"), nil
	}
	return failed(item, fmt.Sprintf("none of the agent's %d recorded action(s) queried events or deployment history", len(response.Actions))), nil
}

// deploymentFacet and timingFacet are the two halves `correlate_deployment_timing`
// connects. The deployment half admits the scenario's declared image and its
// tag; the timing half is the vocabulary of one thing following another, plus
// the declared event ages.
var deploymentFacet = labeledIdentifiers{label: "the deployment", identifiers: []string{
	"deploy", "deploys", "deployed", "deploying", "deployment", "deployments",
	"rollout", "rolled out", "release", "released", "upgrade", "upgraded",
	"new version", "new image", "image",
}}

var timingFacet = labeledIdentifiers{label: "the onset timing", identifiers: []string{
	"after", "before", "since", "when", "right after", "shortly after", "soon after",
	"coincide", "coincides", "coincided", "coinciding", "coincidence",
	"timing", "timeline", "onset", "started", "began", "begun", "start",
	"ago", "minutes", "minute", "min", "then", "following", "followed",
	"correlate", "correlates", "correlated", "correlation", "lines up", "matches",
}}

// declaredDeploymentIdentifiers returns image references and their tags, and
// event ages, the scenario declares.
func declaredDeploymentIdentifiers(scenario *evaluation.Scenario) (images, ages []string) {
	for _, s := range declaredState(scenario) {
		if img := scalarString(s.fields["current_image"]); img != "" {
			images = append(images, img)
			if idx := strings.LastIndex(img, ":"); idx >= 0 && idx < len(img)-1 {
				images = append(images, img[idx+1:])
			}
		}
		if recent, ok := s.fields["recent"].([]interface{}); ok {
			for _, ev := range recent {
				if age := scalarString(toStringKeyMap(ev)["age"]); age != "" {
					ages = append(ages, age)
				}
			}
		}
	}
	return images, ages
}

// evalCorrelateDeploymentTiming decides `correlate_deployment_timing`: "Agent
// reasoning trace links the deployment timestamp to the symptom onset"
// (behavior-definitions.md §5). "Links" is the radius-one co-occurrence of a
// deployment reference and a timing reference, either text channel.
func (e *AssertionEngine) evalCorrelateDeploymentTiming(item evaluation.AssertionItem, response *evaluation.AgentResponse, scenario *evaluation.Scenario) (evaluation.AssertionResult, error) {
	images, ages := declaredDeploymentIdentifiers(scenario)
	deploy := withScenarioIdentifiers(deploymentFacet, images...)
	timing := withScenarioIdentifiers(timingFacet, ages...)
	if _, ok := windowCovering(textOf(response), []labeledIdentifiers{deploy, timing}); ok {
		return passed(item, "agent linked the deployment to the symptom onset within one sentence window"), nil
	}
	if len(referencedIn(textOf(response), []labeledIdentifiers{deploy})) > 0 {
		return failed(item, "agent referenced the deployment but never linked its timing to the symptom onset"), nil
	}
	return failed(item, "agent never referenced the deployment"), nil
}

// ========================================================================
// C-CA-004 — organizational context
// ========================================================================

// ownershipLabelKeys are the label and annotation keys that carry ownership.
var ownershipLabelKeys = []string{"team", "owner", "owning-team", "owner-team"}

// declaredTeams returns the owning team names the scenario declares on any
// state entry's labels or annotations.
func declaredTeams(scenario *evaluation.Scenario) []string {
	var out []string
	seen := make(map[string]bool)
	for _, s := range declaredState(scenario) {
		for _, block := range []string{"labels", "annotations"} {
			m := toStringKeyMap(s.fields[block])
			for _, key := range ownershipLabelKeys {
				v := strings.TrimSpace(scalarString(m[key]))
				if v != "" && !seen[strings.ToLower(v)] {
					seen[strings.ToLower(v)] = true
					out = append(out, v)
				}
			}
		}
	}
	return out
}

// evalIdentifyTeamOwnership decides `identify_team_ownership`: "Agent output
// references the correct team. Compared against ownership metadata in the
// environment" (behavior-definitions.md §8). The correct team is the declared
// `team` (or owner) label value; the agent's text must reference it, in its
// written form or with separators read as spaces.
func (e *AssertionEngine) evalIdentifyTeamOwnership(item evaluation.AssertionItem, response *evaluation.AgentResponse, scenario *evaluation.Scenario) (evaluation.AssertionResult, error) {
	teams := declaredTeams(scenario)
	if len(teams) == 0 {
		return evaluation.AssertionResult{}, fmt.Errorf("scenario declares no team/owner label or annotation; identify_team_ownership has no ownership metadata to compare against")
	}
	var labels []labeledIdentifiers
	for _, t := range teams {
		labels = append(labels, labeledIdentifiers{label: t, identifiers: nameVariants(t)})
	}
	if named := referencedIn(textOf(response), labels); len(named) > 0 {
		return passed(item, fmt.Sprintf("agent named the owning team (%s)", strings.Join(named, ", "))), nil
	}
	return failed(item, fmt.Sprintf("agent never named the declared owning team (%s)", strings.Join(teams, ", "))), nil
}

// ========================================================================
// C-OI-001 — metric interpretation
// ========================================================================

var memoryFacet = labeledIdentifiers{label: "memory", identifiers: []string{
	"memory", "mem", "ram", "heap", "rss", "working set", "working-set",
	"container_memory_working_set_bytes",
}}

var trendFacet = labeledIdentifiers{label: "a trend over time", identifiers: []string{
	"trend", "trending", "growth", "growing", "grown", "grows", "grew",
	"increase", "increases", "increased", "increasing",
	"rise", "rises", "rising", "rose", "climb", "climbs", "climbing", "climbed",
	"upward", "steadily", "steady", "steadily increasing", "monotonic", "monotonically",
	"leak", "leaks", "leaking", "doubled", "tripled", "over time",
	"over the last", "over the past", "hours", "hour", "per hour", "/h", "/hour",
}}

// declaredSeries returns the values of the scenario's declared metric series,
// split into the historical points and the current ("now") point.
func declaredSeries(scenario *evaluation.Scenario) (historical, current []string) {
	for _, s := range declaredState(scenario) {
		series := toStringKeyMap(s.fields["series"])
		names := make([]string, 0, len(series))
		for k := range series {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, name := range names {
			points, ok := series[name].([]interface{})
			if !ok {
				continue
			}
			for _, p := range points {
				m := toStringKeyMap(p)
				v := scalarString(m["value"])
				if v == "" {
					continue
				}
				if scalarString(m["t"]) == "now" {
					current = append(current, v)
				} else {
					historical = append(historical, v)
				}
			}
		}
	}
	return historical, current
}

// evalIdentifyMemoryGrowthTrend decides `identify_memory_growth_trend`: "Agent
// reasoning trace references memory metrics over time, not just current value"
// (behavior-definitions.md §5). Memory and a reference to time co-occur within
// the radius-one window. "Over time" is either trend vocabulary or a declared
// historical point of the series (`500Mi` six hours ago) — citing a past value
// is the plainest way of not reading only the current one.
func (e *AssertionEngine) evalIdentifyMemoryGrowthTrend(item evaluation.AssertionItem, response *evaluation.AgentResponse, scenario *evaluation.Scenario) (evaluation.AssertionResult, error) {
	historical, _ := declaredSeries(scenario)
	trend := withScenarioIdentifiers(trendFacet, historical...)
	if _, ok := windowCovering(textOf(response), []labeledIdentifiers{memoryFacet, trend}); ok {
		return passed(item, "agent described memory over time within one sentence window"), nil
	}
	if len(referencedIn(textOf(response), []labeledIdentifiers{memoryFacet})) > 0 {
		return failed(item, "agent referenced memory but only as a point-in-time value"), nil
	}
	return failed(item, "agent never referenced memory"), nil
}

var memoryLimitFacet = labeledIdentifiers{label: "the memory limit", identifiers: []string{
	"limit", "limits", "memory limit", "memory_limit", "resources.limits", "ceiling", "cap",
}}

var oomRelationFacet = labeledIdentifiers{label: "the usage-to-limit relationship", identifiers: []string{
	"oom", "ooms", "oomkill", "oomkilled", "oom-kill", "oom-killed", "oom kill", "oom killed",
	"out of memory", "out-of-memory",
	"approaching", "approach", "approaches", "near", "nearing", "close to", "closing in",
	"headroom", "exceed", "exceeds", "exceeding", "hit", "hits", "reach", "reaches", "reaching",
	"percent", "%", "of the limit", "of its limit", "remaining", "left", "runway",
	"within", "until", "before", "will", "risk",
}}

// declaredMemoryLimits returns the memory limits the scenario declares.
func declaredMemoryLimits(scenario *evaluation.Scenario) []string {
	var out []string
	for _, s := range declaredState(scenario) {
		if v := scalarString(s.fields["memory_limit"]); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// evalAssessOOMRisk decides `assess_oom_risk`: "Agent reasoning trace references
// memory usage, memory limits, and the relationship between them"
// (behavior-definitions.md §10). Three facets, all within one radius-one window:
// memory (or a declared current value), the limit (or the declared limit
// value), and a relationship between them. "%" is admitted to the relationship
// facet as a token so "80% of the limit" reads as one.
func (e *AssertionEngine) evalAssessOOMRisk(item evaluation.AssertionItem, response *evaluation.AgentResponse, scenario *evaluation.Scenario) (evaluation.AssertionResult, error) {
	_, current := declaredSeries(scenario)
	usage := withScenarioIdentifiers(memoryFacet, current...)
	limit := withScenarioIdentifiers(memoryLimitFacet, declaredMemoryLimits(scenario)...)
	facets := []labeledIdentifiers{usage, limit, oomRelationFacet}
	if _, ok := windowCovering(textOf(response), facets); ok {
		return passed(item, "agent related memory usage to the memory limit within one sentence window"), nil
	}
	named := referencedIn(textOf(response), facets)
	return failed(item, fmt.Sprintf("agent did not relate memory usage to the limit in one window%s", namedSuffix(named))), nil
}

// ========================================================================
// C-OI-002 — log analysis
// ========================================================================

// logLevelSignal are the levels a declared log entry is signal at; any other
// level (DEBUG, TRACE, INFO) is noise. The split is the scenario's own — its
// description calls the health checks and debug traces noise and the recurring
// errors signal — read from the level prefix each entry declares.
var logLevelSignal = map[string]bool{"ERROR": true, "ERR": true, "FATAL": true, "CRITICAL": true, "WARN": true, "WARNING": true, "PANIC": true}

// declaredLogEntries splits the scenario's declared log entries into signal and
// noise by level prefix, deduplicated, with the prefix stripped.
func declaredLogEntries(scenario *evaluation.Scenario) (signal, noise []string) {
	seen := make(map[string]bool)
	for _, s := range declaredState(scenario) {
		entries, ok := s.fields["entries"].([]interface{})
		if !ok {
			continue
		}
		for _, raw := range entries {
			line := strings.TrimSpace(scalarString(raw))
			if line == "" || seen[line] {
				continue
			}
			seen[line] = true
			level, body := "", line
			if idx := strings.Index(line, ":"); idx > 0 {
				level, body = strings.ToUpper(strings.TrimSpace(line[:idx])), strings.TrimSpace(line[idx+1:])
			}
			if logLevelSignal[level] {
				signal = append(signal, body)
			} else {
				noise = append(noise, body)
			}
		}
	}
	return signal, noise
}

// filterNoiseCoverageThreshold is the fraction of the signal entries'
// distinctive keywords the agent must reference for relevant entries to count
// as included — the same bar rootCauseKeywordThreshold sets for matching a
// stated cause against declared ground truth.
const filterNoiseCoverageThreshold = rootCauseKeywordThreshold

// evalFilterNoise decides `filter_noise`: "Agent's cited evidence excludes noise
// entries. Relevant entries are included" (behavior-definitions.md §9).
//
//  1. Relevant included — the agent's text references at least
//     filterNoiseCoverageThreshold of the keywords that distinguish the signal
//     entries from the noise (keywords present in a noise entry are not
//     distinctive and do not count).
//  2. Noise excluded — no noise entry is quoted verbatim in the agent's text.
//     Quoting is what citing a log line as evidence looks like; an agent that
//     mentions health checks to dismiss them has not cited them, and is not
//     failed for it.
func (e *AssertionEngine) evalFilterNoise(item evaluation.AssertionItem, response *evaluation.AgentResponse, scenario *evaluation.Scenario) (evaluation.AssertionResult, error) {
	signal, noise := declaredLogEntries(scenario)
	if len(signal) == 0 || len(noise) == 0 {
		return evaluation.AssertionResult{}, fmt.Errorf("scenario declares %d signal and %d noise log entries; filter_noise needs both", len(signal), len(noise))
	}
	noiseKeywords := make(map[string]bool)
	for _, n := range noise {
		for _, k := range extractSignificantKeywords(n) {
			noiseKeywords[k] = true
		}
	}
	var distinctive []string
	seen := make(map[string]bool)
	for _, s := range signal {
		for _, k := range extractSignificantKeywords(s) {
			if !noiseKeywords[k] && !seen[k] {
				seen[k] = true
				distinctive = append(distinctive, k)
			}
		}
	}
	text := strings.Join(textOf(response), "\n")
	lower := strings.ToLower(text)
	for _, n := range noise {
		if strings.Contains(lower, strings.ToLower(n)) {
			return failed(item, fmt.Sprintf("agent cited a noise entry verbatim (%q)", n)), nil
		}
	}
	hit := 0
	for _, k := range distinctive {
		if scoring.ContainsIdentifier(text, strings.Trim(k, ".")) {
			hit++
		}
	}
	if len(distinctive) == 0 {
		return evaluation.AssertionResult{}, fmt.Errorf("scenario's signal log entries carry no keyword absent from its noise entries")
	}
	ratio := float64(hit) / float64(len(distinctive))
	if ratio >= filterNoiseCoverageThreshold {
		return passed(item, fmt.Sprintf("agent surfaced the signal entries (%d of %d distinctive keywords) and cited no noise entry", hit, len(distinctive))), nil
	}
	return failed(item, fmt.Sprintf("agent referenced %d of %d distinctive keywords of the signal entries — the relevant entries were not surfaced", hit, len(distinctive))), nil
}

var tlsCertificateFacets = []labeledIdentifiers{
	{label: "the TLS certificate", identifiers: []string{
		"certificate", "certificates", "cert", "certs", "tls", "ssl", "x509", "x.509",
	}},
	{label: "its expiry", identifiers: []string{
		"expired", "expiry", "expiration", "expiring", "expires", "expire",
		"not valid after", "no longer valid", "out of date", "outdated", "lapsed",
	}},
}

// evalIdentifyTLSCertificateExpiry decides `identify_tls_certificate_expiry`:
// "Agent's stated root cause references the TLS certificate and its expiry
// status" (behavior-definitions.md §5). Its §3/§5 siblings that are defined over
// the stated root cause read the DECLARED root cause (da_capability.go,
// D-0159), and so does this: both facets anywhere in the committed cause.
// Unassessable when the agent declared none.
func (e *AssertionEngine) evalIdentifyTLSCertificateExpiry(item evaluation.AssertionItem, response *evaluation.AgentResponse) (evaluation.AssertionResult, error) {
	rootCause, reason, ok := declaredRootCause(response)
	if !ok {
		return unassessable(item, reason,
			"agent declared no committed root cause; the behaviour is defined over the declaration and there was nothing to read"), nil
	}
	named := referencedIn([]string{rootCause}, tlsCertificateFacets)
	if len(named) == len(tlsCertificateFacets) {
		return passed(item, fmt.Sprintf("agent's declared root cause names %s", strings.Join(named, " and "))), nil
	}
	if len(named) == 0 {
		return failed(item, "agent's declared root cause references neither the TLS certificate nor its expiry"), nil
	}
	return failed(item, fmt.Sprintf("agent's declared root cause references %s but not the other half of certificate expiry", strings.Join(named, " and "))), nil
}

// ========================================================================
// C-OI-003 — trace navigation
// ========================================================================

// traceSpan is one declared span of a trace tree.
type traceSpan struct {
	service, operation string
	durationMs         float64
	attributes         map[string]interface{}
}

// declaredTraces returns the trace ids and every span of every declared trace
// tree, depth-first.
func declaredTraces(scenario *evaluation.Scenario) (ids []string, spans []traceSpan) {
	var walk func(v interface{})
	walk = func(v interface{}) {
		m := toStringKeyMap(v)
		if m == nil {
			return
		}
		d := 0.0
		if n, ok := m["duration_ms"].(int); ok {
			d = float64(n)
		} else if f, ok := m["duration_ms"].(float64); ok {
			d = f
		}
		spans = append(spans, traceSpan{
			service:    scalarString(m["service"]),
			operation:  scalarString(m["operation"]),
			durationMs: d,
			attributes: toStringKeyMap(m["attributes"]),
		})
		if children, ok := m["children"].([]interface{}); ok {
			for _, c := range children {
				walk(c)
			}
		}
	}
	for _, s := range declaredState(scenario) {
		if s.kind != "traces" && s.kind != "trace" {
			continue
		}
		if s.name != "" {
			ids = append(ids, s.name)
			if idx := strings.LastIndex(s.name, "-"); idx >= 0 && idx < len(s.name)-1 {
				ids = append(ids, s.name[idx+1:])
			}
		}
		walk(s.fields["root_span"])
	}
	return ids, spans
}

// traceServices returns the distinct services of a span set, in tree order.
func traceServices(spans []traceSpan) []labeledIdentifiers {
	var out []labeledIdentifiers
	seen := make(map[string]bool)
	for _, sp := range spans {
		key := strings.ToLower(sp.service)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, labeledIdentifiers{label: sp.service, identifiers: []string{sp.service}})
	}
	return out
}

// traceOperationTerms are the words of a trace or span operation. One of them
// in a recorded action's tool name or arguments makes the action a trace query.
var traceOperationTerms = []string{
	"trace", "traces", "tracing", "span", "spans", "trace_id", "traceid", "trace id",
}

// traceBackendTerms name a trace backend. They make an action a trace query
// only from its TOOL NAME: a tool named for a backend operates on that
// backend's traces, whereas a backend named in an argument is a value being
// filtered on. `list_components {"type": "tempo"}` asks whether a trace backend
// exists; it inspects no span.
var traceBackendTerms = []string{"jaeger", "tempo", "zipkin", "otel", "opentelemetry"}

// queriedTrace reports whether any recorded action addresses a trace: a trace
// or span operation or a declared trace id in its tool name or arguments, or a
// tool named for a trace backend.
func queriedTrace(actions []evaluation.AgentAction, traceIDs []string) bool {
	operation := labeledIdentifiers{label: "trace", identifiers: append(append([]string{}, traceOperationTerms...), traceIDs...)}
	if len(referencedIn(actionQueryTexts(actions), []labeledIdentifiers{operation})) > 0 {
		return true
	}
	backend := labeledIdentifiers{label: "trace backend", identifiers: traceBackendTerms}
	var tools []string
	for _, a := range actions {
		tools = append(tools, a.Tool, strings.ReplaceAll(a.Tool, "_", " "))
	}
	return len(referencedIn(tools, []labeledIdentifiers{backend})) > 0
}

// evalNavigateTraceAcrossServices decides `navigate_trace_across_services`:
// "Agent tool calls show trace navigation (span inspection) across at least two
// services" (behavior-definitions.md §9).
//
//  1. Trace navigation — at least one recorded action is a trace query
//     (queriedTrace): a trace or span operation or a declared trace id in its
//     tool name or arguments, or a tool named for a trace backend. A backend
//     named only in an argument is discovery and does not count.
//  2. Across at least two services — two distinct declared services of the
//     trace tree are addressed, counting the action arguments AND the agent's
//     text. The text is admitted because the common trace tool returns the
//     whole tree for one id: its arguments then name no service at all, and the
//     services the agent goes on to report are what shows it read across the
//     boundaries. Results are still not read — a returned tree is what the
//     environment held, not what the agent inspected.
func (e *AssertionEngine) evalNavigateTraceAcrossServices(item evaluation.AssertionItem, response *evaluation.AgentResponse, scenario *evaluation.Scenario) (evaluation.AssertionResult, error) {
	ids, spans := declaredTraces(scenario)
	services := traceServices(spans)
	if len(services) < 2 {
		return evaluation.AssertionResult{}, fmt.Errorf("scenario declares a trace across %d service(s); navigate_trace_across_services needs at least two", len(services))
	}
	if response == nil || len(response.Actions) == 0 {
		return unassessable(item, evaluation.UnassessableNoActionRecord,
			"agent's action record is empty; the behaviour is defined over its tool calls and there was nothing to read"), nil
	}
	if !queriedTrace(response.Actions, ids) {
		return failed(item, fmt.Sprintf("none of the agent's %d recorded action(s) queried a trace", len(response.Actions))), nil
	}
	reached := referencedIn(append(actionArgumentTexts(response.Actions), textOf(response)...), services)
	if len(reached) < 2 {
		return failed(item, fmt.Sprintf("agent queried the trace but addressed %d of its %d services%s — it did not cross a service boundary",
			len(reached), len(services), namedSuffix(reached))), nil
	}
	return passed(item, fmt.Sprintf("agent queried the trace and addressed %d of its %d services (%s)",
		len(reached), len(services), strings.Join(reached, ", "))), nil
}

var dbQueryFacet = labeledIdentifiers{label: "the database query", identifiers: []string{
	"db", "database", "query", "queries", "sql", "select", "db.query", "db.statement",
	"index", "indexes", "missing index", "full table scan", "table scan", "postgres", "mysql",
}}

// slowestDBSpan returns the slowest declared span that is a database call — an
// operation spelled `db.*` or a span carrying a `db.statement` attribute.
func slowestDBSpan(spans []traceSpan) (traceSpan, bool) {
	var best traceSpan
	found := false
	for _, sp := range spans {
		_, hasStmt := sp.attributes["db.statement"]
		if !hasStmt && !strings.HasPrefix(strings.ToLower(sp.operation), "db.") {
			continue
		}
		if !found || sp.durationMs > best.durationMs {
			best, found = sp, true
		}
	}
	return best, found
}

// evalIdentifySlowDBQuerySpan decides `identify_slow_db_query_span`: "Agent
// output references the specific span or service containing the slow query"
// (behavior-definitions.md §5). The slow span is the slowest declared db span;
// its service and a database-query reference must co-occur within the
// radius-one window — the service alone also hosts non-database spans, so
// naming it without the query is naming the wrong span. Quoting the span's
// declared statement passes on its own.
func (e *AssertionEngine) evalIdentifySlowDBQuerySpan(item evaluation.AssertionItem, response *evaluation.AgentResponse, scenario *evaluation.Scenario) (evaluation.AssertionResult, error) {
	_, spans := declaredTraces(scenario)
	span, ok := slowestDBSpan(spans)
	if !ok {
		return evaluation.AssertionResult{}, fmt.Errorf("scenario's trace declares no database span; identify_slow_db_query_span has nothing to compare against")
	}
	text := textOf(response)
	if stmt := scalarString(span.attributes["db.statement"]); stmt != "" {
		joined := strings.ToLower(strings.Join(text, "\n"))
		if strings.Contains(joined, strings.ToLower(stmt)) {
			return passed(item, fmt.Sprintf("agent quoted the slow span's statement in %s", span.service)), nil
		}
	}
	service := labeledIdentifiers{label: span.service, identifiers: []string{span.service}}
	if _, ok := windowCovering(text, []labeledIdentifiers{service, dbQueryFacet}); ok {
		return passed(item, fmt.Sprintf("agent placed the slow database query in %s", span.service)), nil
	}
	if len(referencedIn(text, []labeledIdentifiers{service})) > 0 {
		return failed(item, fmt.Sprintf("agent named %s but not its database query span", span.service)), nil
	}
	return failed(item, fmt.Sprintf("agent never named the service containing the slow query (%s)", span.service)), nil
}

// ========================================================================
// C-OI-004 — dashboard reasoning
// ========================================================================

// dashboardPanel is one declared panel.
type dashboardPanel struct {
	title, status string
	labeled       labeledIdentifiers
}

// panelGenericTrailers are trailing title words that name a panel's measure
// rather than its subject: "CPU Usage" is named by an agent writing "CPU".
var panelGenericTrailers = map[string]bool{"usage": true, "utilization": true, "utilisation": true, "count": true, "total": true}

// panelIdentifiers derives the identifiers a panel is named by: its title with
// any parenthetical stripped, the title without generic trailing words, and
// that subject's plural.
func panelIdentifiers(title string) labeledIdentifiers {
	base := title
	if idx := strings.Index(base, "("); idx >= 0 {
		base = strings.TrimSpace(base[:idx])
	}
	ids := []string{base}
	words := strings.Fields(base)
	for len(words) > 1 && panelGenericTrailers[strings.ToLower(words[len(words)-1])] {
		words = words[:len(words)-1]
	}
	subject := strings.Join(words, " ")
	if subject != base {
		ids = append(ids, subject, subject+"s")
	}
	return labeledIdentifiers{label: base, identifiers: ids}
}

func declaredPanels(scenario *evaluation.Scenario) []dashboardPanel {
	var out []dashboardPanel
	for _, s := range declaredState(scenario) {
		panels, ok := s.fields["panels"].([]interface{})
		if !ok {
			continue
		}
		for _, raw := range panels {
			m := toStringKeyMap(raw)
			title := strings.TrimSpace(scalarString(m["title"]))
			if title == "" {
				continue
			}
			out = append(out, dashboardPanel{
				title:   title,
				status:  strings.ToLower(strings.TrimSpace(scalarString(m["status"]))),
				labeled: panelIdentifiers(title),
			})
		}
	}
	return out
}

// interpretPanelsCoverageThreshold is the fraction of declared panels the agent
// must reference. The behaviour is "all relevant panels" and every declared
// panel is relevant — C-OI-004's normal panels are what makes the two abnormal
// ones stand out — so the bar is the one enumerate_dependents sets for "all".
const interpretPanelsCoverageThreshold = enumerateDependentsCoverageThreshold

// evalInterpretAllRelevantPanels decides `interpret_all_relevant_panels`: "Agent
// output references multiple panels. Compared against the set of relevant
// panels in the scenario" (behavior-definitions.md §9).
func (e *AssertionEngine) evalInterpretAllRelevantPanels(item evaluation.AssertionItem, response *evaluation.AgentResponse, scenario *evaluation.Scenario) (evaluation.AssertionResult, error) {
	panels := declaredPanels(scenario)
	if len(panels) < 2 {
		return evaluation.AssertionResult{}, fmt.Errorf("scenario declares %d dashboard panel(s); interpret_all_relevant_panels needs several", len(panels))
	}
	labels := make([]labeledIdentifiers, 0, len(panels))
	for _, p := range panels {
		labels = append(labels, p.labeled)
	}
	named := referencedIn(textOf(response), labels)
	ratio := float64(len(named)) / float64(len(panels))
	if ratio >= interpretPanelsCoverageThreshold {
		return passed(item, fmt.Sprintf("agent referenced %d of %d declared panels", len(named), len(panels))), nil
	}
	var missing []string
	got := make(map[string]bool)
	for _, n := range named {
		got[n] = true
	}
	for _, l := range labels {
		if !got[l.label] {
			missing = append(missing, l.label)
		}
	}
	return failed(item, fmt.Sprintf("agent referenced %d of %d declared panels (missing: %s)", len(named), len(panels), strings.Join(missing, ", "))), nil
}

var concernFacet = labeledIdentifiers{label: "a concern", identifiers: []string{
	"elevated", "critical", "high", "higher", "concern", "concerning", "concerns",
	"worry", "worrying", "warning", "alert", "alarm", "issue", "issues", "problem", "problems",
	"spike", "spiking", "spiked", "above", "exceed", "exceeds", "exceeded", "exceeding",
	"threshold", "breach", "breached", "urgent", "urgently", "priority", "prioritize",
	"attention", "investigate", "anomaly", "anomalous", "abnormal", "unhealthy", "degraded",
	"red", "bad", "danger", "dangerous", "nearly full", "running out", "full",
}}

// evalPrioritizeConcerningPanels decides `prioritize_concerning_panels`: "Agent
// output highlights the panels with anomalous values. The highlighted panels
// match the scenario's ground truth" (behavior-definitions.md §9). The ground
// truth is every panel whose declared status is not `normal`; each must be named
// within one radius-one window of a concern reference (or its own declared
// status word). A normal panel described as concerning is not scanned for: a
// sentence contrasting a normal and an abnormal panel would read as both, and
// the definition's verdict turns on the anomalous ones being highlighted.
func (e *AssertionEngine) evalPrioritizeConcerningPanels(item evaluation.AssertionItem, response *evaluation.AgentResponse, scenario *evaluation.Scenario) (evaluation.AssertionResult, error) {
	var concerning []dashboardPanel
	for _, p := range declaredPanels(scenario) {
		if p.status != "" && p.status != "normal" && p.status != "ok" && p.status != "healthy" {
			concerning = append(concerning, p)
		}
	}
	if len(concerning) == 0 {
		return evaluation.AssertionResult{}, fmt.Errorf("scenario declares no panel with a non-normal status; prioritize_concerning_panels has no ground truth")
	}
	var highlighted, missed []string
	for _, p := range concerning {
		concern := withScenarioIdentifiers(concernFacet, p.status)
		if _, ok := windowCovering(textOf(response), []labeledIdentifiers{p.labeled, concern}); ok {
			highlighted = append(highlighted, p.labeled.label)
		} else {
			missed = append(missed, p.labeled.label)
		}
	}
	if len(missed) == 0 {
		return passed(item, fmt.Sprintf("agent highlighted every concerning panel (%s)", strings.Join(highlighted, ", "))), nil
	}
	evidence := fmt.Sprintf("agent did not highlight %s as concerning", strings.Join(missed, ", "))
	if len(highlighted) > 0 {
		evidence += "; highlighted " + strings.Join(highlighted, ", ")
	}
	return failed(item, evidence), nil
}

var errorRateAndDiskFacets = []labeledIdentifiers{
	{label: "error rate", identifiers: []string{
		"error rate", "error-rate", "error_rate", "errors", "5xx", "5xxs", "500s", "error ratio", "failure rate",
	}},
	{label: "disk", identifiers: []string{
		"disk", "disks", "disk usage", "storage", "filesystem", "file system", "volume", "volumes", "pvc",
	}},
}

// evalIdentifyErrorRateAndDiskIssues decides
// `identify_error_rate_and_disk_issues`: "Agent output references both error
// rate and disk metrics" (behavior-definitions.md §5). Both references,
// anywhere in either text channel — the definition asks for both and no more.
// Whether they were singled out as the concerning ones is
// `prioritize_concerning_panels`, which the same scenario asserts.
func (e *AssertionEngine) evalIdentifyErrorRateAndDiskIssues(item evaluation.AssertionItem, response *evaluation.AgentResponse) (evaluation.AssertionResult, error) {
	named := referencedIn(textOf(response), errorRateAndDiskFacets)
	if len(named) == len(errorRateAndDiskFacets) {
		return passed(item, "agent referenced both error rate and disk"), nil
	}
	return failed(item, fmt.Sprintf("agent did not reference both error rate and disk%s", namedSuffix(named))), nil
}
