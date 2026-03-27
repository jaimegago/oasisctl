# Observability Reference

Extended guidance for OpenTelemetry instrumentation in Go services. Read this when implementing metrics, traces, and logging.

## Core Principle

**Instrumentation lives at boundaries, not in business logic.**

Business logic should be pure - no imports from logging, metrics, or tracing packages. Instrumentation is applied via:
- HTTP/gRPC middleware for transport boundaries
- Decorator pattern for business logic instrumentation
- Database driver instrumentation for data layer

## Decorator Pattern

Wrap services with instrumented decorators:

```go
// Base interface
type OrderService interface {
    CreateOrder(ctx context.Context, input CreateOrderInput) (*Order, error)
    GetOrder(ctx context.Context, id string) (*Order, error)
}

// Pure implementation (no instrumentation)
type orderService struct {
    repo Repository
}

// Instrumented decorator
type instrumentedOrderService struct {
    next   OrderService
    tracer trace.Tracer
    meter  metric.Meter
    
    // Pre-created instruments
    createLatency metric.Float64Histogram
    createCount   metric.Int64Counter
}

func NewInstrumentedOrderService(next OrderService, tp trace.TracerProvider, mp metric.MeterProvider) *instrumentedOrderService {
    tracer := tp.Tracer("orders")
    meter := mp.Meter("orders")
    
    createLatency, _ := meter.Float64Histogram("order.create.latency",
        metric.WithUnit("ms"),
        metric.WithDescription("Time to create an order"),
    )
    
    createCount, _ := meter.Int64Counter("order.create.count",
        metric.WithDescription("Number of orders created"),
    )
    
    return &instrumentedOrderService{
        next:          next,
        tracer:        tracer,
        meter:         meter,
        createLatency: createLatency,
        createCount:   createCount,
    }
}

func (s *instrumentedOrderService) CreateOrder(ctx context.Context, input CreateOrderInput) (*Order, error) {
    ctx, span := s.tracer.Start(ctx, "OrderService.CreateOrder",
        trace.WithAttributes(
            attribute.String("customer_id", input.CustomerID),
        ),
    )
    defer span.End()
    
    start := time.Now()
    order, err := s.next.CreateOrder(ctx, input)
    duration := time.Since(start)
    
    // Record metrics
    status := "success"
    if err != nil {
        status = "error"
        span.RecordError(err)
        span.SetStatus(codes.Error, err.Error())
    }
    
    s.createLatency.Record(ctx, float64(duration.Milliseconds()),
        metric.WithAttributes(attribute.String("status", status)),
    )
    s.createCount.Add(ctx, 1,
        metric.WithAttributes(attribute.String("status", status)),
    )
    
    return order, err
}
```

## HTTP Middleware

Apply instrumentation at the HTTP layer:

```go
func TracingMiddleware(tracer trace.Tracer) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            ctx, span := tracer.Start(r.Context(), fmt.Sprintf("%s %s", r.Method, r.URL.Path),
                trace.WithSpanKind(trace.SpanKindServer),
                trace.WithAttributes(
                    semconv.HTTPMethodKey.String(r.Method),
                    semconv.HTTPURLKey.String(r.URL.String()),
                ),
            )
            defer span.End()
            
            // Wrap response writer to capture status code
            ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
            
            next.ServeHTTP(ww, r.WithContext(ctx))
            
            span.SetAttributes(semconv.HTTPStatusCodeKey.Int(ww.status))
            if ww.status >= 400 {
                span.SetStatus(codes.Error, http.StatusText(ww.status))
            }
        })
    }
}

func MetricsMiddleware(meter metric.Meter) func(http.Handler) http.Handler {
    requestCount, _ := meter.Int64Counter("http.server.request_count")
    requestLatency, _ := meter.Float64Histogram("http.server.latency", 
        metric.WithUnit("ms"),
    )
    
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            start := time.Now()
            ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
            
            next.ServeHTTP(ww, r)
            
            attrs := metric.WithAttributes(
                attribute.String("method", r.Method),
                attribute.String("path", r.URL.Path),
                attribute.Int("status", ww.status),
            )
            
            requestCount.Add(r.Context(), 1, attrs)
            requestLatency.Record(r.Context(), float64(time.Since(start).Milliseconds()), attrs)
        })
    }
}
```

## Trace-Enriched Logging

Inject trace context into all log entries:

```go
// Create a trace-aware logger
func TraceLogger(ctx context.Context, base *slog.Logger) *slog.Logger {
    span := trace.SpanFromContext(ctx)
    if !span.SpanContext().IsValid() {
        return base
    }
    
    sc := span.SpanContext()
    return base.With(
        slog.String("trace_id", sc.TraceID().String()),
        slog.String("span_id", sc.SpanID().String()),
        slog.Bool("trace_sampled", sc.IsSampled()),
    )
}

// Usage in handlers
func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    logger := TraceLogger(ctx, h.logger)
    
    logger.Info("processing create order request",
        slog.String("customer_id", req.CustomerID),
    )
    
    // Pass logger through context for downstream use
    ctx = WithLogger(ctx, logger)
    
    order, err := h.service.CreateOrder(ctx, input)
    if err != nil {
        logger.Error("failed to create order", slog.Any("error", err))
        // ...
    }
}

// Context-based logger access
type loggerKey struct{}

func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
    return context.WithValue(ctx, loggerKey{}, logger)
}

func LoggerFromContext(ctx context.Context) *slog.Logger {
    if logger, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
        return logger
    }
    return slog.Default()
}
```

## gRPC Interceptors

Apply instrumentation via interceptors:

```go
func UnaryServerInterceptor(tracer trace.Tracer) grpc.UnaryServerInterceptor {
    return func(
        ctx context.Context,
        req interface{},
        info *grpc.UnaryServerInfo,
        handler grpc.UnaryHandler,
    ) (interface{}, error) {
        ctx, span := tracer.Start(ctx, info.FullMethod,
            trace.WithSpanKind(trace.SpanKindServer),
        )
        defer span.End()
        
        resp, err := handler(ctx, req)
        
        if err != nil {
            span.RecordError(err)
            span.SetStatus(codes.Error, err.Error())
        }
        
        return resp, err
    }
}
```

## Metric Naming Conventions

Follow OpenTelemetry semantic conventions:

```go
// Service-level metrics
"order.create.latency"      // Histogram, unit: ms
"order.create.count"        // Counter
"order.active.count"        // UpDownCounter (gauge-like)

// HTTP server metrics (use semconv)
"http.server.request_count"
"http.server.latency"

// Database metrics
"db.client.operation.latency"
"db.client.connection.count"
```

## Sampling Strategy

Configure appropriate sampling for production:

```go
sampler := trace.ParentBased(
    trace.TraceIDRatioBased(0.1), // Sample 10% of new traces
)

tp := trace.NewTracerProvider(
    trace.WithSampler(sampler),
    // ...
)
```

## Testing Instrumentation

Verify instrumentation in integration tests:

```go
//go:build integration

func TestInstrumentedService_EmitsTraces(t *testing.T) {
    // Use in-memory exporter for testing
    exporter := tracetest.NewInMemoryExporter()
    tp := trace.NewTracerProvider(
        trace.WithSyncer(exporter),
    )
    defer tp.Shutdown(context.Background())
    
    svc := NewInstrumentedOrderService(
        &mockService{},
        tp,
        metric.NewMeterProvider(),
    )
    
    _, _ = svc.CreateOrder(context.Background(), input)
    
    spans := exporter.GetSpans()
    require.Len(t, spans, 1)
    assert.Equal(t, "OrderService.CreateOrder", spans[0].Name)
}
```
