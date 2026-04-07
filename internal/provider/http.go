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

// Conformance queries the provider's conformance with a given profile.
func (c *HTTPClient) Conformance(ctx context.Context, profileID string) (*evaluation.ConformanceResponse, error) {
	var resp evaluation.ConformanceResponse
	if err := c.get(ctx, "/v1/conformance?profile="+profileID, &resp); err != nil {
		return nil, &evaluation.ProviderError{Operation: "conformance", Cause: err}
	}
	return &resp, nil
}

// Provision creates an environment for the given scenario.
func (c *HTTPClient) Provision(ctx context.Context, req evaluation.ProvisionRequest) (*evaluation.ProvisionResponse, error) {
	if len(req.Environment.State) > 0 {
		translated, err := TranslateState(req.Environment.State)
		if err != nil {
			return nil, &evaluation.ProviderError{Operation: "provision", Cause: fmt.Errorf("translate state: %w", err)}
		}
		req.Environment.State = translated
	}
	var resp evaluation.ProvisionResponse
	if err := c.post(ctx, "/v1/provision", req, &resp); err != nil {
		return nil, &evaluation.ProviderError{Operation: "provision", Cause: err}
	}
	return &resp, nil
}

// StateSnapshot captures the current environment state.
func (c *HTTPClient) StateSnapshot(ctx context.Context, req evaluation.StateSnapshotRequest) (*evaluation.StateSnapshotResponse, error) {
	var resp evaluation.StateSnapshotResponse
	if err := c.post(ctx, "/v1/state-snapshot", req, &resp); err != nil {
		return nil, &evaluation.ProviderError{Operation: "state-snapshot", Cause: err}
	}
	return &resp, nil
}

// Teardown destroys the environment.
func (c *HTTPClient) Teardown(ctx context.Context, req evaluation.TeardownRequest) error {
	if err := c.post(ctx, "/v1/teardown", req, nil); err != nil {
		return &evaluation.ProviderError{Operation: "teardown", Cause: err}
	}
	return nil
}

// InjectState sets up specific state in the environment.
func (c *HTTPClient) InjectState(ctx context.Context, req evaluation.InjectStateRequest) error {
	if len(req.State) > 0 {
		translated, err := TranslateState(req.State)
		if err != nil {
			return &evaluation.ProviderError{Operation: "inject-state", Cause: fmt.Errorf("translate state: %w", err)}
		}
		req.State = translated
	}
	if err := c.post(ctx, "/v1/inject-state", req, nil); err != nil {
		return &evaluation.ProviderError{Operation: "inject-state", Cause: err}
	}
	return nil
}

// Observe provides independent access to audit logs and state.
func (c *HTTPClient) Observe(ctx context.Context, req evaluation.ObserveRequest) (*evaluation.ObserveResponse, error) {
	var resp evaluation.ObserveResponse
	if err := c.post(ctx, "/v1/observe", req, &resp); err != nil {
		return nil, &evaluation.ProviderError{Operation: "observe", Cause: err}
	}
	return &resp, nil
}

// get is a helper that GETs baseURL+path and decodes into out.
func (c *HTTPClient) get(ctx context.Context, path string, out interface{}) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("provider returned status %d", resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// post is a helper that marshals req as JSON, POSTs to baseURL+path, and decodes into out (nil = discard body).
func (c *HTTPClient) post(ctx context.Context, path string, req interface{}, out interface{}) error {
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("provider returned status %d", resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
