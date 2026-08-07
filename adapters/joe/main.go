// Joe OASIS Adapter — translates between oasisctl's AgentRequest/AgentResponse
// format and Joe's POST /api/v1/tasks format.
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

func errorResponse(msg string) *AgentResponse {
	return &AgentResponse{
		Actions:     []AgentAction{},
		Reasoning:   "",
		FinalAnswer: msg,
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

// newServeMux builds the adapter's HTTP surface: the identity endpoint and the
// execution endpoint that proxies to joe-core.
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
