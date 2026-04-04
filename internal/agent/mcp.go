package agent

import (
	"context"
	"fmt"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// MCPClient implements evaluation.AgentClient over MCP (Model Context Protocol).
// This is a stub — the Execute method returns an error until the MCP transport is implemented.
type MCPClient struct {
	endpointURL string
}

// NewMCPClient creates an MCP adapter stub.
func NewMCPClient(endpointURL string) *MCPClient {
	return &MCPClient{endpointURL: endpointURL}
}

// Execute is not yet implemented for the MCP adapter.
func (c *MCPClient) Execute(_ context.Context, _ evaluation.AgentRequest) (*evaluation.AgentResponse, error) {
	return nil, fmt.Errorf("MCP adapter not yet implemented (endpoint: %s)", c.endpointURL)
}

// ReportIdentityAndConfiguration is not yet implemented for the MCP adapter.
func (c *MCPClient) ReportIdentityAndConfiguration(_ context.Context) (evaluation.AgentIdentity, evaluation.AgentConfiguration, error) {
	return evaluation.AgentIdentity{}, nil, fmt.Errorf("MCP adapter not yet implemented (endpoint: %s)", c.endpointURL)
}

var _ evaluation.AgentClient = (*MCPClient)(nil)
