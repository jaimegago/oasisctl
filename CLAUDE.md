# oasisctl

## Project identity

oasisctl is the reference CLI for the OASIS (Open Assessment Standard for Intelligent Systems) ecosystem. It validates domain profiles and executes evaluations of AI agents that interact with external systems. The spec lives in a separate repo (oasis-spec), referenced as a git submodule.

## Architectural invariants

- Three commands: `oasisctl validate` (profile/scenario linting), `oasisctl run` (full evaluation), and `oasisctl report` (re-render saved verdicts as HTML, summary, or convert between YAML/JSON).
- The runner is deterministic — no LLM in the evaluation loop. The only LLM is the agent under test.
- Safety is a binary gate. If any *applicable* safety scenario fails, capability scenarios do not run. Scenarios marked NOT_APPLICABLE (due to agent configuration) are excluded from the gate.
- Independent verification mandate. The assertion engine never trusts agent self-reporting. All verdicts come from the provider's Observe endpoint.
- All cross-package dependencies flow through interfaces defined in `internal/evaluation/`.
- Business logic packages must not import `net/http`, database drivers, or logging packages. Logging belongs at CLI boundaries only.
- oasisctl talks to the provider over HTTP — it never touches Kubernetes directly.
- Do not modify the `EnvironmentProvider` or `AgentClient` interfaces without considering both the oasisctl side and the provider/agent side — these are wire contracts. `AgentClient` includes `ReportIdentityAndConfiguration` (called once at evaluation start) and `Execute` (called per scenario).
- Do not copy spec files into the repo. The oasis-spec submodule is the single source of truth.
- Do not add LLM dependencies.

## Repo dependencies

- **oasis-spec** (git submodule at `testdata/oasis-spec/`) — the OASIS standard and domain profiles. oasisctl reads profiles from here for testing. For real use, the `--profile` flag points at any directory containing a profile. When starting work that touches profile loading, validation, or scenario parsing, check if this submodule is behind upstream with `git submodule status` and update with `git submodule update --remote testdata/oasis-spec` if needed.
- **Petri** (`github.com/jaimegago/petri`) — the OASIS environment provider for software infrastructure. Exposes the `/v1/*` HTTP API that oasisctl's provider client calls.
- **Joe** (`github.com/jaimegago/joe`) — the AI infrastructure copilot. The first agent evaluated by oasisctl against the Software Infrastructure profile.

## Build / test / lint

```bash
git submodule update --init
go build -o oasisctl ./cmd/oasisctl
go test ./...
golangci-lint run
go test -tags=integration ./...   # integration tests (mock HTTP servers, no external deps)
```

## Repo-specific conventions

This project follows the Go backend skill in `.claude/skills/go-backend/`. Read SKILL.md and reference files before writing code.

Organized by domain, not by technical layer. Key distinction: `internal/evaluation/` is pure domain (types, interfaces, errors — no external deps), while `internal/cli/` is the thin boundary layer (flags, output, logging).

Deferred work items are tracked in `deferred-items.md` at the repo root. Items 002–006 are complete. Only item 001 (adversarial verification) remains — it requires an LLM dependency, so it is intentionally deferred pending an architectural decision.
