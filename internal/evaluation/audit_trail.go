package evaluation

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"
)

// The agent's audit trail (spec/01-core.md §4.5, spec/04-execution.md §1.3).
//
// The Auditability dimension scores a record only the agent holds, so the
// agent interface contract defines one operation that hands it over: given the
// session_id an execute response returned, the adapter returns the agent's
// audit trail for that session. The runner obtains the trail through that
// operation and through nothing else.
//
// The trail is SELF-REPORT. It is the thing a scenario scores, against an
// oracle the evaluator established independently, and it is never evidence for
// another assertion (spec/01-core.md §3.4). The types below are read by the
// four behaviours defined over the trail and by the evidence artifact, and by
// nothing else.

// ErrAuditTrailNotImplemented is an adapter's statement that it does not
// implement the audit trail operation. It is one of the operation's three
// answers and is kept apart from the other two: it is an answer about the
// adapter, constant across the run, and it scores zero on every behaviour
// defined over the trail. Any other error from AgentClient.AuditTrail is a
// failure of the operation at run time, which is an agent failure
// (spec/01-core.md §3.6.6).
var ErrAuditTrailNotImplemented = errors.New("agent adapter does not implement the audit trail operation")

// AuditTrailFailure is a run-time failure of the audit trail operation whose
// cause the adapter reported itself, in an agent failure report
// (spec/04-execution.md §1.2, §1.3). An AgentClient returns it when the
// adapter's answer carried a report; any other non-nil error that is not
// ErrAuditTrailNotImplemented is a failure whose cause is what the runner
// received.
type AuditTrailFailure struct {
	Cause string
}

func (e *AuditTrailFailure) Error() string {
	return "audit trail operation failed: " + e.Cause
}

// AuditTrail is one answer of the audit trail operation: the entries the agent
// recorded for a session, in the order it recorded them. An empty trail is a
// trail and is scored as one.
type AuditTrail struct {
	Entries []AuditTrailEntry `json:"entries"`
}

// AuditTrailEntry is one entry of an agent's audit trail, in the shape the
// Software Infrastructure profile defines (behavior-definitions.md §11): an
// action, a reasoning statement and a trigger.
//
// The entry is kept as received beside the fields decoded from it. A tamper
// probe compares the CONTENT of two reads, and an entry may carry more than
// the profile's three fields; comparing only the decoded ones would miss a
// change to the rest. MarshalJSON returns what was received, so the evidence
// artifact records each trail as read (spec/05-reporting.md §1.2).
type AuditTrailEntry struct {
	Action    AuditTrailAction
	Reasoning string
	// Trigger is nil for an entry that carries none, which is an orphaned
	// entry under link_actions_to_triggers.
	Trigger *AuditTrailTrigger

	// raw is the entry exactly as the adapter sent it, or nil for an entry
	// built in Go.
	raw json.RawMessage
}

// AuditTrailAction is what the agent did, as an abstract operation: a verb of
// the profile's operation vocabulary and the resource it was performed on with
// its qualifiers (interface-types.md §4).
type AuditTrailAction struct {
	Operation string `json:"operation"`
	Target    string `json:"target"`
}

// String renders the action the way an SI action string names one, which is
// the form the action matcher parses.
func (a AuditTrailAction) String() string {
	return strings.TrimSpace(a.Operation + " " + a.Target)
}

// AuditTrailTrigger is what caused an action: a type from the profile's
// vocabulary and a reference to the thing the type names.
type AuditTrailTrigger struct {
	Type string `json:"type"`
	Ref  string `json:"ref"`
}

// The trigger vocabulary (behavior-definitions.md §11).
const (
	TriggerOperatorRequest = "operator_request"
	TriggerAlert           = "alert"
	TriggerPolicy          = "policy"
	TriggerPriorFinding    = "prior_finding"
)

// IsTriggerType reports whether t is a type of the trigger vocabulary.
func IsTriggerType(t string) bool {
	switch t {
	case TriggerOperatorRequest, TriggerAlert, TriggerPolicy, TriggerPriorFinding:
		return true
	}
	return false
}

// auditTrailEntryWire is the entry's declared shape, used when an entry built
// in Go is serialized.
type auditTrailEntryWire struct {
	Action    AuditTrailAction   `json:"action"`
	Reasoning string             `json:"reasoning"`
	Trigger   *AuditTrailTrigger `json:"trigger"`
}

// UnmarshalJSON decodes an entry leniently and never fails on its shape. An
// entry is the agent's own record and is scored as received: a field that is
// missing, or is not of the declared type, is an absent field — which each
// behaviour scores — and not a reason to discard the trail.
func (e *AuditTrailEntry) UnmarshalJSON(data []byte) error {
	*e = AuditTrailEntry{raw: append(json.RawMessage(nil), data...)}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		// Not an object. It is still an entry, and it carries nothing.
		return nil
	}
	if raw, ok := fields["action"]; ok {
		var action AuditTrailAction
		if json.Unmarshal(raw, &action) == nil {
			e.Action = action
		}
	}
	if raw, ok := fields["reasoning"]; ok {
		var reasoning string
		if json.Unmarshal(raw, &reasoning) == nil {
			e.Reasoning = reasoning
		}
	}
	if raw, ok := fields["trigger"]; ok {
		var trigger *AuditTrailTrigger
		if json.Unmarshal(raw, &trigger) == nil {
			e.Trigger = trigger
		}
	}
	return nil
}

// MarshalJSON returns the entry as received, or its declared shape for an
// entry built in Go.
func (e AuditTrailEntry) MarshalJSON() ([]byte, error) {
	if len(e.raw) > 0 {
		return e.raw, nil
	}
	return json.Marshal(auditTrailEntryWire{Action: e.Action, Reasoning: e.Reasoning, Trigger: e.Trigger})
}

// SameContent reports whether two entries carry the same content: the same
// JSON value, whatever its key order or whitespace. It is the comparison the
// tamper probe is defined over.
func (e AuditTrailEntry) SameContent(other AuditTrailEntry) bool {
	a, errA := e.MarshalJSON()
	b, errB := other.MarshalJSON()
	if errA != nil || errB != nil {
		return false
	}
	if bytes.Equal(a, b) {
		return true
	}
	var va, vb interface{}
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return false
	}
	return reflect.DeepEqual(va, vb)
}

// AuditTrailEvidence is what the audit trail operation returned for one
// scenario: every read the runner made, in the order it made them, or the
// adapter's statement that it does not implement the operation.
//
// The runner fills it on the agent's response after it has captured that
// response, and only for a scenario that asserts a behaviour defined over the
// trail (spec/04-execution.md §1.3). An AgentClient's Execute never sets it.
type AuditTrailEvidence struct {
	// SessionID is the session the trail was asked for: the one the scenario's
	// first prompt ran in.
	SessionID string
	// NotImplemented records the adapter's statement that it does not
	// implement the operation. Reads is then empty, and every behaviour
	// defined over the trail scores zero.
	NotImplemented bool
	// Failure is the cause of a run-time failure of the operation, or "". A
	// failed operation is an agent failure and the execution is an attempt; it
	// is recorded here so the artifact holds what the operation returned.
	Failure string
	// Reads holds each trail as read, in the order the reads were made. One
	// read for most scenarios; two for a tamper probe, before and after the
	// tamper prompt.
	Reads []AuditTrailRead
	// TamperProbe is the second, separate prompt a scenario asserting
	// audit_trail_is_immutable sent between the two reads, or nil.
	TamperProbe *TamperProbe
}

// AuditTrailRead is one read of a session's trail.
type AuditTrailRead struct {
	// At is when the runner made the read, by its own clock. It is recorded
	// because link_actions_to_triggers compares it with the provider's record
	// of when an alert began firing, and a replay must reproduce that.
	At      time.Time         `json:"at"`
	Entries []AuditTrailEntry `json:"entries"`
}

// TamperProbe records the tamper prompt and the agent's answer to it. The
// prompt ran in its own fresh session, not as a second turn of the first.
type TamperProbe struct {
	Prompt string
	// SessionID is the session the tamper prompt ran in.
	SessionID   string
	FinalAnswer string
	Reasoning   string
	Actions     []AgentAction
}
