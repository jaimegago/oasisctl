package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// HTTPClient implements evaluation.AgentClient over HTTP/JSON.
type HTTPClient struct {
	endpointURL string
	token       string
	httpClient  *http.Client
}

func newHTTPClient(endpointURL, token string) *HTTPClient {
	return &HTTPClient{
		endpointURL: endpointURL,
		token:       token,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

type agentRequestBody struct {
	Prompt string   `json:"prompt"`
	Tools  []string `json:"tools"`
	Mode   string   `json:"mode"`
	Scope  struct {
		Namespaces []string `json:"namespaces,omitempty"`
		Zones      []string `json:"zones,omitempty"`
	} `json:"scope"`
}

type agentResponseBody struct {
	Actions []struct {
		ID         string                 `json:"id"`
		Tool       string                 `json:"tool"`
		Arguments  map[string]interface{} `json:"arguments"`
		Result     string                 `json:"result"`
		Error      string                 `json:"error"`
		ErrorCode  string                 `json:"error_code"`
		DurationMs int                    `json:"duration_ms"`
	} `json:"actions"`
	Reasoning   string `json:"reasoning"`
	FinalAnswer string `json:"final_answer"`
	// Model is the optional model identifier an adapter may report for the
	// execution. It is decoded as a plain string precisely so the two ways of
	// not reporting one — field absent, field empty — arrive here identically
	// and are collapsed to a nil *string exactly once, below.
	Model string `json:"model"`
	// RootCause, Discarded and ConclusionDeclared are the diagnostic
	// conclusion an adapter may report for the execution. Declaring one is an
	// adapter CAPABILITY like Model: an adapter that sends none of them leaves
	// all three at their zero values, which collapse to a nil Conclusion below
	// and are reported as an absence rather than scored.
	RootCause string `json:"root_cause"`
	Discarded []struct {
		Signal    string `json:"signal"`
		Rationale string `json:"rationale"`
	} `json:"discarded"`
	ConclusionDeclared bool `json:"conclusion_declared"`
	// EmptyAnswerGate is the optional empty-answer gate outcome an adapter may
	// report for the execution — "held", "not_held", or absent. Like Model it
	// is decoded as a plain string precisely so the two ways of not reporting
	// one — field absent, field empty — arrive here identically and are
	// collapsed to a nil *string exactly once, below.
	EmptyAnswerGate string `json:"empty_answer_gate"`
}

// Execute sends a request to the agent and returns its response.
func (c *HTTPClient) Execute(ctx context.Context, req evaluation.AgentRequest) (*evaluation.AgentResponse, error) {
	body := agentRequestBody{
		Prompt: req.Prompt,
		Tools:  req.Tools,
		Mode:   string(req.Mode),
	}
	body.Scope.Namespaces = req.Scope.Namespaces
	body.Scope.Zones = req.Scope.Zones

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, &evaluation.AgentError{Cause: fmt.Errorf("marshal request: %w", err)}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpointURL, bytes.NewReader(payload))
	if err != nil {
		return nil, &evaluation.AgentError{Cause: fmt.Errorf("create request: %w", err)}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, &evaluation.AgentError{Cause: fmt.Errorf("execute request: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, &evaluation.AgentError{Cause: fmt.Errorf("agent returned status %d", resp.StatusCode)}
	}

	var respBody agentResponseBody
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return nil, &evaluation.AgentError{Cause: fmt.Errorf("decode response: %w", err)}
	}

	agentResp := &evaluation.AgentResponse{
		Reasoning:   respBody.Reasoning,
		FinalAnswer: respBody.FinalAnswer,
	}
	// The one place a reported model becomes an optional value. An agent that
	// reports no model — absent field or empty string — yields nil, which the
	// evidence artifact records as an explicit JSON null; an empty string would
	// read as a real observation of a model named "".
	if respBody.Model != "" {
		model := respBody.Model
		agentResp.Model = &model
	}
	// The same collapse for the empty-answer gate outcome, and for the same
	// reason: an agent that reports none — absent field or empty string —
	// yields nil, which the evidence artifact records as an explicit JSON null.
	// An empty string would read as an observed outcome named "".
	if respBody.EmptyAnswerGate != "" {
		gate := respBody.EmptyAnswerGate
		agentResp.EmptyAnswerGate = &gate
	}
	// The one place a reported conclusion becomes an optional value. An adapter
	// that declares nothing yields nil, which every behaviour keyed on the
	// declaration reports as UNASSESSABLE — never as a wrong diagnosis.
	//
	// The gate is ConclusionDeclared and NOT the emptiness of the fields, which
	// is the whole point of the flag travelling separately: an agent that
	// declared a conclusion and ruled nothing out has answered, and an agent
	// that declared nothing has not, and both arrive here with an empty list.
	if respBody.ConclusionDeclared || respBody.RootCause != "" || len(respBody.Discarded) > 0 {
		conclusion := &evaluation.DiagnosticConclusion{
			RootCause: respBody.RootCause,
			Discarded: make([]evaluation.DiscardedSignal, 0, len(respBody.Discarded)),
		}
		for _, d := range respBody.Discarded {
			conclusion.Discarded = append(conclusion.Discarded, evaluation.DiscardedSignal{
				Signal:    d.Signal,
				Rationale: d.Rationale,
			})
		}
		agentResp.Conclusion = conclusion
	}
	for _, a := range respBody.Actions {
		agentResp.Actions = append(agentResp.Actions, evaluation.AgentAction{
			ID:         a.ID,
			Tool:       a.Tool,
			Arguments:  a.Arguments,
			Result:     a.Result,
			Error:      a.Error,
			ErrorCode:  a.ErrorCode,
			DurationMs: a.DurationMs,
		})
	}

	return agentResp, nil
}

// identityAndConfigResponse is the JSON shape from the adapter's identity endpoint.
type identityAndConfigResponse struct {
	Identity struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description"`
	} `json:"identity"`
	Configuration map[string]interface{} `json:"configuration"`
}

// ReportIdentityAndConfiguration queries the agent adapter for identity and configuration.
func (c *HTTPClient) ReportIdentityAndConfiguration(ctx context.Context) (evaluation.AgentIdentity, evaluation.AgentConfiguration, error) {
	url := strings.TrimSuffix(c.endpointURL, "/") + "/identity-and-configuration"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return evaluation.AgentIdentity{}, nil, &evaluation.AgentError{Cause: fmt.Errorf("create identity request: %w", err)}
	}
	if c.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return evaluation.AgentIdentity{}, nil, &evaluation.AgentError{Cause: fmt.Errorf("identity request: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return evaluation.AgentIdentity{}, nil, &evaluation.AgentError{
			Cause: fmt.Errorf("agent adapter does not implement GET /identity-and-configuration (returned 404); this endpoint is required"),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return evaluation.AgentIdentity{}, nil, &evaluation.AgentError{
			Cause: fmt.Errorf("identity endpoint returned status %d", resp.StatusCode),
		}
	}

	var body identityAndConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return evaluation.AgentIdentity{}, nil, &evaluation.AgentError{Cause: fmt.Errorf("decode identity response: %w", err)}
	}

	identity := evaluation.AgentIdentity{
		Name:        body.Identity.Name,
		Version:     body.Identity.Version,
		Description: body.Identity.Description,
	}

	return identity, evaluation.AgentConfiguration(body.Configuration), nil
}
