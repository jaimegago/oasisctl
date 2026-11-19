// Joe OASIS Adapter — translates between oasisctl's AgentRequest/AgentResponse
// format and Joe's POST /api/v1/tasks format, and serves the audit trail
// operation from Joe's session audit-trail read.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// oasisctl request/response types.

type AgentRequest struct {
	Prompt string     `json:"prompt"`
	Tools  []string   `json:"tools"`
	Mode   string     `json:"mode"`
	Scope  AgentScope `json:"scope"`
}

type AgentScope struct {
	Namespaces []string `json:"namespaces,omitempty"`
	Zones      []string `json:"zones,omitempty"`
}

type AgentResponse struct {
	Actions     []AgentAction `json:"actions"`
	Reasoning   string        `json:"reasoning"`
	FinalAnswer string        `json:"final_answer"`
	// SessionID names the session the prompt ran in (oasis-spec
	// spec/04-execution.md §1.3): joe's own session id, forwarded from its
	// task response unchanged. It is the value the audit trail operation is
	// asked with, and it is sent on every response joe answered — not only
	// for scenarios that score the trail.
	//
	// omitempty, for the one case the contract allows it to be absent: a
	// response carrying an agent failure report for a failure that left no
	// session to name, which here is the adapter failing to reach joe or to
	// read its reply.
	SessionID string `json:"session_id,omitempty"`
	// Model is the provider model identifier that served the execution, forwarded
	// from joe's task response (joe D-0153). It is omitempty in both directions:
	// joe omits it when nothing resolved, and the adapter omits it in turn, so an
	// agent that reports no model produces an absent field rather than an empty
	// string that would read downstream as a real observation.
	Model string `json:"model,omitempty"`
	// RootCause, Discarded and ConclusionDeclared carry the diagnostic
	// conclusion the agent declared on its terminal turn, forwarded from joe's
	// task response (joe D-0159). They exist because an evaluator deciding
	// whether an answer named a cause, or dismissed a signal, otherwise has
	// only the prose — and deciding it from prose measures vocabulary rather
	// than reasoning.
	//
	// Reporting one is an adapter CAPABILITY, exactly as Model is: an agent
	// whose adapter sends nothing here still evaluates, and its behaviours are
	// reported unassessable rather than scored zero.
	//
	// Discarded is NOT omitempty and is built non-nil, so it crosses the wire
	// as `[]` rather than disappearing. An absent list is ambiguous between
	// "ruled nothing out" and "declared nothing" — an answer and an absence —
	// and ConclusionDeclared is what separates them.
	RootCause          string            `json:"root_cause,omitempty"`
	Discarded          []DiscardedSignal `json:"discarded"`
	ConclusionDeclared bool              `json:"conclusion_declared,omitempty"`
	// EmptyAnswerGate forwards the empty-answer gate's outcome for the session
	// joe ran (joe D-0160): "held" when joe declined to return an `answer` turn
	// with nothing for the operator to read and the model then wrote one,
	// "not_held" when the re-entered session again ended on an empty answer and
	// that answer was returned as it stood, absent when the gate never fired.
	//
	// omitempty in both directions, exactly as Model is: joe omits it when the
	// gate never fired, an older joe does not send it at all, and the adapter
	// omits it in turn rather than sending an empty string that would read
	// downstream as an observed outcome named "".
	//
	// It is never an input to an assertion or a band. What it buys is that a
	// run can tell "the gate held" from "the defect never occurred" — a
	// distinction no artifact kept before, so the invariant built to close
	// `empty-answer-turn-accepted` could not be observed doing its work.
	EmptyAnswerGate string `json:"empty_answer_gate,omitempty"`
	// AgentFailure is the agent failure report (oasis-spec
	// spec/04-execution.md §1.2): the adapter's statement that joe could not
	// complete the task for its own infrastructure reasons, with the cause.
	// oasisctl treats a scenario carrying it as unevaluable — excluded from
	// every score and counted at run level — rather than scoring whatever
	// was returned as an answer.
	//
	// omitempty, and that is load-bearing: its PRESENCE is the signal, so it
	// must be absent on every ordinary response, including an empty one.
	AgentFailure *AgentFailureReport `json:"agent_failure,omitempty"`
	// TokenUsage and AgentLoopDurationMs forward joe's per-task token
	// accounting and the time joe spent inside its agent loop
	// (taskResponse.total_tokens / .duration_ms). They are NON-SCORING
	// metadata: cost and latency context for a result, never an input to an
	// assertion, a band or a verdict — oasisctl holds them in a type no
	// scoring path can read.
	//
	// The duration is renamed on purpose. joe brackets agent.Run and nothing
	// else, so it excludes joe's request handling, session setup and every
	// second of lab or fixture time; "duration_ms" would read as scenario
	// wall-clock, which it is not.
	//
	// omitempty pointers: an older joe sends neither, and the adapter then
	// sends nothing rather than zeros that would read as measurements.
	TokenUsage          *TokenUsage `json:"token_usage,omitempty"`
	AgentLoopDurationMs *int        `json:"agent_loop_duration_ms,omitempty"`
}

// TokenUsage is joe's per-task token accounting as forwarded to oasisctl. The
// cache counts are absent, never zero, when joe's provider does not report
// them: a present 0 is "no cache activity", an absent field is "not reported".
type TokenUsage struct {
	InputTokens      int  `json:"input_tokens"`
	OutputTokens     int  `json:"output_tokens"`
	CacheReadTokens  *int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int `json:"cache_write_tokens,omitempty"`
}

// AgentFailureReport is the body of an agent failure report.
type AgentFailureReport struct {
	Cause string `json:"cause"`
}

// DiscardedSignal is one signal the agent declared it ruled out, with the
// rationale it gave. Both halves travel separately because the act declared is
// discarding a signal WITH stated rationale: a signal named with no reason is a
// different, weaker thing, and an evaluator that requires the rationale must be
// able to see that it is missing.
type DiscardedSignal struct {
	Signal    string `json:"signal"`
	Rationale string `json:"rationale,omitempty"`
}

// AgentAction is one tool call in oasisctl's wire format. Result carries the
// tool's output as compact JSON *inside* a string — a joe string result "foo"
// becomes the five characters "foo", a JSON null becomes the four characters
// null, and an absent result is the empty string. The encoding is lossless and
// type-preserving; consumers decode the string as JSON.
type AgentAction struct {
	ID         string                 `json:"id,omitempty"`
	Tool       string                 `json:"tool"`
	Arguments  map[string]interface{} `json:"arguments"`
	Result     string                 `json:"result"`
	Error      string                 `json:"error,omitempty"`
	ErrorCode  string                 `json:"error_code,omitempty"`
	DurationMs int                    `json:"duration_ms,omitempty"`
}

// Identity and configuration types.

type IdentityAndConfigResponse struct {
	Identity      AgentIdentityResponse  `json:"identity"`
	Configuration map[string]interface{} `json:"configuration"`
}

type AgentIdentityResponse struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

// Joe request/response types.

type JoeRequest struct {
	Message string    `json:"message"`
	Config  JoeConfig `json:"config"`
}

type JoeConfig struct {
	SafetyTier        string   `json:"safety_tier"`
	Timeout           string   `json:"timeout"`
	AllowedZones      []string `json:"allowed_zones,omitempty"`
	AllowedNamespaces []string `json:"allowed_namespaces,omitempty"`
}

type JoeResponse struct {
	Steps       []JoeStep `json:"steps"`
	FinalAnswer string    `json:"final_answer"`
	// SessionID is joe's taskResponse.session_id: the session the turn ran
	// in. Every act joe's loop took in that turn is recorded against it, and
	// joe's audit-trail read is keyed by it.
	SessionID string `json:"session_id"`
	// Model is joe's provider model identifier for the turn — "claude-sonnet-4-20250514",
	// not the catalogue key that names it in joe's own config (joe D-0153). It
	// attests the model resolved at TASK PREPARATION, which is what the evidence
	// artifact's observed_model records. joe omits the field when it resolved no
	// model, and an older joe does not send it at all; both decode to "" here and
	// travel onward as an absent field, never an empty one.
	//
	// joe also reports a sibling `provider` (the adapter family). It is
	// deliberately not carried this slice — see the slice ledger.
	Model string `json:"model"`
	// RootCause / Discarded / ConclusionDeclared mirror joe's declared
	// diagnostic conclusion (joe D-0159, taskTurn.root_cause / .discarded /
	// .conclusion_declared). An older joe sends none of them; all three then
	// decode to their zero values and travel onward as an undeclared
	// conclusion, which is the truth about that agent rather than a defect.
	RootCause          string               `json:"root_cause"`
	Discarded          []JoeDiscardedSignal `json:"discarded"`
	ConclusionDeclared bool                 `json:"conclusion_declared"`
	// EmptyAnswerGate mirrors joe's taskTurn.empty_answer_gate (joe D-0160).
	// joe omits it when the gate never fired and an older joe never sends it;
	// both decode to "" here and travel onward as an absent field, which is the
	// truth about that run rather than a defect.
	EmptyAnswerGate string `json:"empty_answer_gate"`
	// Status, Iterations and Error are joe's terminal state for the turn
	// (taskResponse.status / .iterations / .error). Status is one of joe's
	// closed set — "completed", "timeout", "max_iterations_reached",
	// "runaway_terminated", "cost_limit_exceeded", "context_overflow",
	// "error" — and is what decides whether this turn is reported as an agent
	// failure (see agentFailureFromStatus).
	Status     string `json:"status"`
	Iterations int    `json:"iterations"`
	Error      string `json:"error"`
	// TotalTokens and DurationMs are joe's per-task token accounting and its
	// agent-loop duration (taskResponse.total_tokens / .duration_ms). Pointers
	// so a joe that sends neither decodes to nil rather than to zeros. The
	// cache counts inside TotalTokens are pointers for the same reason: joe
	// omits each one its provider does not report.
	TotalTokens *TokenUsage `json:"total_tokens"`
	DurationMs  *int        `json:"duration_ms"`
}

// JoeDiscardedSignal mirrors joe's taskDiscardedSignal.
type JoeDiscardedSignal struct {
	Signal    string `json:"signal"`
	Rationale string `json:"rationale"`
}

// JoeStep mirrors joe's taskStep (internal/api/tasks.go). Tool calls are nested
// inside llm_response, not at the step level; tool results are at the step level.
type JoeStep struct {
	StepNumber  int             `json:"step_number"`
	LLMResponse *JoeLLMResponse `json:"llm_response,omitempty"`
	ToolResults []JoeToolResult `json:"tool_results,omitempty"`
}

type JoeLLMResponse struct {
	Content   string        `json:"content"`
	ToolCalls []JoeToolCall `json:"tool_calls,omitempty"`
}

type JoeToolCall struct {
	ID   string                 `json:"id"`
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

// JoeToolResult mirrors joe's taskToolResult. Result is an arbitrary JSON value,
// so it is held as RawMessage and never coerced to a Go type.
type JoeToolResult struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Result     json.RawMessage `json:"result"`
	Error      string          `json:"error,omitempty"`
	ErrorCode  string          `json:"error_code,omitempty"`
	DurationMs int             `json:"duration_ms"`
}

// JoeStatusResponse is the expected shape of joe-core's GET /api/v1/status.
type JoeStatusResponse struct {
	Version string `json:"version"`
}

// adapterConfig holds CLI flags for the adapter.
type adapterConfig struct {
	listen          string
	joeURL          string
	joeToken        string
	timeout         time.Duration
	operationalMode string
	zoneModel       bool
	agentVersion    string
}

func modeToSafetyTier(mode string) string {
	switch mode {
	case "read-only":
		return "observe"
	case "supervised":
		return "record"
	case "autonomous":
		return "act"
	default:
		return "act"
	}
}

// toolCalls returns the step's tool calls, tolerating a missing llm_response.
func (r *JoeLLMResponse) toolCalls() []JoeToolCall {
	if r == nil {
		return nil
	}
	return r.ToolCalls
}

// applyResult copies a tool result onto an action, encoding the result body as
// compact JSON inside the string field. An absent result stays the empty string,
// which is distinguishable from a JSON null ("null") and an empty JSON string
// (`""`). The body is never truncated or summarized.
func applyResult(action *AgentAction, tr JoeToolResult) {
	action.Error = tr.Error
	action.ErrorCode = tr.ErrorCode
	action.DurationMs = tr.DurationMs

	if len(tr.Result) == 0 {
		return
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, tr.Result); err != nil {
		// Unreachable for a body that already decoded, but never silently drop:
		// fall back to the raw bytes as received.
		action.Result = string(tr.Result)
		return
	}
	action.Result = compact.String()
}

func translateResponse(jr *JoeResponse) *AgentResponse {
	resp := &AgentResponse{
		Actions:     []AgentAction{},
		FinalAnswer: jr.FinalAnswer,
		SessionID:   jr.SessionID,
		Model:       jr.Model,
		// Built non-nil unconditionally: the field must reach oasisctl as `[]`
		// and never as null, or the absent-list ambiguity the declaration
		// exists to remove comes back silently at the far end of the pipe.
		Discarded:           make([]DiscardedSignal, 0, len(jr.Discarded)),
		RootCause:           jr.RootCause,
		ConclusionDeclared:  jr.ConclusionDeclared,
		EmptyAnswerGate:     jr.EmptyAnswerGate,
		TokenUsage:          jr.TotalTokens,
		AgentLoopDurationMs: jr.DurationMs,
	}
	for _, d := range jr.Discarded {
		// A conversion rather than a field-by-field literal: the two types are
		// deliberately field-identical, and the conversion is what makes a
		// later divergence a compile error here instead of a silently dropped
		// field on the way to the evaluator.
		resp.Discarded = append(resp.Discarded, DiscardedSignal(d))
	}

	var reasoningParts []string
	stepNum := 0

	for _, step := range jr.Steps {
		// Collect reasoning from all steps that have LLM content, including
		// deliberation-only steps where the agent reasons without taking action
		// (e.g. safety refusals that detect zone boundary crossings).
		if step.LLMResponse != nil && step.LLMResponse.Content != "" {
			stepNum++
			reasoningParts = append(reasoningParts, fmt.Sprintf("Step %d: %s", stepNum, step.LLMResponse.Content))
		}

		// Pair calls to results by id, not by tool name: one step can call the
		// same tool more than once, and name-keyed pairing collides.
		resultByID := make(map[string]int, len(step.ToolResults))
		for i, tr := range step.ToolResults {
			if tr.ID == "" {
				continue
			}
			if _, seen := resultByID[tr.ID]; !seen {
				resultByID[tr.ID] = i
			}
		}
		paired := make([]bool, len(step.ToolResults))

		// Flatten tool calls into actions in call order, attaching each call's
		// own result. A call with no matching result keeps an empty result.
		for _, tc := range step.LLMResponse.toolCalls() {
			action := AgentAction{
				ID:        tc.ID,
				Tool:      tc.Name,
				Arguments: tc.Args,
			}
			if action.Arguments == nil {
				action.Arguments = map[string]interface{}{}
			}
			if i, ok := resultByID[tc.ID]; ok && !paired[i] {
				paired[i] = true
				applyResult(&action, step.ToolResults[i])
			}
			resp.Actions = append(resp.Actions, action)
		}

		// A result with no matching call is still evidence — emit it rather than
		// dropping it, carrying the result's own tool name and empty arguments.
		for i, tr := range step.ToolResults {
			if paired[i] {
				continue
			}
			action := AgentAction{
				ID:        tr.ID,
				Tool:      tr.Name,
				Arguments: map[string]interface{}{},
			}
			applyResult(&action, tr)
			resp.Actions = append(resp.Actions, action)
		}
	}

	resp.Reasoning = strings.Join(reasoningParts, "\n")

	// If FinalAnswer is empty and the last step is a deliberation step (has
	// LLM content but no tool calls), use that content as the final answer.
	// This covers safety refusal scenarios where the agent explains why it
	// will not act — that explanation IS the response.
	if resp.FinalAnswer == "" && len(jr.Steps) > 0 {
		last := jr.Steps[len(jr.Steps)-1]
		if last.LLMResponse != nil && last.LLMResponse.Content != "" && len(last.LLMResponse.toolCalls()) == 0 {
			resp.FinalAnswer = last.LLMResponse.Content
		}
	}

	return resp
}

// agentFailureStatuses are joe's terminal statuses that mean its turn ended in
// an LLM or provider error rather than in anything joe decided.
//
//   - "error" is joe's generic bucket for a run error no typed sentinel
//     claimed: a provider 404 for a retired model, a reset connection, an SDK
//     that could not carry a response. Every observed instance of
//     joe-pm queue/agent-llm-failure-scores-as-capability-miss.md landed here.
//   - "context_overflow" is the provider refusing the request because the
//     input exceeded the model's window — classified by joe's LLM adapter.
//
// Deliberately NOT here: "timeout" (the wall-clock budget joe was given),
// "max_iterations_reached" and "runaway_terminated" (joe's own loop and token
// budgets), and "cost_limit_exceeded" (joe's own spend gate, which refused the
// call before any provider saw it). Each is joe stopping itself under a limit,
// not a provider failing it, and what the agent produced within its limits is
// what gets scored.
var agentFailureStatuses = map[string]bool{
	"error":            true,
	"context_overflow": true,
}

// agentFailureFromStatus returns the agent failure report for a joe turn that
// ended in an LLM or provider error, at any iteration count, or nil. The cause
// carries joe's own status, iteration count and error verbatim, so the
// evidence artifact records what joe said rather than the adapter's reading of
// it.
func agentFailureFromStatus(jr *JoeResponse) *AgentFailureReport {
	if !agentFailureStatuses[jr.Status] {
		return nil
	}
	errText := jr.Error
	if errText == "" {
		errText = "(joe reported no error text)"
	}
	return &AgentFailureReport{
		Cause: fmt.Sprintf("joe status=%s iterations=%d: %s", jr.Status, jr.Iterations, errText),
	}
}

// errorResponse is the adapter's own failure: it could not reach joe, or could
// not read what joe returned. That is an agent failure as the contract defines
// it, and it is reported as one. The message travels in the report and not in
// final_answer, where it used to be — an error string there was scored as
// joe's answer to the task.
func errorResponse(msg string) *AgentResponse {
	return &AgentResponse{
		Actions:      []AgentAction{},
		Discarded:    []DiscardedSignal{},
		AgentFailure: &AgentFailureReport{Cause: "joe-adapter: " + msg},
	}
}

// --- Audit trail (oasis-spec spec/04-execution.md §1.3; SI profile
// behavior-definitions.md §11) ---
//
// joe keeps one audit entry for each act its agent loop takes against a
// managed system and serves a session's entries, in write order, on
// GET /api/v1/sessions/{id}/audit-trail. The adapter's audit trail operation
// is that read, translated entry by entry into the profile's entry shape.

// JoeAuditTrail is the body of joe's session audit-trail read. Entries are
// held raw so each one can travel onward exactly as joe sent it.
type JoeAuditTrail struct {
	SessionID string            `json:"session_id"`
	Entries   []json.RawMessage `json:"entries"`
}

// JoeAuditEntry is the part of a joe audit entry the translation reads.
type JoeAuditEntry struct {
	// Decision is "allow" for an act joe took and "deny" for one it refused.
	Decision  string           `json:"decision"`
	Operation JoeOperation     `json:"operation"`
	Reasoning string           `json:"reasoning"`
	Trigger   *JoeAuditTrigger `json:"trigger"`
}

// JoeOperation is what an act did and to what, as joe records it: at the
// resolution of a Kubernetes audit event.
type JoeOperation struct {
	Verb        string `json:"verb"`
	APIGroup    string `json:"api_group"`
	Resource    string `json:"resource"`
	Subresource string `json:"subresource"`
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
}

// JoeAuditTrigger is what caused an act. Type is already one of the
// profile's four; Source says whether joe's model declared it.
type JoeAuditTrigger struct {
	Type   string `json:"type"`
	Ref    string `json:"ref"`
	Source string `json:"source"`
}

// AuditTrailResponse is the audit trail operation's answer: the session's
// entries in the order joe recorded them. Entries is built non-nil, so a
// session that took no act answers `[]` — an empty trail, which is an answer —
// and never null.
type AuditTrailResponse struct {
	Entries []AuditTrailEntry `json:"entries"`
}

// AuditTrailEntry is one entry in the SI profile's shape, with joe's own
// entry beside it.
//
// JoeEntry is the entry exactly as joe returned it. It is there for two
// reasons. The evidence artifact records each trail as read, and joe's entry
// says more than the three profile fields do: the component, the tool call,
// the row's identity and time. And the tamper probe compares the content of
// two reads of one session, so the more of joe's record an entry carries, the
// more of it a change would have to leave untouched to go unseen.
type AuditTrailEntry struct {
	Action    AuditTrailAction   `json:"action"`
	Reasoning string             `json:"reasoning"`
	Trigger   *AuditTrailTrigger `json:"trigger,omitempty"`
	JoeEntry  json.RawMessage    `json:"joe_entry"`
}

// AuditTrailAction is an abstract operation: a verb of the profile's
// vocabulary and the resource it was performed on with its qualifiers.
type AuditTrailAction struct {
	Operation string `json:"operation"`
	Target    string `json:"target"`
}

// AuditTrailTrigger is what caused the action.
type AuditTrailTrigger struct {
	Type string `json:"type"`
	Ref  string `json:"ref"`
}

// auditTrailFailure is the body of a failed audit trail operation: an agent
// failure report naming the cause, which oasisctl records as the scenario's
// reason for being unevaluable.
type auditTrailFailure struct {
	AgentFailure AgentFailureReport `json:"agent_failure"`
}

// siResourceTypes maps the resource name joe records — the plural the
// Kubernetes API uses — onto the SI profile's resource type
// (interface-types.md §4.2), for the types where the two are one noun in
// singular and plural.
//
// `hpa` and `pvc` are deliberately absent. The profile abbreviates them and
// the API does not, and an entry's action is matched against the provider's
// audit log by the matcher a `must_not.action` uses, which pluralises the
// profile noun: `hpa` would become `hpas` and meet no entry that says
// `horizontalpodautoscalers`. Those two, and every resource the profile's
// table does not list, travel as joe recorded them, which is the form the
// audit log itself uses.
var siResourceTypes = map[string]string{
	"deployments":         "deployment",
	"pods":                "pod",
	"services":            "service",
	"configmaps":          "configmap",
	"secrets":             "secret",
	"namespaces":          "namespace",
	"ingresses":           "ingress",
	"nodes":               "node",
	"networkpolicies":     "networkpolicy",
	"roles":               "role",
	"rolebindings":        "rolebinding",
	"clusterroles":        "clusterrole",
	"clusterrolebindings": "clusterrolebinding",
}

// abstractOperation names joe's operation in the profile's verb vocabulary
// (interface-types.md §4.1). joe records what the API server saw, so a verb
// passes through as it is, with two exceptions the vocabulary names for
// itself and the subresource pins exactly: reading a pod's log subresource is
// `log`, and writing a scale subresource is `scale`.
func abstractOperation(op JoeOperation) string {
	verb := strings.ToLower(op.Verb)
	switch strings.ToLower(op.Subresource) {
	case "log":
		if verb == "get" {
			return "log"
		}
	case "scale":
		if verb == "update" || verb == "patch" {
			return "scale"
		}
	}
	return verb
}

// abstractTarget renders the resource joe's operation was performed on as
// `<resource_type>/<resource_name> [qualifiers]`. An operation that names no
// single resource, as a `list` does, is the resource type and its qualifiers.
func abstractTarget(op JoeOperation) string {
	resource := strings.ToLower(op.Resource)
	if si, ok := siResourceTypes[resource]; ok {
		resource = si
	}
	target := resource
	if op.Name != "" {
		target += "/" + op.Name
	}
	if op.Namespace != "" {
		target += " namespace=" + op.Namespace
	}
	return target
}

// translateAuditTrail turns joe's session trail into the profile's.
//
// An entry is an action the agent performed. joe also records, as an entry of
// the session, each act its accessor refused — a row whose decision is
// `deny`. Those are left out: nothing was done to the system, so presenting
// one as an action would claim an operation that never happened. The order of
// what remains is joe's write order, unchanged.
//
// It fails, rather than skipping, on an entry it cannot read: a trail handed
// over short would be scored as the whole of what joe recorded.
func translateAuditTrail(jt *JoeAuditTrail) (*AuditTrailResponse, error) {
	out := &AuditTrailResponse{Entries: make([]AuditTrailEntry, 0, len(jt.Entries))}
	for i, raw := range jt.Entries {
		var je JoeAuditEntry
		if err := json.Unmarshal(raw, &je); err != nil {
			return nil, fmt.Errorf("entry %d of joe's trail is not readable: %w", i, err)
		}
		switch je.Decision {
		case "allow":
		case "deny":
			continue
		default:
			return nil, fmt.Errorf("entry %d of joe's trail carries decision %q, neither allow nor deny", i, je.Decision)
		}
		entry := AuditTrailEntry{
			Action:    AuditTrailAction{Operation: abstractOperation(je.Operation), Target: abstractTarget(je.Operation)},
			Reasoning: je.Reasoning,
			JoeEntry:  raw,
		}
		if je.Trigger != nil {
			entry.Trigger = &AuditTrailTrigger{Type: je.Trigger.Type, Ref: je.Trigger.Ref}
		}
		out.Entries = append(out.Entries, entry)
	}
	return out, nil
}

// joeErrorBody is joe's API error shape.
type joeErrorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// writeAuditTrailFailure answers a failed audit trail operation. The status is
// never 404, 405 or 501: oasisctl reads those as "this adapter does not
// implement the operation" and scores zero, and a failure at run time is a
// different answer — the scenario is unevaluable, with the cause recorded.
func writeAuditTrailFailure(w http.ResponseWriter, status int, cause string) {
	log.Printf("AUDIT TRAIL FAILURE: %s", cause)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(auditTrailFailure{AgentFailure: AgentFailureReport{Cause: "joe-adapter: " + cause}}); err != nil {
		log.Printf("failed to write response: %v", err)
	}
}

// serveAuditTrail is the audit trail operation: GET /audit-trail?session_id=<id>.
//
// The three answers the contract keeps apart, and how each arises here:
//
//   - the trail — joe answered 200 and every entry was readable: 200 with
//     `entries`, possibly empty.
//   - not implemented — joe has no audit-trail route at all (a joe that
//     predates it answers 404 with no API error body): 501. That is a fact
//     about the joe behind the adapter, constant across a run.
//   - a failure — joe is unreachable, refuses the request, holds no such
//     session, or returns a trail that cannot be read: 502 (400 for a request
//     that names no session) with an agent failure report naming the cause.
func serveAuditTrail(cfg adapterConfig, client *http.Client) http.HandlerFunc {
	base := strings.TrimRight(cfg.joeURL, "/")
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		sessionID := r.URL.Query().Get("session_id")
		if sessionID == "" {
			writeAuditTrailFailure(w, http.StatusBadRequest, "audit trail request names no session_id")
			return
		}

		endpoint := base + "/api/v1/sessions/" + url.PathEscape(sessionID) + "/audit-trail"
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
		if err != nil {
			writeAuditTrailFailure(w, http.StatusBadGateway, fmt.Sprintf("could not build joe's audit-trail request: %v", err))
			return
		}
		if cfg.joeToken != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.joeToken)
		}
		resp, err := client.Do(req)
		if err != nil {
			writeAuditTrailFailure(w, http.StatusBadGateway, fmt.Sprintf("joe's audit-trail read failed: %v", err))
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			writeAuditTrailFailure(w, http.StatusBadGateway, fmt.Sprintf("could not read joe's audit-trail reply: %v", err))
			return
		}

		switch resp.StatusCode {
		case http.StatusOK:
		case http.StatusNotFound:
			// Two different 404s. joe's own "session not found" is an API
			// error body and a run-time failure: this session's trail cannot
			// be had. A 404 with no such body is a joe with no audit-trail
			// route, which is the adapter not implementing the operation.
			var apiErr joeErrorBody
			if json.Unmarshal(body, &apiErr) == nil && apiErr.Error != "" {
				writeAuditTrailFailure(w, http.StatusBadGateway,
					fmt.Sprintf("joe holds no session %q: %s", sessionID, apiErr.Message))
				return
			}
			http.Error(w, "this joe does not serve an audit trail", http.StatusNotImplemented)
			return
		default:
			writeAuditTrailFailure(w, http.StatusBadGateway,
				fmt.Sprintf("joe's audit-trail read returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
			return
		}

		var joeTrail JoeAuditTrail
		if err := json.Unmarshal(body, &joeTrail); err != nil || joeTrail.Entries == nil {
			writeAuditTrailFailure(w, http.StatusBadGateway,
				fmt.Sprintf("joe's audit-trail reply is not a trail (decode error: %v)", err))
			return
		}
		trail, err := translateAuditTrail(&joeTrail)
		if err != nil {
			writeAuditTrailFailure(w, http.StatusBadGateway, err.Error())
			return
		}
		log.Printf("AUDIT TRAIL for session %s: %d of joe's %d entries handed over", sessionID, len(trail.Entries), len(joeTrail.Entries))
		writeJSON(w, trail)
	}
}

// fetchVersionFromStatus probes joe-core's /api/v1/status endpoint and returns
// the version string if available. Returns "" on any failure.
func fetchVersionFromStatus(baseURL, token string) string {
	statusURL := strings.TrimRight(baseURL, "/") + "/api/v1/status"
	client := &http.Client{Timeout: 5 * time.Second}

	req, err := http.NewRequest(http.MethodGet, statusURL, nil)
	if err != nil {
		return ""
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var status JoeStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return ""
	}
	return status.Version
}

func main() {
	cfg := parseFlags()

	// Try to discover agent version from joe-core's status endpoint.
	if v := fetchVersionFromStatus(cfg.joeURL, cfg.joeToken); v != "" {
		log.Printf("discovered agent version %q from joe-core /api/v1/status", v)
		cfg.agentVersion = v
	} else if cfg.agentVersion == "unknown" {
		log.Printf("could not fetch version from joe-core /api/v1/status; using --agent-version=%q", cfg.agentVersion)
	}

	joeEndpoint := strings.TrimRight(cfg.joeURL, "/") + "/api/v1/tasks"
	mux := newServeMux(cfg)

	log.Printf("joe-adapter listening on %s, forwarding to %s", cfg.listen, joeEndpoint)
	log.Fatal(http.ListenAndServe(cfg.listen, mux))
}

// newServeMux builds the adapter's HTTP surface: the identity endpoint, the
// audit trail operation, and the execution endpoint that proxies to joe-core.
func newServeMux(cfg adapterConfig) *http.ServeMux {
	joeEndpoint := strings.TrimRight(cfg.joeURL, "/") + "/api/v1/tasks"
	client := &http.Client{Timeout: cfg.timeout}

	mux := http.NewServeMux()

	// GET /identity-and-configuration — returns agent identity and configuration.
	mux.HandleFunc("/identity-and-configuration", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		resp := IdentityAndConfigResponse{
			Identity: AgentIdentityResponse{
				Name:        "joe",
				Version:     cfg.agentVersion,
				Description: "AI infrastructure copilot for Kubernetes",
			},
			Configuration: map[string]interface{}{
				"operational_mode": cfg.operationalMode,
				"zone_model":       cfg.zoneModel,
				"interface_type":   "cli",
			},
		}

		writeJSON(w, resp)
	})

	// GET /audit-trail?session_id=<id> — the audit trail operation.
	mux.HandleFunc("/audit-trail", serveAuditTrail(cfg, client))

	// POST / — agent execution requests.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req AgentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, errorResponse(fmt.Sprintf("Error: invalid request: %v", err)))
			return
		}

		joeReq := JoeRequest{
			Message: req.Prompt,
			Config: JoeConfig{
				SafetyTier:        modeToSafetyTier(req.Mode),
				Timeout:           "2m",
				AllowedZones:      req.Scope.Zones,
				AllowedNamespaces: req.Scope.Namespaces,
			},
		}

		body, err := json.Marshal(joeReq)
		if err != nil {
			writeJSON(w, errorResponse(fmt.Sprintf("Error: failed to marshal request: %v", err)))
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), cfg.timeout)
		defer cancel()

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, joeEndpoint, bytes.NewReader(body))
		if err != nil {
			writeJSON(w, errorResponse(fmt.Sprintf("Error: failed to create request: %v", err)))
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if cfg.joeToken != "" {
			httpReq.Header.Set("Authorization", "Bearer "+cfg.joeToken)
		}

		resp, err := client.Do(httpReq)
		if err != nil {
			writeJSON(w, errorResponse(fmt.Sprintf("Error: Joe request failed: %v", err)))
			return
		}
		defer func() { _ = resp.Body.Close() }()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			writeJSON(w, errorResponse(fmt.Sprintf("Error: failed to read Joe response: %v", err)))
			return
		}

		if resp.StatusCode != http.StatusOK {
			writeJSON(w, errorResponse(fmt.Sprintf("Error: Joe returned status %d: %s", resp.StatusCode, string(respBody))))
			return
		}

		var joeResp JoeResponse
		if err := json.Unmarshal(respBody, &joeResp); err != nil {
			// Explicit error response, never a silently empty action list: the
			// message is surfaced in final_answer and logged with the body.
			log.Printf("ERROR: failed to decode Joe response: %v; body: %s", err, string(respBody))
			writeJSON(w, errorResponse(fmt.Sprintf("Error: failed to decode Joe response: %v", err)))
			return
		}

		oasisResp := translateResponse(&joeResp)
		// Whatever joe returned beside a failed turn — actions taken before
		// the failure, partial reasoning — still travels as received; the
		// report is what tells the evaluator not to score it.
		oasisResp.AgentFailure = agentFailureFromStatus(&joeResp)
		if oasisResp.AgentFailure != nil {
			log.Printf("AGENT FAILURE: %s", oasisResp.AgentFailure.Cause)
		}

		// Log the full agent response for debugging/visibility.
		scenarioID := req.Prompt
		if len(scenarioID) > 100 {
			scenarioID = scenarioID[:100]
		}
		log.Printf("=== AGENT RESPONSE for scenario %s ===", scenarioID)
		log.Printf("ACTIONS: %d", len(oasisResp.Actions))
		log.Printf("REASONING: %s", oasisResp.Reasoning)
		log.Printf("FINAL_ANSWER: %s", oasisResp.FinalAnswer)
		log.Print("=== END AGENT RESPONSE ===")

		writeJSON(w, oasisResp)
	})

	return mux
}

func parseFlags() adapterConfig {
	var cfg adapterConfig
	flag.StringVar(&cfg.listen, "listen", ":8091", "address to listen on")
	flag.StringVar(&cfg.joeURL, "joe-url", "", "Joe's HTTP API base URL (required)")
	flag.StringVar(&cfg.joeToken, "joe-token", "", "bearer token for Joe's API")
	flag.DurationVar(&cfg.timeout, "timeout", 3*time.Minute, "per-request timeout")
	flag.StringVar(&cfg.operationalMode, "operational-mode", "", "Joe's operational mode: read_only or read_write (required)")
	flag.BoolVar(&cfg.zoneModel, "zone-model", true, "whether Joe has security zones enabled")
	flag.StringVar(&cfg.agentVersion, "agent-version", "unknown", "agent version string (overridden by joe-core /api/v1/status if reachable)")
	flag.Parse()

	if cfg.joeURL == "" {
		log.Fatal("--joe-url is required")
	}
	if cfg.operationalMode == "" {
		log.Fatal("--operational-mode is required (must be read_only or read_write)")
	}
	if cfg.operationalMode != "read_only" && cfg.operationalMode != "read_write" {
		log.Fatalf("--operational-mode must be read_only or read_write, got %q", cfg.operationalMode)
	}

	return cfg
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("failed to write response: %v", err)
	}
}
