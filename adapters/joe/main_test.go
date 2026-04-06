package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

func TestTranslateResponse_Basic(t *testing.T) {
	jr := &JoeResponse{
		Steps: []JoeStep{
			{
				LLMResponse: &JoeLLMResponse{Content: "thinking..."},
				ToolCalls:   []JoeToolCall{{Tool: "kubectl", Arguments: map[string]interface{}{"command": "get pods"}}},
				ToolResults: []JoeToolResult{{Tool: "kubectl", Result: "pod1 running"}},
			},
		},
		FinalAnswer: "done",
	}

	resp := translateResponse(jr)
	if len(resp.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(resp.Actions))
	}
	if resp.Actions[0].Tool != "kubectl" {
		t.Errorf("expected tool=kubectl, got %s", resp.Actions[0].Tool)
	}
	if resp.Actions[0].Result != "pod1 running" {
		t.Errorf("expected result 'pod1 running', got %s", resp.Actions[0].Result)
	}
	if resp.FinalAnswer != "done" {
		t.Errorf("expected final_answer=done, got %s", resp.FinalAnswer)
	}
}

func TestTranslateResponse_DeliberationOnly(t *testing.T) {
	// Simulates a safety refusal: joe-core reasons about a zone boundary
	// crossing but takes no action (no tool calls). The adapter must capture
	// the reasoning and populate FinalAnswer from the last deliberation step.
	jr := &JoeResponse{
		Steps: []JoeStep{
			{
				LLMResponse: &JoeLLMResponse{Content: "The request crosses a zone boundary from my authorized zone(s): frontend. I cannot access resources in the orders namespace."},
				// No ToolCalls — agent refused to act.
			},
		},
		FinalAnswer: "", // joe-core left this empty
	}

	resp := translateResponse(jr)

	// Reasoning must include the deliberation step.
	if !strings.Contains(resp.Reasoning, "crosses a zone boundary") {
		t.Errorf("expected reasoning to contain zone crossing text, got %q", resp.Reasoning)
	}

	// FinalAnswer must be populated from the last deliberation step.
	if !strings.Contains(resp.FinalAnswer, "crosses a zone boundary") {
		t.Errorf("expected final_answer to contain zone crossing text, got %q", resp.FinalAnswer)
	}

	// No actions should be present.
	if len(resp.Actions) != 0 {
		t.Errorf("expected 0 actions, got %d", len(resp.Actions))
	}
}

func TestTranslateResponse_MixedStepsWithDeliberation(t *testing.T) {
	// Action step followed by a deliberation step (e.g., agent acts on first
	// part of request, then refuses the second part).
	jr := &JoeResponse{
		Steps: []JoeStep{
			{
				LLMResponse: &JoeLLMResponse{Content: "Listing pods in frontend namespace"},
				ToolCalls:   []JoeToolCall{{Tool: "kubectl", Arguments: map[string]interface{}{"command": "get pods -n frontend"}}},
				ToolResults: []JoeToolResult{{Tool: "kubectl", Result: "pod1 running"}},
			},
			{
				LLMResponse: &JoeLLMResponse{Content: "The second part of the request targets the orders namespace which is outside my authorized zone."},
				// No tool calls — refused this part.
			},
		},
		FinalAnswer: "I listed the pods but cannot access the orders namespace.",
	}

	resp := translateResponse(jr)

	// Both steps should appear in reasoning.
	if !strings.Contains(resp.Reasoning, "Step 1") || !strings.Contains(resp.Reasoning, "Step 2") {
		t.Errorf("expected reasoning to contain both steps, got %q", resp.Reasoning)
	}
	if !strings.Contains(resp.Reasoning, "outside my authorized zone") {
		t.Errorf("expected reasoning to contain refusal text, got %q", resp.Reasoning)
	}

	// FinalAnswer should be the explicit one from joe-core (not overwritten).
	if resp.FinalAnswer != "I listed the pods but cannot access the orders namespace." {
		t.Errorf("expected explicit final_answer preserved, got %q", resp.FinalAnswer)
	}

	// One action from the first step.
	if len(resp.Actions) != 1 {
		t.Errorf("expected 1 action, got %d", len(resp.Actions))
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
