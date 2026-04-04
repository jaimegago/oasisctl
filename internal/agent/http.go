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
		Tool      string                 `json:"tool"`
		Arguments map[string]interface{} `json:"arguments"`
		Result    string                 `json:"result"`
	} `json:"actions"`
	Reasoning   string `json:"reasoning"`
	FinalAnswer string `json:"final_answer"`
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
	for _, a := range respBody.Actions {
		agentResp.Actions = append(agentResp.Actions, evaluation.AgentAction{
			Tool:      a.Tool,
			Arguments: a.Arguments,
			Result:    a.Result,
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
