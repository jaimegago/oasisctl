---
name: go-backend-standards
description: Go backend development standards and architecture patterns. Use this skill whenever writing, reviewing, or scaffolding Go backend code including services, APIs, operators, or any Go project. Triggers on Go file creation, code review requests, architecture discussions, or when the user mentions Go, Golang, backend services, REST APIs, gRPC, or Kubernetes operators. Always apply these standards when generating Go code.
---

# Go Backend Development Standards

These are opinionated standards for Go backend development. Apply them to all Go code generation, review, and architecture decisions.

## Quick Reference

Before writing Go code, ensure:

- [ ] Business logic decoupled from transport/infrastructure via interfaces
- [ ] Context propagated as first parameter through all functions
- [ ] Errors wrapped with context using `fmt.Errorf("...: %w", err)`
- [ ] Instrumentation via middleware/decorators only (not in business logic)
- [ ] Table-driven tests with mocks, targeting >80% coverage
- [ ] Code passes `golangci-lint run`
- [ ] External dependencies justified (prefer stdlib)

## Architecture

### Package Structure

Use `cmd/`, `internal/`, `pkg/` layout:

- `cmd/` - Entry points (main packages), one subdirectory per service/tool
- `internal/` - Private application code, organized by domain (not by layer)
- `pkg/` - Reusable libraries safe for external import (use sparingly)

**Organize by domain**, not by technical layer:
```
internal/
├── orders/      # Good: domain-focused
├── payments/
└── users/
```

Not:
```
internal/
├── handlers/    # Bad: layer-focused
├── models/
└── repositories/
```

### Interface-Based Design

Business logic depends only on interfaces it defines. Infrastructure implements those interfaces.

```go
// In internal/orders/service.go (business logic)
type OrderRepository interface {
    Save(ctx context.Context, order *Order) error
    FindByID(ctx context.Context, id string) (*Order, error)
}

type Service struct {
    repo OrderRepository  // Depends on interface, not concrete type
}
```

Business logic should never import transport, database, or instrumentation packages.

### Context Handling

Context is a first-class architectural concern:

- Always pass `context.Context` as the first parameter
- Never store context in structs
- Propagate through entire call chain including I/O operations
- Use `context.Background()` only at program entry points
- Use `context.WithValue` sparingly and only for request-scoped data

## Error Handling

Wrap errors with context using `fmt.Errorf`:

```go
if err != nil {
    return fmt.Errorf("failed to save order %s: %w", order.ID, err)
}
```

Handle errors at appropriate boundaries. Don't log and return the same error.

## Testing

### Unit Tests

- Use table-driven tests with descriptive test case names
- Use mocks for external dependencies (interfaces enable this)
- Test files live next to source: `service.go` → `service_test.go`
- Target >80% coverage on business logic

```go
func TestService_CreateOrder(t *testing.T) {
    tests := []struct {
        name    string
        input   CreateOrderInput
        setup   func(*mocks.MockRepository)
        wantErr bool
    }{
        // test cases...
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // ...
        })
    }
}
```

### Integration Tests

- Use build tag: `//go:build integration`
- Test real database/service interactions
- Focus on repository implementations, API handlers, and instrumentation wiring
- Verify metrics and traces are emitted correctly

## Observability

### OpenTelemetry Instrumentation

Apply instrumentation via middleware/decorators at boundaries, not in business logic:

```go
// Decorator pattern for business logic instrumentation
type InstrumentedService struct {
    next    Service
    metrics Metrics
    tracer  trace.Tracer
}

func (s *InstrumentedService) CreateOrder(ctx context.Context, input CreateOrderInput) (*Order, error) {
    ctx, span := s.tracer.Start(ctx, "CreateOrder")
    defer span.End()
    
    start := time.Now()
    order, err := s.next.CreateOrder(ctx, input)
    s.metrics.RecordLatency("create_order", time.Since(start))
    
    return order, err
}
```

### Trace-Enriched Logging

- Inject trace ID, span ID, and trace flags into all log entries
- Pass logger through context as request-scoped data
- Enable log-trace correlation

## Code Style

Follow:
- [Effective Go](https://golang.org/doc/effective_go)
- [Google Go Style Guide](https://google.github.io/styleguide/go/)

Key principles:
- Prefer composition over inheritance
- Keep interfaces small (1-3 methods)
- Use meaningful names; avoid abbreviations
- Prefer simple implementations; avoid unnecessary abstraction

## Linting

Run `golangci-lint run` before committing. Enable standard linters:
- `gofmt`, `goimports`, `govet`, `errcheck`
- `staticcheck`, `gosimple`, `ineffassign`, `unused`

## External Dependencies

**Prefer the Go standard library.** Before introducing any external library:
1. Check if stdlib can accomplish the task
2. Evaluate maintenance status and adoption
3. Ask/confirm with the user if introducing new dependencies

## API Design

### REST APIs

- Return meaningful error responses (not just 500)
- Use structured JSON error format with code, message, and details
- Apply input validation at transport boundary

### gRPC Services

- Use standard `google.rpc.Status` with error details
- Implement proper status codes (not just Internal for all errors)
- Apply interceptors for cross-cutting concerns

## Security Essentials

Based on [OWASP Go Secure Coding Practices](https://github.com/OWASP/Go-SCP):

### Input Validation
- Validate all user input server-side; consider input insecure by default
- Use `github.com/go-playground/validator/v10` for struct validation
- Use allowlists over blocklists

### SQL Injection
- **Always use prepared statements** - never concatenate user input into queries
- PostgreSQL: `$1`, MySQL: `?`, Oracle: `:param1`

### Password Hashing
- Use `golang.org/x/crypto/bcrypt` - never roll your own
- Never store plaintext, never use MD5/SHA alone

### Cryptographic Randomness
- Use `crypto/rand` for all security-sensitive values (tokens, keys)
- Never use `math/rand` for security purposes

### XSS Prevention
- Use `html/template` (not `text/template`) for HTML output
- Set `Content-Type: application/json` for JSON APIs

### Secrets
- Never hardcode secrets - use environment variables or secrets manager
- Never log secrets or include in error messages

---

## Detailed References

For extended guidance, read these files from this skill's directory:
- `references/architecture.md` - Detailed architecture patterns, DI, boundary separation
- `references/testing.md` - Comprehensive testing strategies, test containers, coverage
- `references/observability.md` - Full OTel implementation, trace-enriched logging
- `references/security.md` - OWASP-based security practices, input validation, cryptography
