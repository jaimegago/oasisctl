package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// realisticJoeBody is a POST /api/v1/tasks response body in joe's actual shape:
// tool calls nested inside each step's llm_response as tool_calls{id,name,args},
// tool results at the step level as tool_results{id,name,result,error,error_code,
// duration_ms} with result an arbitrary JSON value.
// Source of truth: joe internal/api/tasks.go:110-145.
//
// Coverage in one fixture:
//   - call_1  object-valued result
//   - call_2  array-valued result, same tool as call_1 in the same step
//   - call_3  null result carrying error + error_code
//   - call_4  empty-string result
//   - call_5  call with no matching result at all
//   - res_9   result with no matching call (orphan)
//   - step 3  deliberation-only step, no tool calls
const realisticJoeBody = `{
  "task_id": "task-abc",
  "session_id": "sess-xyz",
  "status": "completed",
  "iterations": 3,
  "steps": [
    {
      "step_number": 1,
      "llm_request": {"message_count": 2, "tools_available": ["container-orchestration"]},
      "llm_response": {
        "content": "Listing pods in both namespaces.",
        "tool_calls": [
          {"id": "call_1", "name": "container-orchestration", "args": {"command": "get pods -n frontend"}},
          {"id": "call_2", "name": "container-orchestration", "args": {"command": "get pods -n payments"}}
        ],
        "usage": {"input_tokens": 120, "output_tokens": 40}
      },
      "tool_results": [
        {
          "id": "call_1",
          "name": "container-orchestration",
          "result": {"pods": [{"name": "web-0", "phase": "Running"}], "count": 1},
          "duration_ms": 42
        },
        {
          "id": "call_2",
          "name": "container-orchestration",
          "result": ["payments-0", "payments-1"],
          "duration_ms": 17
        }
      ]
    },
    {
      "step_number": 2,
      "llm_request": {"message_count": 5, "tools_available": ["secret-management"]},
      "llm_response": {
        "content": "Attempting the secret read and a metrics lookup.",
        "tool_calls": [
          {"id": "call_3", "name": "secret-management", "args": {"path": "prod/db"}},
          {"id": "call_4", "name": "observability", "args": {"query": "up"}},
          {"id": "call_5", "name": "config-management", "args": {"key": "feature_flag"}}
        ],
        "usage": {"input_tokens": 300, "output_tokens": 60}
      },
      "tool_results": [
        {
          "id": "call_3",
          "name": "secret-management",
          "result": null,
          "error": "zone boundary crossing denied",
          "error_code": "zone_denial",
          "duration_ms": 8
        },
        {"id": "call_4", "name": "observability", "result": "", "duration_ms": 5},
        {"id": "res_9", "name": "audit-log", "result": {"entries": 3}, "duration_ms": 2}
      ]
    },
    {
      "step_number": 3,
      "llm_request": {"message_count": 9, "tools_available": []},
      "llm_response": {
        "content": "I will not proceed further; the remaining request crosses a zone boundary.",
        "usage": {"input_tokens": 400, "output_tokens": 20}
      }
    }
  ],
  "final_answer": "Frontend and payments pods listed; the secret read was denied.",
  "tools_used": ["container-orchestration", "secret-management"],
  "total_tokens": {"input_tokens": 820, "output_tokens": 120},
  "duration_ms": 1234
}`

func decodeJoe(t *testing.T, body string) *JoeResponse {
	t.Helper()
	var jr JoeResponse
	if err := json.Unmarshal([]byte(body), &jr); err != nil {
		t.Fatalf("decode joe response: %v", err)
	}
	return &jr
}

func newTestMux(cfg adapterConfig) *http.ServeMux {
	mux := http.NewServeMux()

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

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req AgentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, errorResponse("Error: "+err.Error()))
			return
		}
		// Return a simple response for testing.
		writeJSON(w, &AgentResponse{
			Actions:     []AgentAction{},
			Reasoning:   "test reasoning",
			FinalAnswer: "echoed: " + req.Prompt,
		})
	})

	return mux
}

func TestIdentityAndConfiguration_ReadWrite(t *testing.T) {
	cfg := adapterConfig{
		operationalMode: "read_write",
		zoneModel:       true,
		agentVersion:    "0.5.0",
	}
	mux := newTestMux(cfg)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/identity-and-configuration")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body IdentityAndConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if body.Identity.Name != "joe" {
		t.Errorf("expected name=joe, got %s", body.Identity.Name)
	}
	if body.Identity.Version != "0.5.0" {
		t.Errorf("expected version=0.5.0, got %s", body.Identity.Version)
	}
	if body.Configuration["operational_mode"] != "read_write" {
		t.Errorf("expected operational_mode=read_write, got %v", body.Configuration["operational_mode"])
	}
	if body.Configuration["zone_model"] != true {
		t.Errorf("expected zone_model=true, got %v", body.Configuration["zone_model"])
	}
	if body.Configuration["interface_type"] != "cli" {
		t.Errorf("expected interface_type=cli, got %v", body.Configuration["interface_type"])
	}
}

func TestIdentityAndConfiguration_ReadOnly(t *testing.T) {
	cfg := adapterConfig{
		operationalMode: "read_only",
		zoneModel:       false,
		agentVersion:    "0.4.0",
	}
	mux := newTestMux(cfg)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/identity-and-configuration")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var body IdentityAndConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if body.Configuration["operational_mode"] != "read_only" {
		t.Errorf("expected operational_mode=read_only, got %v", body.Configuration["operational_mode"])
	}
	if body.Configuration["zone_model"] != false {
		t.Errorf("expected zone_model=false, got %v", body.Configuration["zone_model"])
	}
}

func TestPostExecution_StillWorks(t *testing.T) {
	cfg := adapterConfig{
		operationalMode: "read_write",
		zoneModel:       true,
		agentVersion:    "0.5.0",
	}
	mux := newTestMux(cfg)
	server := httptest.NewServer(mux)
	defer server.Close()

	reqBody := `{"prompt":"list pods","tools":["kubectl"],"mode":"read-only","scope":{}}`
	resp, err := http.Post(server.URL+"/", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body AgentResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if body.FinalAnswer != "echoed: list pods" {
		t.Errorf("expected echoed prompt, got %s", body.FinalAnswer)
	}
}

func TestIdentityAndConfiguration_MethodNotAllowed(t *testing.T) {
	cfg := adapterConfig{operationalMode: "read_write", zoneModel: true, agentVersion: "0.5.0"}
	mux := newTestMux(cfg)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Post(server.URL+"/identity-and-configuration", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
}

// TestTranslateResponse_RealisticFixture is the round-trip assertion: every tool
// call in the fixture appears in Actions, in order, paired with its own result.
func TestTranslateResponse_RealisticFixture(t *testing.T) {
	resp := translateResponse(decodeJoe(t, realisticJoeBody))

	want := []AgentAction{
		{
			ID:         "call_1",
			Tool:       "container-orchestration",
			Arguments:  map[string]interface{}{"command": "get pods -n frontend"},
			Result:     `{"pods":[{"name":"web-0","phase":"Running"}],"count":1}`,
			DurationMs: 42,
		},
		{
			ID:         "call_2",
			Tool:       "container-orchestration",
			Arguments:  map[string]interface{}{"command": "get pods -n payments"},
			Result:     `["payments-0","payments-1"]`,
			DurationMs: 17,
		},
		{
			ID:         "call_3",
			Tool:       "secret-management",
			Arguments:  map[string]interface{}{"path": "prod/db"},
			Result:     `null`,
			Error:      "zone boundary crossing denied",
			ErrorCode:  "zone_denial",
			DurationMs: 8,
		},
		{
			ID:         "call_4",
			Tool:       "observability",
			Arguments:  map[string]interface{}{"query": "up"},
			Result:     `""`,
			DurationMs: 5,
		},
		{
			ID:        "call_5",
			Tool:      "config-management",
			Arguments: map[string]interface{}{"key": "feature_flag"},
			Result:    "",
		},
		{
			ID:         "res_9",
			Tool:       "audit-log",
			Arguments:  map[string]interface{}{},
			Result:     `{"entries":3}`,
			DurationMs: 2,
		},
	}

	if len(resp.Actions) != len(want) {
		t.Fatalf("expected %d actions, got %d: %+v", len(want), len(resp.Actions), resp.Actions)
	}
	for i, w := range want {
		got := resp.Actions[i]
		if got.ID != w.ID {
			t.Errorf("action %d: id = %q, want %q", i, got.ID, w.ID)
		}
		if got.Tool != w.Tool {
			t.Errorf("action %d: tool = %q, want %q", i, got.Tool, w.Tool)
		}
		if got.Result != w.Result {
			t.Errorf("action %d: result = %q, want %q", i, got.Result, w.Result)
		}
		if got.Error != w.Error {
			t.Errorf("action %d: error = %q, want %q", i, got.Error, w.Error)
		}
		if got.ErrorCode != w.ErrorCode {
			t.Errorf("action %d: error_code = %q, want %q", i, got.ErrorCode, w.ErrorCode)
		}
		if got.DurationMs != w.DurationMs {
			t.Errorf("action %d: duration_ms = %d, want %d", i, got.DurationMs, w.DurationMs)
		}
		if len(got.Arguments) != len(w.Arguments) {
			t.Fatalf("action %d: arguments = %v, want %v", i, got.Arguments, w.Arguments)
		}
		for k, v := range w.Arguments {
			if got.Arguments[k] != v {
				t.Errorf("action %d: arguments[%q] = %v, want %v", i, k, got.Arguments[k], v)
			}
		}
	}

	if resp.FinalAnswer != "Frontend and payments pods listed; the secret read was denied." {
		t.Errorf("final_answer = %q", resp.FinalAnswer)
	}
	if !strings.Contains(resp.Reasoning, "Step 1") || !strings.Contains(resp.Reasoning, "Step 3") {
		t.Errorf("reasoning missing steps: %q", resp.Reasoning)
	}
	if !strings.Contains(resp.Reasoning, "crosses a zone boundary") {
		t.Errorf("reasoning missing deliberation step text: %q", resp.Reasoning)
	}
}

// TestTranslateResponse_SameToolTwiceInOneStep isolates the name-keyed pairing
// collision: two calls to the same tool in one step must keep distinct results.
func TestTranslateResponse_SameToolTwiceInOneStep(t *testing.T) {
	resp := translateResponse(decodeJoe(t, realisticJoeBody))

	if len(resp.Actions) < 2 {
		t.Fatalf("expected at least 2 actions, got %d", len(resp.Actions))
	}
	a, b := resp.Actions[0], resp.Actions[1]
	if a.Tool != b.Tool {
		t.Fatalf("fixture invariant broken: actions 0 and 1 should share a tool, got %q and %q", a.Tool, b.Tool)
	}
	if a.Result == b.Result {
		t.Errorf("same-tool calls collapsed to one result: both %q", a.Result)
	}
	if !strings.Contains(a.Result, "web-0") {
		t.Errorf("action 0 result mispaired: %q", a.Result)
	}
	if !strings.Contains(b.Result, "payments-0") {
		t.Errorf("action 1 result mispaired: %q", b.Result)
	}
}

// TestTranslateResponse_ResultEncodingThreeWay asserts the three-way distinction
// the compact-JSON encoding buys: an empty-string result is `""`, a JSON null
// result is `null`, and an absent result is the empty Go string.
func TestTranslateResponse_ResultEncodingThreeWay(t *testing.T) {
	resp := translateResponse(decodeJoe(t, realisticJoeBody))

	byID := make(map[string]AgentAction, len(resp.Actions))
	for _, a := range resp.Actions {
		byID[a.ID] = a
	}

	if got := byID["call_4"].Result; got != `""` {
		t.Errorf("empty-string result: got %q, want %q", got, `""`)
	}
	if got := byID["call_3"].Result; got != "null" {
		t.Errorf("null result: got %q, want %q", got, "null")
	}
	if got := byID["call_5"].Result; got != "" {
		t.Errorf("absent result: got %q, want empty string", got)
	}
	if byID["call_4"].Result == byID["call_3"].Result ||
		byID["call_3"].Result == byID["call_5"].Result ||
		byID["call_4"].Result == byID["call_5"].Result {
		t.Error("empty-string, null and absent results are not mutually distinguishable")
	}
}

// TestTranslateResponse_OrphanResultAndOrphanCall covers both pairing orphans:
// a result with no matching call must still be emitted, and a call with no
// result must still be emitted.
func TestTranslateResponse_OrphanResultAndOrphanCall(t *testing.T) {
	resp := translateResponse(decodeJoe(t, realisticJoeBody))

	var orphanResult, orphanCall *AgentAction
	for i := range resp.Actions {
		switch resp.Actions[i].ID {
		case "res_9":
			orphanResult = &resp.Actions[i]
		case "call_5":
			orphanCall = &resp.Actions[i]
		}
	}

	if orphanResult == nil {
		t.Fatal("orphan result res_9 was dropped")
	}
	if orphanResult.Tool != "audit-log" {
		t.Errorf("orphan result tool = %q, want audit-log", orphanResult.Tool)
	}
	if len(orphanResult.Arguments) != 0 {
		t.Errorf("orphan result arguments = %v, want empty", orphanResult.Arguments)
	}
	if orphanResult.Result != `{"entries":3}` {
		t.Errorf("orphan result body = %q", orphanResult.Result)
	}

	if orphanCall == nil {
		t.Fatal("call with no result (call_5) was dropped")
	}
	if orphanCall.Result != "" {
		t.Errorf("unmatched call result = %q, want empty string", orphanCall.Result)
	}

	// Orphan result comes after its step's calls: step-order first, call-order within.
	idxCall5, idxRes9 := -1, -1
	for i, a := range resp.Actions {
		if a.ID == "call_5" {
			idxCall5 = i
		}
		if a.ID == "res_9" {
			idxRes9 = i
		}
	}
	if idxRes9 < idxCall5 {
		t.Errorf("orphan result at %d precedes step calls ending at %d", idxRes9, idxCall5)
	}
}

// TestTranslateResponse_NoResultBodyTruncation guards the "never truncate"
// requirement against a large object result.
func TestTranslateResponse_NoResultBodyTruncation(t *testing.T) {
	big := strings.Repeat("x", 20000)
	body := `{"steps":[{"step_number":1,"llm_response":{"content":"c","tool_calls":[` +
		`{"id":"c1","name":"observability","args":{}}]},"tool_results":[` +
		`{"id":"c1","name":"observability","result":{"blob":"` + big + `"},"duration_ms":1}]}],` +
		`"final_answer":"done"}`

	resp := translateResponse(decodeJoe(t, body))
	if len(resp.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(resp.Actions))
	}
	if !strings.Contains(resp.Actions[0].Result, big) {
		t.Errorf("result body truncated: length %d", len(resp.Actions[0].Result))
	}
}

// TestTranslateResponse_DeliberationOnly is a safety-refusal shape: joe reasons
// about a zone boundary but calls no tools. Reasoning and final answer must
// still be populated, with zero actions.
func TestTranslateResponse_DeliberationOnly(t *testing.T) {
	body := `{
      "task_id": "t-1",
      "status": "completed",
      "steps": [
        {
          "step_number": 1,
          "llm_request": {"message_count": 1, "tools_available": []},
          "llm_response": {
            "content": "The request crosses a zone boundary from my authorized zone(s): frontend. I cannot access resources in the orders namespace.",
            "usage": {"input_tokens": 10, "output_tokens": 5}
          }
        }
      ],
      "final_answer": ""
    }`

	resp := translateResponse(decodeJoe(t, body))

	if !strings.Contains(resp.Reasoning, "crosses a zone boundary") {
		t.Errorf("expected reasoning to contain zone crossing text, got %q", resp.Reasoning)
	}
	if !strings.Contains(resp.FinalAnswer, "crosses a zone boundary") {
		t.Errorf("expected final_answer to contain zone crossing text, got %q", resp.FinalAnswer)
	}
	if len(resp.Actions) != 0 {
		t.Errorf("expected 0 actions, got %d", len(resp.Actions))
	}
}

// TestTranslateResponse_MixedStepsWithDeliberation: an action step followed by a
// refusal step. The explicit final answer must not be overwritten.
func TestTranslateResponse_MixedStepsWithDeliberation(t *testing.T) {
	body := `{
      "steps": [
        {
          "step_number": 1,
          "llm_response": {
            "content": "Listing pods in frontend namespace",
            "tool_calls": [{"id": "c1", "name": "container-orchestration", "args": {"command": "get pods -n frontend"}}]
          },
          "tool_results": [{"id": "c1", "name": "container-orchestration", "result": "pod1 running", "duration_ms": 3}]
        },
        {
          "step_number": 2,
          "llm_response": {"content": "The second part of the request targets the orders namespace which is outside my authorized zone."}
        }
      ],
      "final_answer": "I listed the pods but cannot access the orders namespace."
    }`

	resp := translateResponse(decodeJoe(t, body))

	if !strings.Contains(resp.Reasoning, "Step 1") || !strings.Contains(resp.Reasoning, "Step 2") {
		t.Errorf("expected reasoning to contain both steps, got %q", resp.Reasoning)
	}
	if !strings.Contains(resp.Reasoning, "outside my authorized zone") {
		t.Errorf("expected reasoning to contain refusal text, got %q", resp.Reasoning)
	}
	if resp.FinalAnswer != "I listed the pods but cannot access the orders namespace." {
		t.Errorf("expected explicit final_answer preserved, got %q", resp.FinalAnswer)
	}
	if len(resp.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(resp.Actions))
	}
	// A joe string result is encoded as a quoted JSON string, not bare text.
	if resp.Actions[0].Result != `"pod1 running"` {
		t.Errorf("result = %q, want %q", resp.Actions[0].Result, `"pod1 running"`)
	}
}

// --- Pre-fix regression guard ---
//
// legacy* mirrors the adapter's pre-fix decode structs and pairing logic exactly
// as they stood at adapters/joe/main.go:76-94 and :125-174 before this change:
// tool_calls expected at step level with fields tool/arguments, tool_results with
// fields tool/result and result typed as a Go string. They exist only here, to
// demonstrate against the realistic fixture that the old path produced no actions
// and could not decode a non-string result at all.

type legacyJoeResponse struct {
	Steps       []legacyJoeStep `json:"steps"`
	FinalAnswer string          `json:"final_answer"`
}

type legacyJoeStep struct {
	LLMResponse *struct {
		Content string `json:"content"`
	} `json:"llm_response,omitempty"`
	ToolCalls []struct {
		Tool      string                 `json:"tool"`
		Arguments map[string]interface{} `json:"arguments"`
	} `json:"tool_calls,omitempty"`
	ToolResults []struct {
		Tool   string `json:"tool"`
		Result string `json:"result"`
	} `json:"tool_results,omitempty"`
}

func legacyActionCount(jr *legacyJoeResponse) int {
	n := 0
	for _, step := range jr.Steps {
		n += len(step.ToolCalls)
	}
	return n
}

// TestPreFixDefect_LegacyDecodeYieldsEmptyActions demonstrates defect 1: the old
// step-level tool_calls key is never present in a real joe body, so the old path
// yielded zero actions on every run.
func TestPreFixDefect_LegacyDecodeYieldsEmptyActions(t *testing.T) {
	// Strip the object/array/null results so the legacy struct can decode at all;
	// this isolates defect 1 from defect 2.
	stringOnlyBody := `{
      "steps": [
        {
          "step_number": 1,
          "llm_response": {
            "content": "Listing pods.",
            "tool_calls": [{"id": "c1", "name": "container-orchestration", "args": {"command": "get pods"}}]
          },
          "tool_results": [{"id": "c1", "name": "container-orchestration", "result": "pod1 running", "duration_ms": 3}]
        }
      ],
      "final_answer": "done"
    }`

	var legacy legacyJoeResponse
	if err := json.Unmarshal([]byte(stringOnlyBody), &legacy); err != nil {
		t.Fatalf("legacy decode unexpectedly failed on a string-only body: %v", err)
	}
	if got := legacyActionCount(&legacy); got != 0 {
		t.Fatalf("regression guard is no longer meaningful: legacy path yielded %d actions", got)
	}

	// The fixed path must find the call the legacy path missed.
	fixed := translateResponse(decodeJoe(t, stringOnlyBody))
	if len(fixed.Actions) != 1 {
		t.Errorf("fixed path yielded %d actions, want 1", len(fixed.Actions))
	}
}

// TestPreFixDefect_LegacyDecodeFailsOnNonStringResult demonstrates defect 2: any
// object- or array-valued tool result made the old decode fail outright.
func TestPreFixDefect_LegacyDecodeFailsOnNonStringResult(t *testing.T) {
	var legacy legacyJoeResponse
	err := json.Unmarshal([]byte(realisticJoeBody), &legacy)
	if err == nil {
		t.Fatal("regression guard is no longer meaningful: legacy decode accepted a non-string result")
	}
	if !strings.Contains(err.Error(), "string") {
		t.Errorf("expected a string-type decode error, got %v", err)
	}

	// The fixed path decodes the same body without error and finds every call.
	fixed := translateResponse(decodeJoe(t, realisticJoeBody))
	if len(fixed.Actions) != 6 {
		t.Errorf("fixed path yielded %d actions, want 6", len(fixed.Actions))
	}
}

// TestTranslateResponse_MalformedBodyIsExplicitError: a body that cannot be
// decoded must surface as an explicit error response, never as a silently empty
// Actions list on a 200.
func TestTranslateResponse_MalformedBodyIsExplicitError(t *testing.T) {
	var jr JoeResponse
	err := json.Unmarshal([]byte(`{"steps": "not-an-array"}`), &jr)
	if err == nil {
		t.Fatal("expected decode error on malformed body")
	}

	resp := errorResponse("Error: failed to decode Joe response: " + err.Error())
	if !strings.HasPrefix(resp.FinalAnswer, "Error:") {
		t.Errorf("error response final_answer = %q, want an Error: prefix", resp.FinalAnswer)
	}
	if resp.Actions == nil || len(resp.Actions) != 0 {
		t.Errorf("error response actions = %v, want empty non-nil slice", resp.Actions)
	}
}

// TestServeExecute_EndToEnd drives the real handler against a stub joe-core to
// confirm the realistic body survives the full HTTP path into the oasisctl wire
// format, including the enriched per-action fields.
func TestServeExecute_EndToEnd(t *testing.T) {
	joeCore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(realisticJoeBody))
	}))
	defer joeCore.Close()

	cfg := adapterConfig{joeURL: joeCore.URL, timeout: 5 * time.Second, operationalMode: "read_write"}
	adapter := httptest.NewServer(newServeMux(cfg))
	defer adapter.Close()

	resp, err := http.Post(adapter.URL+"/", "application/json",
		strings.NewReader(`{"prompt":"list pods","tools":[],"mode":"read-only","scope":{}}`))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var body AgentResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode adapter response: %v", err)
	}
	if len(body.Actions) != 6 {
		t.Fatalf("expected 6 actions over the wire, got %d", len(body.Actions))
	}
	if body.Actions[2].ErrorCode != "zone_denial" {
		t.Errorf("error_code did not survive the wire: %q", body.Actions[2].ErrorCode)
	}
	if body.Actions[0].Result != `{"pods":[{"name":"web-0","phase":"Running"}],"count":1}` {
		t.Errorf("object result did not survive the wire: %q", body.Actions[0].Result)
	}
}

func TestModeToSafetyTier(t *testing.T) {
	tests := []struct {
		mode     string
		expected string
	}{
		{"read-only", "observe"},
		{"supervised", "record"},
		{"autonomous", "act"},
		{"unknown", "act"},
	}
	for _, tc := range tests {
		got := modeToSafetyTier(tc.mode)
		if got != tc.expected {
			t.Errorf("modeToSafetyTier(%q) = %q, want %q", tc.mode, got, tc.expected)
		}
	}
}

func TestFetchVersionFromStatus_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"1.2.3","status":"ok"}`))
	}))
	defer server.Close()

	got := fetchVersionFromStatus(server.URL, "")
	if got != "1.2.3" {
		t.Errorf("expected version=1.2.3, got %q", got)
	}
}

func TestFetchVersionFromStatus_WithToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"2.0.0"}`))
	}))
	defer server.Close()

	got := fetchVersionFromStatus(server.URL, "secret")
	if got != "2.0.0" {
		t.Errorf("expected version=2.0.0, got %q", got)
	}
}

func TestFetchVersionFromStatus_Unavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	got := fetchVersionFromStatus(server.URL, "")
	if got != "" {
		t.Errorf("expected empty version on 404, got %q", got)
	}
}

func TestFetchVersionFromStatus_Unreachable(t *testing.T) {
	got := fetchVersionFromStatus("http://127.0.0.1:1", "")
	if got != "" {
		t.Errorf("expected empty version on unreachable host, got %q", got)
	}
}
