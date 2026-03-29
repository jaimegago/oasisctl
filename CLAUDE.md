Project identity
oasisctl is the reference CLI for the OASIS (Open Assessment Standard for Intelligent Systems) ecosystem. It validates domain profiles and executes evaluations of AI agents that interact with external systems. The spec lives in a separate repo (oasis-spec), referenced as a git submodule at testdata/oasis-spec/.
What oasisctl does
Two modes:

oasisctl validate — validate profiles and lint scenarios against the OASIS schema
oasisctl run — execute a full evaluation: provision environments via an OASIS-conformant provider (like Petri), send stimuli to the agent, independently verify outcomes, score results, emit a verdict

The runner is deterministic — no LLM in the evaluation loop. The only LLM is the agent under test.
Architecture
Organized by domain, not by technical layer. Key packages:

internal/evaluation/ — core domain types, interfaces, enums, errors. Every other package depends on this. No external dependencies here — pure domain.
internal/profile/ — profile and scenario loading from disk. Parses YAML scenarios, markdown behavior definitions, markdown stimulus library, markdown profile metadata. Implements ProfileLoader interface.
internal/agent/ — agent client interface and adapters (HTTP, MCP stub, CLI stub). Factory function in adapter.go selects the right client. The HTTP adapter speaks the standard AgentRequest/AgentResponse JSON contract.
internal/provider/ — environment provider HTTP client. Calls the five /v1/* endpoints (provision, state-snapshot, teardown, inject-state, observe). Includes state entry translation (translate.go) that converts raw scenario YAML maps to the normalized format Petri expects.
internal/validation/ — profile and scenario validation, quality analysis (difficulty spectrum, coverage, negative testing ratio, intent coverage, subcategory distribution).
internal/execution/ — the evaluation engine. Orchestrator (main loop), assertion engine (behavior heuristics + audit log verification), scorer (binary safety + weighted capability + aggregation), report writer (YAML/JSON).
internal/cli/ — cobra commands. Thin boundary layer — extracts flags, calls domain logic, handles output. Logging happens here, not in business logic.

Key design decisions

Safety is a binary gate. If any safety scenario fails, capability scenarios do not run.
Independent verification mandate. The assertion engine never trusts agent self-reporting. All verdicts come from the provider's Observe endpoint.
All cross-package dependencies flow through interfaces defined in internal/evaluation/.
Business logic packages must not import net/http, database drivers, or logging packages. Instrumentation via decorators only.
The provider HTTP client translates scenario state entries from the raw YAML format (resource: "deployment/name" with flat fields) to the normalized provider format (kind, name, namespace, spec as separate fields). This translation lives in internal/provider/translate.go.

Go standards
This project follows the Go backend skill in .claude/skills/go-backend/. Read the SKILL.md and reference files before writing code. Key rules:

Constructor injection, no global state, no init() functions creating dependencies
Context as first parameter everywhere, propagated through the full call chain
Errors wrapped with fmt.Errorf("context: %w", err), custom error types in internal/evaluation/errors.go
Table-driven tests with testify require/assert, gomock for interface mocks
go:generate directives for mock generation
golangci-lint must pass (config in .golangci.yml)

Dependencies and related repos

oasis-spec (git submodule at testdata/oasis-spec/) — the OASIS standard and domain profiles. oasisctl reads profiles from here for testing. For real use, the --profile flag points at any directory containing a profile.
Petri (github.com/jaimegago/petri) — the OASIS environment provider for software infrastructure. Runs as petri serve and exposes the /v1/* HTTP API that oasisctl's provider client calls. Petri provisions Kubernetes environments, injects state, captures audit logs, and performs independent observation.
Joe (github.com/jaimegago/joe) — the AI infrastructure copilot. The first agent to be evaluated by oasisctl against the Software Infrastructure profile.

How to build and test
go build -o oasisctl ./cmd/oasisctl
go test ./...
golangci-lint run
Submodule must be initialized:
git submodule update --init
Integration tests (require mock HTTP servers, no external deps):
go test -tags=integration ./...
What NOT to do

Do not copy spec files into the repo. The oasis-spec submodule is the single source of truth for profiles and scenarios.
Do not add Kubernetes client libraries. oasisctl talks to the provider over HTTP — it never touches Kubernetes directly.
Do not add LLM dependencies. The runner is deterministic. The agent is external.
Do not log in business logic packages. Logging belongs at CLI boundaries.
Do not modify the EnvironmentProvider or AgentClient interfaces without considering both the oasisctl side and the provider/agent side — these are wire contracts.

Current state
Phase 1 (profile validation) and Phase 2 (execution engine) are complete. The run command works end-to-end with a provider and agent.
Deferred items are tracked in docs/backlog/ (when present). Each backlog file is a standalone prompt — read it and implement it.
