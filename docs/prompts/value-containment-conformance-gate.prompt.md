# Prompt: Add ValueContainmentSupport gate to provider conformance validation

Generated: 2026-04-24
Model: Claude Opus 4.7 (1M context)
Target: internal/execution/orchestrator.go, internal/execution/orchestrator_test.go

## Specification

Add a `ValueContainmentSupport` gate to oasisctl's provider conformance
validation so that the preflight check fails fast when an SI profile declares
value containment constraints on scenarios but the provider does not support
the verification mechanism.

### Change 1: validation gate

In `validateConformanceResponse` in `internal/execution/orchestrator.go`, add a
new branch that follows the exact pattern of the three existing capability
gates (`state_injection`, `audit_policy_installation`,
`network_policy_enforcement`). Place it immediately after the
`network_policy_enforcement` branch and before the final `return nil`.

The branch fires when the profile's `ProviderConformanceRequirements` set
`ValueContainmentSupport = true` and the provider's `ConformanceRequirements`
reports `ValueContainmentSupport = false`. The error message must follow the
tone and shape of the existing three gates: it names the capability
(`value_containment_support`) and explains, in a parenthetical clause, what the
missing support means in the context of the SI profile — the SI profile
declares value containment constraints on scenarios and requires the provider
to support the verification mechanism that checks sensitive values do not
escape declared output channels.

### Change 2: tests

Extend `internal/execution/orchestrator_test.go` conformance tests. Do not
rewrite the file. Follow the existing per-function test style already used for
`audit_policy_installation` and `network_policy_enforcement` — one
`TestValidateConformanceResponse_*` function per case, each constructing a
`ConformanceResponse` inline and obtaining requirements via the existing
`testSIConformanceRequirements()` helper.

Do not modify the existing helper's default (value containment must remain
absent from the default so existing tests keep passing); set
`ValueContainmentSupport = true` on the returned value inline in the tests that
need it.

Add three cases:

1. Profile requires `value_containment_support` and provider declares it:
   validation returns no error.
2. Profile requires `value_containment_support` and provider declares false:
   validation returns an error whose message contains
   `value_containment_support must be true`.
3. Profile does not require `value_containment_support` and provider declares
   false: validation returns no error (the gate must not fire when the profile
   does not require the capability).

### Acceptance criteria

- `go vet ./...` passes.
- `go build ./...` passes.
- `go test ./...` passes.
- The gate fires during the preflight conformance check, before any scenario
  execution, when the provider lacks value containment support against an SI
  profile that requires it.

### Out of scope

- No changes to the assertion engine, the value-ref resolver, the channel
  content dispatch, or any file under `internal/execution/` other than
  `orchestrator.go` and its test file.
- No changes to the profile-side conformance loader — the
  `ValueContainmentSupport` field on `ProviderConformanceRequirements` is
  already populated by the loader.
- No changes to the `ConformanceResponse` / `ConformanceRequirements` schema —
  the `ValueContainmentSupport` field is already present on the oasisctl side.
- No changes to case-insensitive matching in `value_containment.go`.
