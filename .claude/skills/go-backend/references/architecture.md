# Architecture Patterns Reference

Extended guidance for Go backend architecture. Read this when scaffolding new services or making architectural decisions.

## Dependency Injection

Pattern-agnostic: manual constructor injection, wire, or fx are all acceptable. The key requirement is explicit dependency declaration:

```go
// Constructor injection (preferred for most cases)
func NewService(repo Repository, logger *slog.Logger) *Service {
    return &Service{repo: repo, logger: logger}
}

// Main wires everything together
func main() {
    db := postgres.NewDB(cfg.DatabaseURL)
    repo := postgres.NewOrderRepository(db)
    logger := slog.Default()
    svc := orders.NewService(repo, logger)
    // ...
}
```

Avoid:
- Global state and package-level singletons
- `init()` functions that create dependencies
- Service locator patterns

## Domain Organization

Group by domain, not by technical layer. Each domain package should be relatively self-contained:

```
internal/orders/
├── order.go           # Domain types
├── service.go         # Business logic
├── service_test.go    # Unit tests
├── repository.go      # Repository interface
└── errors.go          # Domain-specific errors
```

Infrastructure implementations live separately:

```
internal/postgres/
├── order_repository.go      # Implements orders.Repository
└── order_repository_test.go # Integration tests
```

## Interface Design

### Small Interfaces

Prefer small, focused interfaces:

```go
// Good: single responsibility
type OrderReader interface {
    FindByID(ctx context.Context, id string) (*Order, error)
}

type OrderWriter interface {
    Save(ctx context.Context, order *Order) error
}

// Compose when needed
type OrderRepository interface {
    OrderReader
    OrderWriter
}
```

### Accept Interfaces, Return Structs

Functions should accept interfaces but return concrete types:

```go
// Good
func NewService(repo OrderRepository) *Service { ... }

// The Service methods return concrete types or domain interfaces
func (s *Service) CreateOrder(ctx context.Context, input Input) (*Order, error) { ... }
```

## Boundary Separation

### Transport Layer

HTTP handlers, gRPC methods, CLI commands. Responsibilities:
- Extract and validate input
- Call business logic
- Format response
- Handle transport-specific concerns (headers, status codes)

```go
func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {
    // 1. Extract input
    var req CreateOrderRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        h.writeError(w, http.StatusBadRequest, "invalid request body")
        return
    }
    
    // 2. Validate
    if err := req.Validate(); err != nil {
        h.writeError(w, http.StatusBadRequest, err.Error())
        return
    }
    
    // 3. Call business logic (no HTTP concepts leak here)
    order, err := h.service.CreateOrder(r.Context(), orders.CreateOrderInput{
        CustomerID: req.CustomerID,
        Items:      req.Items,
    })
    
    // 4. Format response
    if err != nil {
        h.handleServiceError(w, err)
        return
    }
    h.writeJSON(w, http.StatusCreated, order)
}
```

### Business Logic Layer

Pure domain logic. No imports from:
- `net/http` or any transport package
- Database drivers or ORM packages
- Logging/metrics/tracing packages (instrumentation happens via decorators)

```go
package orders

type Service struct {
    repo Repository
}

func (s *Service) CreateOrder(ctx context.Context, input CreateOrderInput) (*Order, error) {
    // Pure business logic
    order := NewOrder(input.CustomerID, input.Items)
    
    if err := order.Validate(); err != nil {
        return nil, fmt.Errorf("invalid order: %w", err)
    }
    
    if err := s.repo.Save(ctx, order); err != nil {
        return nil, fmt.Errorf("failed to save order: %w", err)
    }
    
    return order, nil
}
```

### Infrastructure Layer

Concrete implementations of domain interfaces:

```go
package postgres

type OrderRepository struct {
    db *sql.DB
}

func (r *OrderRepository) Save(ctx context.Context, order *orders.Order) error {
    _, err := r.db.ExecContext(ctx,
        `INSERT INTO orders (id, customer_id, total, created_at) VALUES ($1, $2, $3, $4)`,
        order.ID, order.CustomerID, order.Total, order.CreatedAt,
    )
    if err != nil {
        return fmt.Errorf("inserting order: %w", err)
    }
    return nil
}
```

### No ORMs

Use `database/sql` with raw SQL queries. Do NOT use ORMs (GORM, ent, bun, sqlx, squirrel, etc.).

**Why:**

- Raw SQL is explicit, readable, and debuggable
- ORMs hide query behavior and make performance problems hard to trace
- `database/sql` + `rows.Scan` is sufficient for all needs
- SQL is a first-class skill — don't abstract it away

```go
// CORRECT: raw database/sql
rows, err := r.db.QueryContext(ctx, `SELECT id, name FROM users WHERE active = true`)
// ...
for rows.Next() {
    var u User
    if err := rows.Scan(&u.ID, &u.Name); err != nil { ... }
}

// WRONG: ORM
db.Where("active = ?", true).Find(&users)  // never do this
```

## Error Boundaries

### Where to Log

Log at boundaries where you have context to act:
- HTTP handlers (to include request ID, path)
- Background job processors
- Event handlers

Don't log in business logic - let errors bubble up to boundaries.

### Error Types

Use error wrapping for context, custom types for behavior:

```go
// Wrapping for context
return fmt.Errorf("failed to process order %s: %w", orderID, err)

// Custom types when callers need to inspect
type NotFoundError struct {
    Resource string
    ID       string
}

func (e *NotFoundError) Error() string {
    return fmt.Sprintf("%s %s not found", e.Resource, e.ID)
}

// Check with errors.As
var notFound *NotFoundError
if errors.As(err, &notFound) {
    // Handle not found case
}
```
