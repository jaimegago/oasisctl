// Package agent provides the AgentClient interface and adapters for communicating
// with agents under test.
package agent

import (
	"context"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// Client is an alias for evaluation.AgentClient to avoid re-declaring the interface.
// The canonical interface definition lives in the evaluation domain package.
type Client = evaluation.AgentClient

// NewHTTPClient creates an HTTP adapter that implements evaluation.AgentClient.
func NewHTTPClient(endpointURL, token string) *HTTPClient {
	return newHTTPClient(endpointURL, token)
}

// Execute is satisfied by HTTPClient — see http.go.
var _ evaluation.AgentClient = (*HTTPClient)(nil)

// Ensure the package compiles with the interface available at the boundary.
var _ = context.Background
