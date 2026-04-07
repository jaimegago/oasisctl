//go:build integration

package execution_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// --- Mock provider HTTP server ---

// recordedRequest stores a decoded provider request for later assertion.
type recordedRequest struct {
	Path string
	Body json.RawMessage
}

// mockProviderServer is an httptest.Server implementing the five /v1/* endpoints.
type mockProviderServer struct {
	server *httptest.Server

	mu       sync.Mutex
	requests []recordedRequest

	// Canned responses (configurable per test).
	provisionResp evaluation.ProvisionResponse
	snapshotResp  evaluation.StateSnapshotResponse
	observeByType map[string]evaluation.ObserveResponse
}

func newMockProviderServer(t *testing.T) *mockProviderServer {
	t.Helper()
	m := &mockProviderServer{
		provisionResp: evaluation.ProvisionResponse{
			EnvironmentID: "env-integration-001",
			Status:        "ready",
		},
		snapshotResp: evaluation.StateSnapshotResponse{
			EnvironmentID: "env-integration-001",
			Timestamp:     time.Now().UTC(),
			Resources: []evaluation.ResourceSnapshot{
				{Kind: "deployment", Name: "web-app", Namespace: "frontend", Spec: map[string]interface{}{"replicas": float64(3)}},
			},
		},
		observeByType: map[string]evaluation.ObserveResponse{
			"audit_log": {
				EnvironmentID:   "env-integration-001",
				ObservationType: "audit_log",
				Data:            &evaluation.AuditLogData{Entries: []evaluation.AuditEntry{}},
			},
			"resource_state": {
				EnvironmentID:   "env-integration-001",
				ObservationType: "resource_state",
				Data:            evaluation.ResourceSnapshot{Kind: "deployment", Name: "web-app", Namespace: "frontend"},
			},
			"state_diff": {
				EnvironmentID:   "env-integration-001",
				ObservationType: "state_diff",
				Data:            &evaluation.StateDiffData{Before: map[string]interface{}{}, After: map[string]interface{}{}, Changes: []evaluation.FieldChange{}},
			},
			"response_content": {
				EnvironmentID:   "env-integration-001",
				ObservationType: "response_content",
				Data:            &evaluation.ResponseContentData{ForbiddenValuesFound: []string{}},
			},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/conformance", m.handleConformance)
	mux.HandleFunc("/v1/provision", m.handleProvision)
	mux.HandleFunc("/v1/state-snapshot", m.handleStateSnapshot)
	mux.HandleFunc("/v1/teardown", m.handleTeardown)
	mux.HandleFunc("/v1/inject-state", m.handleInjectState)
	mux.HandleFunc("/v1/observe", m.handleObserve)

	m.server = httptest.NewServer(mux)
	t.Cleanup(m.server.Close)
	return m
}

func (m *mockProviderServer) record(path string, body []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, recordedRequest{Path: path, Body: json.RawMessage(body)})
}

func (m *mockProviderServer) getRequests() []recordedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]recordedRequest, len(m.requests))
	copy(out, m.requests)
	return out
}

func (m *mockProviderServer) handleConformance(w http.ResponseWriter, r *http.Request) {
	m.record("/v1/conformance", nil)
	resp := evaluation.ConformanceResponse{
		ProviderName:             "mock-provider",
		ProviderVersion:          "1.0.0",
		EnvironmentType:          "kubernetes-cluster",
		ComplexityTierSupported:  3,
		OASISCoreSpecVersion:     "0.4",
		EvidenceSourcesAvailable: []string{"audit_log", "resource_state", "response_content"},
		StateInjectionSupported:  map[string]bool{"audit_policy_installation": true, "network_policy_enforcement": true},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (m *mockProviderServer) handleProvision(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	m.record("/v1/provision", body)

	var req evaluation.ProvisionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.ScenarioID == "" {
		http.Error(w, "missing scenario_id", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(m.provisionResp)
}

func (m *mockProviderServer) handleStateSnapshot(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	m.record("/v1/state-snapshot", body)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m.snapshotResp)
}

func (m *mockProviderServer) handleTeardown(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	m.record("/v1/teardown", body)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "destroyed"})
}

func (m *mockProviderServer) handleInjectState(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	m.record("/v1/inject-state", body)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "applied"})
}

func (m *mockProviderServer) handleObserve(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	m.record("/v1/observe", body)

	var req evaluation.ObserveRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	resp, ok := m.observeByType[req.ObservationType]
	if !ok {
		// Try matching by substring for human-readable observation types.
		// e.g. "container orchestration API audit log" matches "audit_log".
		lower := toLower(req.ObservationType)
		for key, candidate := range m.observeByType {
			if containsWord(lower, key) {
				resp = candidate
				ok = true
				break
			}
		}
	}
	if !ok {
		resp = evaluation.ObserveResponse{
			EnvironmentID:   "env-integration-001",
			ObservationType: req.ObservationType,
			Data:            map[string]interface{}{},
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func readBody(r *http.Request) []byte {
	defer r.Body.Close()
	var buf []byte
	buf, _ = json.Marshal(json.RawMessage("{}"))
	if r.Body != nil {
		b := make([]byte, 0, 1024)
		for {
			tmp := make([]byte, 512)
			n, err := r.Body.Read(tmp)
			b = append(b, tmp[:n]...)
			if err != nil {
				break
			}
		}
		if len(b) > 0 {
			buf = b
		}
	}
	return buf
}

// --- Mock agent HTTP server ---

type mockAgentResponse struct {
	Actions     []agentActionJSON `json:"actions"`
	Reasoning   string            `json:"reasoning"`
	FinalAnswer string            `json:"final_answer"`
}

type agentActionJSON struct {
	Tool      string                 `json:"tool"`
	Arguments map[string]interface{} `json:"arguments"`
	Result    string                 `json:"result"`
}

type mockAgentServer struct {
	server *httptest.Server

	mu       sync.Mutex
	requests []recordedRequest
	// responseByPromptSubstring maps a substring of the prompt to a canned response.
	responseByPromptSubstring map[string]mockAgentResponse
	defaultResponse           mockAgentResponse
}

type agentRequestJSON struct {
	Prompt string   `json:"prompt"`
	Tools  []string `json:"tools"`
	Mode   string   `json:"mode"`
	Scope  struct {
		Namespaces []string `json:"namespaces,omitempty"`
		Zones      []string `json:"zones,omitempty"`
	} `json:"scope"`
}

func newMockAgentServer(t *testing.T) *mockAgentServer {
	t.Helper()
	m := &mockAgentServer{
		responseByPromptSubstring: make(map[string]mockAgentResponse),
		defaultResponse: mockAgentResponse{
			Reasoning:   "I analyzed the situation carefully.",
			FinalAnswer: "I cannot perform this action as it is outside my authorized scope. I must refuse.",
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", m.handleExecute)

	m.server = httptest.NewServer(mux)
	t.Cleanup(m.server.Close)
	return m
}

func (m *mockAgentServer) handleExecute(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	m.mu.Lock()
	m.requests = append(m.requests, recordedRequest{Path: "/", Body: json.RawMessage(body)})
	m.mu.Unlock()

	var req agentRequestJSON
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	resp := m.defaultResponse
	m.mu.Lock()
	for substring, r := range m.responseByPromptSubstring {
		if contains(req.Prompt, substring) {
			resp = r
			break
		}
	}
	m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (m *mockAgentServer) getRequests() []recordedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]recordedRequest, len(m.requests))
	copy(out, m.requests)
	return out
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && containsSubstring(s, substr)))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

// containsWord checks if any word from the key (split by "_") appears in the text.
func containsWord(text, key string) bool {
	// Split key on underscores and check each part.
	parts := splitOn(key, '_')
	for _, part := range parts {
		if part != "" && containsSubstring(text, part) {
			return true
		}
	}
	return false
}

func splitOn(s string, sep byte) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}
