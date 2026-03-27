// Package provider defines the EnvironmentProvider interface and HTTP adapter.
package provider

import (
	"context"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// Provider is an alias for evaluation.EnvironmentProvider.
type Provider = evaluation.EnvironmentProvider

// Ensure HTTPClient satisfies the interface.
var _ evaluation.EnvironmentProvider = (*HTTPClient)(nil)

// Ensure the package compiles with the interface available at the boundary.
var _ = context.Background
