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

type AgentAction struct {
	Tool      string                 `json:"tool"`
	Arguments map[string]interface{} `json:"arguments"`
	Result    string                 `json:"result"`
}

// Joe request/response types.

type JoeRequest struct {
	Message string    `json:"message"`
	Config  JoeConfig `json:"config"`
}

type JoeConfig struct {
	SafetyTier string `json:"safety_tier"`
	Timeout    string `json:"timeout"`
}

type JoeResponse struct {
	Steps       []JoeStep `json:"steps"`
	FinalAnswer string    `json:"final_answer"`
}

type JoeStep struct {
	LLMResponse *JoeLLMResponse `json:"llm_response,omitempty"`
	ToolCalls   []JoeToolCall   `json:"tool_calls,omitempty"`
	ToolResults []JoeToolResult `json:"tool_results,omitempty"`
}

type JoeLLMResponse struct {
	Content string `json:"content"`
}

type JoeToolCall struct {
	Tool      string                 `json:"tool"`
	Arguments map[string]interface{} `json:"arguments"`
}

type JoeToolResult struct {
	Tool   string `json:"tool"`
	Result string `json:"result"`
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

func translateResponse(jr *JoeResponse) *AgentResponse {
	resp := &AgentResponse{
		Actions:     []AgentAction{},
		FinalAnswer: jr.FinalAnswer,
	}

	var reasoningParts []string
	stepNum := 0

	for _, step := range jr.Steps {
		// Collect reasoning from steps that have tool calls (intermediate steps).
		if len(step.ToolCalls) > 0 && step.LLMResponse != nil && step.LLMResponse.Content != "" {
			stepNum++
			reasoningParts = append(reasoningParts, fmt.Sprintf("Step %d: %s", stepNum, step.LLMResponse.Content))
		}

		// Build a result lookup from tool results in this step.
		resultByTool := make(map[string]string)
		for _, tr := range step.ToolResults {
			resultByTool[tr.Tool] = tr.Result
		}

		// Flatten tool calls into actions, pairing with results.
		for _, tc := range step.ToolCalls {
			action := AgentAction{
				Tool:      tc.Tool,
				Arguments: tc.Arguments,
				Result:    resultByTool[tc.Tool],
			}
			resp.Actions = append(resp.Actions, action)
		}
	}

	resp.Reasoning = strings.Join(reasoningParts, "\n")
	return resp
}

func errorResponse(msg string) *AgentResponse {
	return &AgentResponse{
		Actions:     []AgentAction{},
		Reasoning:   "",
		FinalAnswer: msg,
	}
}

func main() {
	listen := flag.String("listen", ":8091", "address to listen on")
	joeURL := flag.String("joe-url", "", "Joe's HTTP API base URL (required)")
	joeToken := flag.String("joe-token", "", "bearer token for Joe's API")
	timeout := flag.Duration("timeout", 3*time.Minute, "per-request timeout")
	flag.Parse()

	if *joeURL == "" {
		log.Fatal("--joe-url is required")
	}

	joeEndpoint := strings.TrimRight(*joeURL, "/") + "/api/v1/tasks"
	client := &http.Client{Timeout: *timeout}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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
				SafetyTier: modeToSafetyTier(req.Mode),
				Timeout:    "2m",
			},
		}

		body, err := json.Marshal(joeReq)
		if err != nil {
			writeJSON(w, errorResponse(fmt.Sprintf("Error: failed to marshal request: %v", err)))
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), *timeout)
		defer cancel()

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, joeEndpoint, bytes.NewReader(body))
		if err != nil {
			writeJSON(w, errorResponse(fmt.Sprintf("Error: failed to create request: %v", err)))
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if *joeToken != "" {
			httpReq.Header.Set("Authorization", "Bearer "+*joeToken)
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
			writeJSON(w, errorResponse(fmt.Sprintf("Error: failed to decode Joe response: %v", err)))
			return
		}

		writeJSON(w, translateResponse(&joeResp))
	})

	log.Printf("joe-adapter listening on %s, forwarding to %s", *listen, joeEndpoint)
	log.Fatal(http.ListenAndServe(*listen, nil))
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("failed to write response: %v", err)
	}
}
