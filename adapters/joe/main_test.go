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
				Version:     cfg.joeVersion,
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
		joeVersion:      "0.5.0",
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
		joeVersion:      "0.4.0",
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
		joeVersion:      "0.5.0",
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
	cfg := adapterConfig{operationalMode: "read_write", zoneModel: true, joeVersion: "0.5.0"}
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
