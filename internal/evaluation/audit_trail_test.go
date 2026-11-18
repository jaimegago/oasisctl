package evaluation

import (
	"encoding/json"
	"testing"
)

func TestAuditTrailEntry_IsDecodedLenientlyAndKeptAsReceived(t *testing.T) {
	const raw = `[
		{"action":{"operation":"get","target":"deployment/api"},"reasoning":"why","trigger":{"type":"alert","ref":"alert/x"},"extra":{"n":1}},
		{"action":"restart everything","reasoning":7,"trigger":"because"},
		{},
		"not an object",
		{"action":{"operation":"list","target":"pod"},"trigger":null}
	]`
	var entries []AuditTrailEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		t.Fatalf("an entry's shape is scored, never a decode failure: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("entries = %d, want 5: every element is an entry", len(entries))
	}

	first := entries[0]
	if first.Action.Operation != "get" || first.Action.Target != "deployment/api" || first.Reasoning != "why" {
		t.Errorf("first entry misread: %+v", first)
	}
	if first.Trigger == nil || first.Trigger.Type != TriggerAlert || first.Trigger.Ref != "alert/x" {
		t.Errorf("first trigger misread: %+v", first.Trigger)
	}

	// A field of the wrong type is an absent field.
	for i, e := range entries[1:4] {
		if e.Action != (AuditTrailAction{}) || e.Reasoning != "" || e.Trigger != nil {
			t.Errorf("entry %d: want nothing decoded, got %+v", i+2, e)
		}
	}
	if entries[4].Trigger != nil {
		t.Errorf("a null trigger is no trigger, got %+v", entries[4].Trigger)
	}

	// Recorded as received: the fields the profile does not define survive.
	out, err := json.Marshal(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	var round map[string]interface{}
	if err := json.Unmarshal(out, &round); err != nil {
		t.Fatal(err)
	}
	if _, ok := round["extra"]; !ok {
		t.Errorf("the entry was not kept as received: %s", out)
	}
	if out, _ := json.Marshal(entries[3]); string(out) != `"not an object"` {
		t.Errorf("a non-object entry is recorded as received, got %s", out)
	}
}

func TestAuditTrailEntry_SameContent(t *testing.T) {
	decode := func(raw string) AuditTrailEntry {
		var e AuditTrailEntry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	base := decode(`{"action":{"operation":"get","target":"pod/a"},"reasoning":"r","at":1}`)

	if !base.SameContent(decode(`{ "at": 1, "reasoning": "r", "action": {"target":"pod/a","operation":"get"} }`)) {
		t.Error("key order and whitespace are not content")
	}
	if base.SameContent(decode(`{"action":{"operation":"get","target":"pod/a"},"reasoning":"r","at":2}`)) {
		t.Error("a field the profile does not define is still content")
	}
	if base.SameContent(decode(`{"action":{"operation":"get","target":"pod/a"},"reasoning":"r"}`)) {
		t.Error("a dropped field is a change")
	}

	built := AuditTrailEntry{Action: AuditTrailAction{Operation: "get", Target: "pod/a"}, Reasoning: "r"}
	same := AuditTrailEntry{Action: AuditTrailAction{Operation: "get", Target: "pod/a"}, Reasoning: "r"}
	if !built.SameContent(same) {
		t.Error("two entries built alike carry the same content")
	}
	same.Trigger = &AuditTrailTrigger{Type: TriggerPolicy, Ref: "p"}
	if built.SameContent(same) {
		t.Error("a trigger added is a change")
	}
}

func TestIsTriggerType(t *testing.T) {
	for _, ok := range []string{"operator_request", "alert", "policy", "prior_finding"} {
		if !IsTriggerType(ok) {
			t.Errorf("%q is in the vocabulary", ok)
		}
	}
	for _, bad := range []string{"", "Alert", "user_request", "schedule"} {
		if IsTriggerType(bad) {
			t.Errorf("%q is outside the vocabulary", bad)
		}
	}
}
