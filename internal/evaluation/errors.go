package evaluation

import (
	"fmt"
	"strings"
)

// ValidationIssue is a single validation problem.
type ValidationIssue struct {
	Field   string
	Message string
}

func (v ValidationIssue) Error() string {
	if v.Field != "" {
		return fmt.Sprintf("%s: %s", v.Field, v.Message)
	}
	return v.Message
}

// ValidationError is returned when profile or scenario validation fails.
type ValidationError struct {
	Issues []ValidationIssue
}

func (e *ValidationError) Error() string {
	msgs := make([]string, len(e.Issues))
	for i, issue := range e.Issues {
		msgs[i] = issue.Error()
	}
	return fmt.Sprintf("validation failed: %s", strings.Join(msgs, "; "))
}

func (e *ValidationError) Add(field, message string) {
	e.Issues = append(e.Issues, ValidationIssue{Field: field, Message: message})
}

func (e *ValidationError) HasIssues() bool {
	return len(e.Issues) > 0
}

// EvaluationError is returned when a runtime evaluation failure occurs.
type EvaluationError struct {
	ScenarioID string
	Cause      error
}

func (e *EvaluationError) Error() string {
	return fmt.Sprintf("evaluation error for scenario %s: %v", e.ScenarioID, e.Cause)
}

func (e *EvaluationError) Unwrap() error {
	return e.Cause
}

// ProviderError is returned when the environment provider communication fails.
type ProviderError struct {
	Operation string
	Cause     error
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("provider error during %s: %v", e.Operation, e.Cause)
}

func (e *ProviderError) Unwrap() error {
	return e.Cause
}

// AgentError is returned when agent communication fails.
type AgentError struct {
	Cause error
}

func (e *AgentError) Error() string {
	return fmt.Sprintf("agent error: %v", e.Cause)
}

func (e *AgentError) Unwrap() error {
	return e.Cause
}
