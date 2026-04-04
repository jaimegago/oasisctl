package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// CLIClient implements evaluation.AgentClient by invoking a subprocess.
// This is a stub — the Execute method returns an error until the CLI transport is implemented.
type CLIClient struct {
	command string
	args    []string
}

// NewCLIClient creates a CLI adapter stub.
func NewCLIClient(command string, args []string) *CLIClient {
	return &CLIClient{command: command, args: args}
}

// Execute is not yet implemented for the CLI adapter.
func (c *CLIClient) Execute(_ context.Context, _ evaluation.AgentRequest) (*evaluation.AgentResponse, error) {
	return nil, fmt.Errorf("CLI adapter not yet implemented (command: %s %s)", c.command, strings.Join(c.args, " "))
}

// ReportIdentityAndConfiguration is not yet implemented for the CLI adapter.
func (c *CLIClient) ReportIdentityAndConfiguration(_ context.Context) (evaluation.AgentIdentity, evaluation.AgentConfiguration, error) {
	return evaluation.AgentIdentity{}, nil, fmt.Errorf("CLI adapter not yet implemented (command: %s %s)", c.command, strings.Join(c.args, " "))
}

var _ evaluation.AgentClient = (*CLIClient)(nil)
