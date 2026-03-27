package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// HTTPClient implements evaluation.EnvironmentProvider over HTTP/JSON.
// Phase 2 will complete the full implementation.
type HTTPClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewHTTPClient creates an HTTPClient for the given provider base URL.
func NewHTTPClient(baseURL string) *HTTPClient {
	return &HTTPClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 2 * time.Minute,
		},
	}
}

// Provision creates an environment for the given scenario.
func (c *HTTPClient) Provision(ctx context.Context, scenario *evaluation.Scenario) (string, error) {
	payload, err := json.Marshal(map[string]interface{}{
		"scenario_id":   scenario.ID,
		"preconditions": scenario.Preconditions,
	})
	if err != nil {
		return "", &evaluation.ProviderError{Operation: "provision", Cause: err}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/environments", bytes.NewReader(payload))
	if err != nil {
		return "", &evaluation.ProviderError{Operation: "provision", Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", &evaluation.ProviderError{Operation: "provision", Cause: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", &evaluation.ProviderError{
			Operation: "provision",
			Cause:     fmt.Errorf("provider returned status %d", resp.StatusCode),
		}
	}

	var body struct {
		EnvironmentID string `json:"environment_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", &evaluation.ProviderError{Operation: "provision", Cause: fmt.Errorf("decode response: %w", err)}
	}
	return body.EnvironmentID, nil
}

// StateSnapshot captures the current environment state.
func (c *HTTPClient) StateSnapshot(ctx context.Context, environmentID string) (*evaluation.EnvironmentState, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/environments/%s/state", c.baseURL, environmentID), nil)
	if err != nil {
		return nil, &evaluation.ProviderError{Operation: "state-snapshot", Cause: err}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &evaluation.ProviderError{Operation: "state-snapshot", Cause: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &evaluation.ProviderError{
			Operation: "state-snapshot",
			Cause:     fmt.Errorf("provider returned status %d", resp.StatusCode),
		}
	}

	var state evaluation.EnvironmentState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return nil, &evaluation.ProviderError{Operation: "state-snapshot", Cause: fmt.Errorf("decode: %w", err)}
	}
	return &state, nil
}

// Teardown destroys the environment.
func (c *HTTPClient) Teardown(ctx context.Context, environmentID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		fmt.Sprintf("%s/environments/%s", c.baseURL, environmentID), nil)
	if err != nil {
		return &evaluation.ProviderError{Operation: "teardown", Cause: err}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &evaluation.ProviderError{Operation: "teardown", Cause: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return &evaluation.ProviderError{
			Operation: "teardown",
			Cause:     fmt.Errorf("provider returned status %d", resp.StatusCode),
		}
	}
	return nil
}

// InjectState sets up specific state in the environment.
func (c *HTTPClient) InjectState(ctx context.Context, environmentID string, state interface{}) error {
	payload, err := json.Marshal(state)
	if err != nil {
		return &evaluation.ProviderError{Operation: "inject-state", Cause: err}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/environments/%s/state", c.baseURL, environmentID), bytes.NewReader(payload))
	if err != nil {
		return &evaluation.ProviderError{Operation: "inject-state", Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &evaluation.ProviderError{Operation: "inject-state", Cause: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return &evaluation.ProviderError{
			Operation: "inject-state",
			Cause:     fmt.Errorf("provider returned status %d", resp.StatusCode),
		}
	}
	return nil
}

// Observe provides independent access to audit logs and state.
func (c *HTTPClient) Observe(ctx context.Context, environmentID string) (*evaluation.EnvironmentState, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/environments/%s/observe", c.baseURL, environmentID), nil)
	if err != nil {
		return nil, &evaluation.ProviderError{Operation: "observe", Cause: err}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &evaluation.ProviderError{Operation: "observe", Cause: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &evaluation.ProviderError{
			Operation: "observe",
			Cause:     fmt.Errorf("provider returned status %d", resp.StatusCode),
		}
	}

	var state evaluation.EnvironmentState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return nil, &evaluation.ProviderError{Operation: "observe", Cause: fmt.Errorf("decode: %w", err)}
	}
	return &state, nil
}
