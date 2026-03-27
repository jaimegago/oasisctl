# Testing Strategies Reference

Extended guidance for testing Go backend services. Read this when setting up tests or improving coverage.

## Unit Testing Patterns

### Table-Driven Tests

Standard pattern for comprehensive test coverage:

```go
func TestService_CreateOrder(t *testing.T) {
    tests := []struct {
        name      string
        input     CreateOrderInput
        setupMock func(*mocks.MockRepository)
        want      *Order
        wantErr   string
    }{
        {
            name: "creates order successfully",
            input: CreateOrderInput{
                CustomerID: "cust-123",
                Items:      []Item{{SKU: "abc", Qty: 2}},
            },
            setupMock: func(m *mocks.MockRepository) {
                m.EXPECT().
                    Save(gomock.Any(), gomock.Any()).
                    Return(nil)
            },
            want: &Order{
                CustomerID: "cust-123",
                Status:     OrderStatusPending,
            },
        },
        {
            name: "returns error when repository fails",
            input: CreateOrderInput{
                CustomerID: "cust-123",
                Items:      []Item{{SKU: "abc", Qty: 2}},
            },
            setupMock: func(m *mocks.MockRepository) {
                m.EXPECT().
                    Save(gomock.Any(), gomock.Any()).
                    Return(errors.New("db connection failed"))
            },
            wantErr: "failed to save order",
        },
        {
            name: "returns error for empty items",
            input: CreateOrderInput{
                CustomerID: "cust-123",
                Items:      []Item{},
            },
            setupMock: func(m *mocks.MockRepository) {
                // No mock setup - validation fails before repo call
            },
            wantErr: "order must have at least one item",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            ctrl := gomock.NewController(t)
            defer ctrl.Finish()

            mockRepo := mocks.NewMockRepository(ctrl)
            if tt.setupMock != nil {
                tt.setupMock(mockRepo)
            }

            svc := NewService(mockRepo)
            got, err := svc.CreateOrder(context.Background(), tt.input)

            if tt.wantErr != "" {
                require.Error(t, err)
                assert.Contains(t, err.Error(), tt.wantErr)
                return
            }

            require.NoError(t, err)
            assert.Equal(t, tt.want.CustomerID, got.CustomerID)
            assert.Equal(t, tt.want.Status, got.Status)
        })
    }
}
```

### Test Case Naming

Use descriptive names that explain the scenario:

```go
// Good: describes scenario and expectation
"returns error when repository fails"
"creates order successfully with multiple items"
"validates customer ID is not empty"

// Bad: vague or just repeating code
"test error"
"test create order"
"TestCase1"
```

### Mock Generation

Use `mockgen` for generating mocks from interfaces:

```bash
# Generate mocks for interfaces in a package
mockgen -source=internal/orders/repository.go -destination=internal/orders/mocks/repository.go -package=mocks
```

Or use `go:generate` directives:

```go
//go:generate mockgen -source=repository.go -destination=mocks/repository.go -package=mocks
```

## Integration Testing

### Build Tags

Use build tags to separate integration tests:

```go
//go:build integration

package postgres_test

import (
    "testing"
    // ...
)

func TestOrderRepository_Save(t *testing.T) {
    // Uses real database
}
```

Run with:
```bash
go test -tags=integration ./...
```

### Test Containers

Use testcontainers for database integration tests:

```go
//go:build integration

package postgres_test

import (
    "context"
    "testing"

    "github.com/testcontainers/testcontainers-go"
    "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func setupTestDB(t *testing.T) *sql.DB {
    ctx := context.Background()
    
    container, err := postgres.RunContainer(ctx,
        testcontainers.WithImage("postgres:15"),
        postgres.WithDatabase("test"),
        postgres.WithUsername("test"),
        postgres.WithPassword("test"),
    )
    require.NoError(t, err)
    
    t.Cleanup(func() {
        require.NoError(t, container.Terminate(ctx))
    })
    
    connStr, err := container.ConnectionString(ctx, "sslmode=disable")
    require.NoError(t, err)
    
    db, err := sql.Open("postgres", connStr)
    require.NoError(t, err)
    
    // Run migrations
    runMigrations(t, db)
    
    return db
}

func TestOrderRepository_Integration(t *testing.T) {
    db := setupTestDB(t)
    repo := NewOrderRepository(db)
    
    t.Run("saves and retrieves order", func(t *testing.T) {
        order := &orders.Order{
            ID:         "order-123",
            CustomerID: "cust-456",
        }
        
        err := repo.Save(context.Background(), order)
        require.NoError(t, err)
        
        got, err := repo.FindByID(context.Background(), "order-123")
        require.NoError(t, err)
        assert.Equal(t, order.CustomerID, got.CustomerID)
    })
}
```

### HTTP Handler Tests

Use `httptest` for handler integration tests:

```go
func TestHandler_CreateOrder(t *testing.T) {
    // Setup with real or mock service
    svc := &mockService{}
    handler := NewHandler(svc)
    
    router := chi.NewRouter()
    router.Post("/orders", handler.CreateOrder)
    
    body := `{"customer_id": "cust-123", "items": [{"sku": "abc", "qty": 2}]}`
    req := httptest.NewRequest("POST", "/orders", strings.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    
    rec := httptest.NewRecorder()
    router.ServeHTTP(rec, req)
    
    assert.Equal(t, http.StatusCreated, rec.Code)
    
    var resp OrderResponse
    err := json.NewDecoder(rec.Body).Decode(&resp)
    require.NoError(t, err)
    assert.NotEmpty(t, resp.ID)
}
```

## Coverage Guidelines

### What to Cover

Focus coverage on:
- Business logic (>80% target)
- Edge cases and error paths
- Public API surfaces

Don't obsess over coverage for:
- Simple getters/setters
- Generated code
- Main functions and wire-up code

### Running Coverage

```bash
# Unit tests with coverage
go test -coverprofile=coverage.out ./internal/...

# View coverage report
go tool cover -html=coverage.out

# Check coverage percentage
go tool cover -func=coverage.out | grep total
```

## Testing Instrumentation

Test that metrics and traces are emitted correctly:

```go
//go:build integration

func TestInstrumentedService_EmitsMetrics(t *testing.T) {
    // Use a test meter provider
    reader := metric.NewManualReader()
    provider := metric.NewMeterProvider(metric.WithReader(reader))
    
    svc := NewInstrumentedService(
        &mockService{},
        provider.Meter("test"),
    )
    
    _, _ = svc.CreateOrder(context.Background(), input)
    
    // Verify metrics were recorded
    rm := metricdata.ResourceMetrics{}
    err := reader.Collect(context.Background(), &rm)
    require.NoError(t, err)
    
    // Assert on collected metrics
    // ...
}
```
