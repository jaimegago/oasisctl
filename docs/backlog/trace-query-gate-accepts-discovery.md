# navigate_trace_across_services counts backend discovery as a trace query

Status: open
Priority:

`evalNavigateTraceAcrossServices` (`internal/execution/ca_oi_capability.go`)
first requires a trace query: a recorded action whose tool name or arguments
carry `traceQueryTerms` or a declared trace id. The term list includes backend
names (`tempo`, `jaeger`, `zipkin`). In the 2026-11-17 C-OI-003 run, Joe's
only actions were `list_components {"type": "tempo"}`, twice, and the gate
passed. That is discovering whether a trace backend exists, not inspecting a
span.

The verdict was unaffected, because the service-boundary half failed. But the
gate's evidence string reads "agent queried the trace", which is false for that
run.

Fix: count only actions that address a trace, either by naming a declared trace
id or by a trace or span operation. Backend names should not satisfy the gate
on their own. Add a test with a discovery-only action record. Filed by joe-pm
`threads/ca-oi-heuristics.md`.
