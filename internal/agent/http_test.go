package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

func TestReportIdentityAndConfiguration_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/identity-and-configuration" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}

		resp := map[string]interface{}{
			"identity": map[string]interface{}{
				"name":        "joe",
				"version":     "0.4.2",
				"description": "AI infrastructure copilot",
			},
			"configuration": map[string]interface{}{
				"operational_mode": "read_write",
				"zone_model":       true,
				"interface_type":   "cli",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewHTTPClient(server.URL, "test-token")
	identity, config, err := client.ReportIdentityAndConfiguration(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if identity.Name != "joe" {
		t.Errorf("expected name joe, got %s", identity.Name)
	}
	if identity.Version != "0.4.2" {
		t.Errorf("expected version 0.4.2, got %s", identity.Version)
	}
	if identity.Description != "AI infrastructure copilot" {
		t.Errorf("expected description, got %s", identity.Description)
	}
	if config["operational_mode"] != "read_write" {
		t.Errorf("expected operational_mode=read_write, got %v", config["operational_mode"])
	}
	if config["zone_model"] != true {
		t.Errorf("expected zone_model=true, got %v", config["zone_model"])
	}
}

func TestReportIdentityAndConfiguration_404(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewHTTPClient(server.URL, "")
	_, _, err := client.ReportIdentityAndConfiguration(context.Background())
	if err == nil {
		t.Error("expected error for 404 response")
	}
}

func TestReportIdentityAndConfiguration_BearerToken(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		resp := map[string]interface{}{
			"identity":      map[string]interface{}{"name": "test", "version": "1.0"},
			"configuration": map[string]interface{}{},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewHTTPClient(server.URL, "my-secret")
	_, _, _ = client.ReportIdentityAndConfiguration(context.Background())

	if gotAuth != "Bearer my-secret" {
		t.Errorf("expected Bearer my-secret, got %s", gotAuth)
	}
}

// TestExecute_EnrichedActionFields verifies the per-action wire fields an
// adapter may report — id, error, error_code, duration_ms — reach the domain
// type, and that a compact-JSON result string is carried through verbatim.
func TestExecute_EnrichedActionFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"actions": [
				{"id":"call_1","tool":"container-orchestration","arguments":{"command":"get pods"},
				 "result":"{\"count\":1}","duration_ms":42},
				{"id":"call_2","tool":"secret-management","arguments":{"path":"prod/db"},
				 "result":"null","error":"denied","error_code":"zone_denial","duration_ms":8}
			],
			"reasoning": "r",
			"final_answer": "f"
		}`))
	}))
	defer server.Close()

	client := NewHTTPClient(server.URL, "")
	resp, err := client.Execute(context.Background(), evaluation.AgentRequest{Prompt: "p"})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if len(resp.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(resp.Actions))
	}

	first := resp.Actions[0]
	if first.ID != "call_1" || first.DurationMs != 42 {
		t.Errorf("first action = %+v", first)
	}
	if first.Result != `{"count":1}` {
		t.Errorf("first action result = %q", first.Result)
	}

	second := resp.Actions[1]
	if second.Error != "denied" || second.ErrorCode != "zone_denial" {
		t.Errorf("second action error fields = %q / %q", second.Error, second.ErrorCode)
	}
	if second.Result != "null" {
		t.Errorf("second action result = %q, want null", second.Result)
	}
}

// TestExecute_ObservedModel pins the decode boundary that turns a reported model
// into an optional value. This is the ONLY place absent-or-empty collapses to
// nil, and the collapse is what keeps the evidence artifact's observed_model an
// explicit JSON null for an agent that reports no model — an empty string there
// would read as an observation of a model named "".
func TestExecute_ObservedModel(t *testing.T) {
	tests := []struct {
		name string
		body string
		want *string
	}{
		{
			name: "reported model reaches the domain type",
			body: `{"actions":[],"reasoning":"r","final_answer":"f","model":"claude-sonnet-4-20250514"}`,
			want: strPtr("claude-sonnet-4-20250514"),
		},
		{
			name: "field absent yields nil, never an empty string",
			body: `{"actions":[],"reasoning":"r","final_answer":"f"}`,
			want: nil,
		},
		{
			name: "field empty yields nil, never an empty string",
			body: `{"actions":[],"reasoning":"r","final_answer":"f","model":""}`,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client := NewHTTPClient(server.URL, "")
			resp, err := client.Execute(context.Background(), evaluation.AgentRequest{Prompt: "p"})
			if err != nil {
				t.Fatalf("execute failed: %v", err)
			}

			switch {
			case tt.want == nil && resp.Model != nil:
				t.Errorf("Model = %q, want nil", *resp.Model)
			case tt.want != nil && resp.Model == nil:
				t.Errorf("Model = nil, want %q", *tt.want)
			case tt.want != nil && *resp.Model != *tt.want:
				t.Errorf("Model = %q, want %q", *resp.Model, *tt.want)
			}
		})
	}
}

func strPtr(s string) *string { return &s }
