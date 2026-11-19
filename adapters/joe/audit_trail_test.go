package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// joeTrailBody is a session audit trail as joe serves it on
// GET /api/v1/sessions/{id}/audit-trail: three entries in write order, the
// middle one an act joe's accessor refused.
const joeTrailBody = `{
  "session_id": "sess-1",
  "count": 3,
  "entries": [
    {
      "id": 41,
      "recorded_at": "2026-11-18T12:00:00.000000001Z",
      "principal": "svc:oasis",
      "component_id": "k8s-lab",
      "zone": "unassigned",
      "decision": "allow",
      "decision_reason": "team_flat_read",
      "session_id": "sess-1",
      "task_id": "task-1",
      "tool": "k8s_get",
      "tool_call_id": "call_1",
      "operation": {"verb": "get", "api_group": "apps", "resource": "deployments", "name": "api-service", "namespace": "default"},
      "reasoning": "  The operator asked for a health check of api-service.\n",
      "trigger": {"type": "operator_request", "ref": "task/task-1", "source": "session"}
    },
    {
      "id": 42,
      "recorded_at": "2026-11-18T12:00:01Z",
      "principal": "svc:oasis",
      "component_id": "k8s-lab",
      "decision": "deny",
      "decision_reason": "no_grant",
      "session_id": "sess-1",
      "task_id": "task-1",
      "tool": "k8s_get",
      "tool_call_id": "call_2",
      "operation": {"verb": "get", "resource": "secrets", "name": "db", "namespace": "default"},
      "reasoning": "Look at the credentials.",
      "trigger": {"type": "operator_request", "ref": "task/task-1", "source": "session"}
    },
    {
      "id": 43,
      "recorded_at": "2026-11-18T12:00:02Z",
      "principal": "svc:oasis",
      "component_id": "k8s-lab",
      "decision": "allow",
      "decision_reason": "team_flat_read",
      "session_id": "sess-1",
      "task_id": "task-1",
      "tool": "k8s_logs",
      "tool_call_id": "call_3",
      "operation": {"verb": "get", "resource": "pods", "subresource": "log", "name": "api-service-0", "namespace": "default"},
      "reasoning": "",
      "trigger": {"type": "alert", "ref": "alert/high-memory", "source": "model"}
    }
  ]
}`

// trailAdapter starts the adapter's real surface in front of a stub joe.
func trailAdapter(t *testing.T, joe http.HandlerFunc, token string) *httptest.Server {
	t.Helper()
	joeCore := httptest.NewServer(joe)
	t.Cleanup(joeCore.Close)
	cfg := adapterConfig{joeURL: joeCore.URL, joeToken: token, timeout: 5 * time.Second, operationalMode: "read_only", zoneModel: true}
	adapter := httptest.NewServer(newServeMux(cfg))
	t.Cleanup(adapter.Close)
	return adapter
}

func getTrail(t *testing.T, url string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, body
}

// trailWire is the operation's answer decoded the way a consumer reads it
// (oasisctl docs/agent-interface-contract.md § Audit trail).
type trailWire struct {
	Entries []struct {
		Action struct {
			Operation string `json:"operation"`
			Target    string `json:"target"`
		} `json:"action"`
		Reasoning *string `json:"reasoning"`
		Trigger   *struct {
			Type string `json:"type"`
			Ref  string `json:"ref"`
		} `json:"trigger"`
		JoeEntry json.RawMessage `json:"joe_entry"`
	} `json:"entries"`
	AgentFailure *struct {
		Cause string `json:"cause"`
	} `json:"agent_failure"`
}

// TestAuditTrail_HandsOverJoesTrail drives the served operation: the adapter
// asks joe for the session's trail by the session_id it was given, with its
// bearer token, and answers 200 with the acts joe took, in joe's order, in the
// profile's entry shape.
func TestAuditTrail_HandsOverJoesTrail(t *testing.T) {
	var gotPath, gotAuth, gotMethod string
	adapter := trailAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotMethod = r.URL.EscapedPath(), r.Header.Get("Authorization"), r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(joeTrailBody))
	}, "tok-123")

	status, body := getTrail(t, adapter.URL+"/audit-trail?session_id=sess-1")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/v1/sessions/sess-1/audit-trail" {
		t.Errorf("joe was asked %s %s; want GET /api/v1/sessions/sess-1/audit-trail", gotMethod, gotPath)
	}
	if gotAuth != "Bearer tok-123" {
		t.Errorf("Authorization sent to joe = %q", gotAuth)
	}

	var trail trailWire
	if err := json.Unmarshal(body, &trail); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if trail.AgentFailure != nil {
		t.Fatalf("a served trail carries an agent failure report: %+v", trail.AgentFailure)
	}
	// The refused act is not an action the agent performed, and is left out.
	if len(trail.Entries) != 2 {
		t.Fatalf("%d entries, want the 2 acts joe took: %s", len(trail.Entries), body)
	}

	get, logs := trail.Entries[0], trail.Entries[1]
	if get.Action.Operation != "get" || get.Action.Target != "deployment/api-service namespace=default" {
		t.Errorf("first action = %q %q", get.Action.Operation, get.Action.Target)
	}
	if get.Reasoning == nil || *get.Reasoning != "  The operator asked for a health check of api-service.\n" {
		t.Errorf("first reasoning = %v; want joe's text verbatim", get.Reasoning)
	}
	if get.Trigger == nil || get.Trigger.Type != "operator_request" || get.Trigger.Ref != "task/task-1" {
		t.Errorf("first trigger = %+v", get.Trigger)
	}

	// A pod's log subresource is the profile's `log` verb.
	if logs.Action.Operation != "log" || logs.Action.Target != "pod/api-service-0 namespace=default" {
		t.Errorf("second action = %q %q", logs.Action.Operation, logs.Action.Target)
	}
	// joe recorded an empty reason: the adapter hands over an empty reason,
	// present, and writes none of its own.
	if logs.Reasoning == nil || *logs.Reasoning != "" {
		t.Errorf("second reasoning = %v; want present and empty", logs.Reasoning)
	}
	if logs.Trigger == nil || logs.Trigger.Type != "alert" || logs.Trigger.Ref != "alert/high-memory" {
		t.Errorf("second trigger = %+v", logs.Trigger)
	}

	// Each entry carries joe's own entry, whole.
	for i, wantID := range []float64{41, 43} {
		var joeEntry map[string]any
		if err := json.Unmarshal(trail.Entries[i].JoeEntry, &joeEntry); err != nil {
			t.Fatalf("entry %d joe_entry: %v", i, err)
		}
		if joeEntry["id"] != wantID || joeEntry["component_id"] != "k8s-lab" || joeEntry["session_id"] != "sess-1" {
			t.Errorf("entry %d joe_entry = %v; want joe's entry %v as received", i, joeEntry, wantID)
		}
	}
}

// TestAuditTrail_ReadTwiceIsTheSameTrail is what the tamper probe rests on at
// the adapter: two reads of one session over an unchanged joe trail are
// byte-identical, so a difference between two reads is joe's and never the
// adapter's.
func TestAuditTrail_ReadTwiceIsTheSameTrail(t *testing.T) {
	adapter := trailAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(joeTrailBody))
	}, "")
	_, first := getTrail(t, adapter.URL+"/audit-trail?session_id=sess-1")
	_, second := getTrail(t, adapter.URL+"/audit-trail?session_id=sess-1")
	if string(first) != string(second) {
		t.Errorf("two reads differ:\n first %s\nsecond %s", first, second)
	}
}

// TestAuditTrail_EmptyTrailIsAnAnswer: a session in which joe took no act is
// a trail with no entries — 200 and `[]`, never null and never a failure. A
// trail holding only refused acts is the same answer.
func TestAuditTrail_EmptyTrailIsAnAnswer(t *testing.T) {
	for name, joeBody := range map[string]string{
		"no entries":        `{"session_id":"s","entries":[],"count":0}`,
		"only refused acts": `{"session_id":"s","entries":[{"decision":"deny","operation":{"verb":"get","resource":"secrets"},"reasoning":"x"}],"count":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			adapter := trailAdapter(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(joeBody)) }, "")
			status, body := getTrail(t, adapter.URL+"/audit-trail?session_id=s")
			if status != http.StatusOK {
				t.Fatalf("status = %d, body %s", status, body)
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if string(raw["entries"]) != "[]" {
				t.Errorf("entries = %s; want []", raw["entries"])
			}
		})
	}
}

// TestAuditTrail_FailureIsAnAgentFailureNeverNotImplemented: when joe cannot
// hand the trail over, the adapter says the operation failed and why. It never
// answers 404, 405 or 501 — the statuses oasisctl scores as zero — and never
// 200, which would be scored as a trail.
func TestAuditTrail_FailureIsAnAgentFailureNeverNotImplemented(t *testing.T) {
	cases := []struct {
		name      string
		joe       http.HandlerFunc
		query     string
		wantCode  int
		wantCause string
	}{
		{
			name: "joe holds no such session",
			joe: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"not_found","message":"session not found"}`))
			},
			query: "session_id=gone", wantCode: http.StatusBadGateway, wantCause: `joe holds no session "gone"`,
		},
		{
			name: "joe refuses the adapter's credential",
			joe: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized","message":"invalid token"}`))
			},
			query: "session_id=s", wantCode: http.StatusBadGateway, wantCause: "status 401",
		},
		{
			name: "joe's read fails",
			joe: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"internal_error","message":"internal error"}`))
			},
			query: "session_id=s", wantCode: http.StatusBadGateway, wantCause: "status 500",
		},
		{
			name:  "joe's reply is not JSON",
			joe:   func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`<html>not a trail</html>`)) },
			query: "session_id=s", wantCode: http.StatusBadGateway, wantCause: "not a trail",
		},
		{
			name:  "joe's reply has no entries array",
			joe:   func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"session_id":"s"}`)) },
			query: "session_id=s", wantCode: http.StatusBadGateway, wantCause: "not a trail",
		},
		{
			name: "an entry cannot be read",
			joe: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"session_id":"s","entries":[{"decision":"allow","operation":{"verb":"list","resource":"pods"}},{"decision":"maybe"}]}`))
			},
			query: "session_id=s", wantCode: http.StatusBadGateway, wantCause: `decision "maybe"`,
		},
		{
			name:  "the request names no session",
			joe:   func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(joeTrailBody)) },
			query: "", wantCode: http.StatusBadRequest, wantCause: "names no session_id",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter := trailAdapter(t, tc.joe, "")
			status, body := getTrail(t, adapter.URL+"/audit-trail?"+tc.query)
			if status != tc.wantCode {
				t.Errorf("status = %d, want %d: %s", status, tc.wantCode, body)
			}
			switch status {
			case http.StatusOK, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
				t.Errorf("status %d would be scored as a trail or as not-implemented", status)
			}
			var trail trailWire
			if err := json.Unmarshal(body, &trail); err != nil {
				t.Fatalf("failure body is not JSON: %s", body)
			}
			if trail.AgentFailure == nil || !strings.Contains(trail.AgentFailure.Cause, tc.wantCause) {
				t.Errorf("agent_failure = %+v; want a cause containing %q", trail.AgentFailure, tc.wantCause)
			}
			if trail.Entries != nil {
				t.Errorf("a failure carries entries: %s", body)
			}
		})
	}

	t.Run("joe is unreachable", func(t *testing.T) {
		cfg := adapterConfig{joeURL: "http://127.0.0.1:1", timeout: 2 * time.Second, operationalMode: "read_only"}
		adapter := httptest.NewServer(newServeMux(cfg))
		defer adapter.Close()
		status, body := getTrail(t, adapter.URL+"/audit-trail?session_id=s")
		var trail trailWire
		_ = json.Unmarshal(body, &trail)
		if status != http.StatusBadGateway || trail.AgentFailure == nil {
			t.Errorf("status = %d, body %s; want 502 with an agent failure report", status, body)
		}
	})
}

// TestAuditTrail_JoeWithoutTheRouteIsNotImplemented: a joe that predates the
// audit-trail read answers 404 with no API error body. That is the adapter not
// implementing the operation — 501, scored zero — and not a run-time failure.
// It is kept apart from joe's own "session not found", which is one.
func TestAuditTrail_JoeWithoutTheRouteIsNotImplemented(t *testing.T) {
	adapter := trailAdapter(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }, "")
	status, body := getTrail(t, adapter.URL+"/audit-trail?session_id=s")
	if status != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501: %s", status, body)
	}
	if strings.Contains(string(body), "agent_failure") {
		t.Errorf("not-implemented carries an agent failure report: %s", body)
	}
}

// TestAuditTrail_IsARead: the operation answers GET alone, and a refused
// method never reaches joe.
func TestAuditTrail_IsARead(t *testing.T) {
	reached := false
	adapter := trailAdapter(t, func(w http.ResponseWriter, _ *http.Request) { reached = true }, "")
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req, err := http.NewRequest(method, adapter.URL+"/audit-trail?session_id=s", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != "GET" {
			t.Errorf("%s: status %d, Allow %q; want 405 and GET", method, resp.StatusCode, resp.Header.Get("Allow"))
		}
	}
	if reached {
		t.Error("a refused method reached joe")
	}
}

// TestAbstractAction pins the translation from what joe records — the API's
// own verb, plural resource, subresource, name and namespace — to the
// profile's abstract operation and target.
func TestAbstractAction(t *testing.T) {
	cases := []struct {
		name          string
		op            JoeOperation
		wantOperation string
		wantTarget    string
	}{
		{"get by name", JoeOperation{Verb: "get", APIGroup: "apps", Resource: "deployments", Name: "api", Namespace: "default"}, "get", "deployment/api namespace=default"},
		{"list in a namespace", JoeOperation{Verb: "list", Resource: "pods", Namespace: "default"}, "list", "pod namespace=default"},
		{"list across namespaces", JoeOperation{Verb: "list", Resource: "pods"}, "list", "pod"},
		{"cluster-scoped get", JoeOperation{Verb: "get", Resource: "nodes", Name: "n1"}, "get", "node/n1"},
		{"pod logs", JoeOperation{Verb: "get", Resource: "pods", Subresource: "log", Name: "api-0", Namespace: "default"}, "log", "pod/api-0 namespace=default"},
		{"scale subresource", JoeOperation{Verb: "patch", APIGroup: "apps", Resource: "deployments", Subresource: "scale", Name: "api", Namespace: "default"}, "scale", "deployment/api namespace=default"},
		{"another subresource keeps its verb", JoeOperation{Verb: "get", Resource: "pods", Subresource: "status", Name: "api-0", Namespace: "default"}, "get", "pod/api-0 namespace=default"},
		{"irregular plural", JoeOperation{Verb: "list", APIGroup: "networking.k8s.io", Resource: "ingresses", Namespace: "web"}, "list", "ingress namespace=web"},
		{"abbreviated profile type travels as recorded", JoeOperation{Verb: "list", APIGroup: "autoscaling", Resource: "horizontalpodautoscalers", Namespace: "web"}, "list", "horizontalpodautoscalers namespace=web"},
		{"a resource the profile does not list", JoeOperation{Verb: "list", APIGroup: "cert-manager.io", Resource: "certificates", Namespace: "web"}, "list", "certificates namespace=web"},
		{"a system that is not kubernetes", JoeOperation{Verb: "query", Resource: "metrics"}, "query", "metrics"},
		{"case is normalised", JoeOperation{Verb: "GET", Resource: "Deployments", Name: "api", Namespace: "default"}, "get", "deployment/api namespace=default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := abstractOperation(tc.op); got != tc.wantOperation {
				t.Errorf("operation = %q, want %q", got, tc.wantOperation)
			}
			if got := abstractTarget(tc.op); got != tc.wantTarget {
				t.Errorf("target = %q, want %q", got, tc.wantTarget)
			}
		})
	}
}

// TestServeExecute_ReturnsJoesSessionID: every execute response joe answered
// carries joe's session id, which is what the audit trail operation is asked
// with. A response the adapter built because it never reached joe has no
// session to name, and carries no session_id key at all.
func TestServeExecute_ReturnsJoesSessionID(t *testing.T) {
	joeCore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task_id":"task-1","session_id":"sess-77","status":"completed","steps":[],"final_answer":"done"}`))
	}))
	defer joeCore.Close()
	adapter := httptest.NewServer(newServeMux(adapterConfig{joeURL: joeCore.URL, timeout: 5 * time.Second, operationalMode: "read_only"}))
	defer adapter.Close()

	post := func(url string) map[string]json.RawMessage {
		t.Helper()
		resp, err := http.Post(url, "application/json", strings.NewReader(`{"prompt":"p","tools":[],"mode":"read-only","scope":{}}`))
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		var out map[string]json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out
	}

	if got := string(post(adapter.URL + "/")["session_id"]); got != `"sess-77"` {
		t.Errorf("session_id = %s, want joe's \"sess-77\"", got)
	}

	// A joe failure that still ran in a session names it beside the report.
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"session_id":"sess-78","status":"error","iterations":0,"error":"provider 500","steps":[],"final_answer":""}`))
	}))
	defer failing.Close()
	adapter2 := httptest.NewServer(newServeMux(adapterConfig{joeURL: failing.URL, timeout: 5 * time.Second, operationalMode: "read_only"}))
	defer adapter2.Close()
	out := post(adapter2.URL + "/")
	if string(out["session_id"]) != `"sess-78"` || out["agent_failure"] == nil {
		t.Errorf("failed turn: session_id = %s, agent_failure present = %v; want the session named beside the report", out["session_id"], out["agent_failure"] != nil)
	}

	// The adapter never reached joe: no session, no key.
	unreachable := httptest.NewServer(newServeMux(adapterConfig{joeURL: "http://127.0.0.1:1", timeout: 2 * time.Second, operationalMode: "read_only"}))
	defer unreachable.Close()
	out = post(unreachable.URL + "/")
	if _, present := out["session_id"]; present {
		t.Errorf("an adapter failure with no session carries session_id = %s", out["session_id"])
	}
	if out["agent_failure"] == nil {
		t.Error("an unreachable joe is not reported as an agent failure")
	}
}
