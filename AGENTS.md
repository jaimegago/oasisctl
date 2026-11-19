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
- Do not modify the `EnvironmentProvider` or `AgentClient` interfaces without considering both the oasisctl side and the provider/agent side — these are wire contracts. `AgentClient` includes `ReportIdentityAndConfiguration` (called once at evaluation start), `Execute` (called per scenario) and `AuditTrail` (called per scenario that asserts a behaviour defined over the agent's audit trail).
- Do not copy spec files into the repo. The oasis-spec submodule is the single source of truth.
- Do not add LLM dependencies.
- Capability scenarios come in two mutually exclusive scoring forms (spec 02-scenarios.md §1.7). Form A declares a weighted rubric and flows through the assertion engine and `Scorer`. Form B declares an `archetype_template` binding and is scored entirely by `internal/scoring`; it skips must/must_not evaluation, but value containment still runs for both forms. A scenario declaring both forms or neither is a load error.
- Everything in `internal/scoring/` must be a pure function of the evidence passed in — no time, randomness, map-iteration order, or environment. Two conformant evaluators must return the same verdict from the same evidence artifact.
- Adding an archetype band template is a data-plus-function addition to the registry in `internal/scoring/bands.go`, never an orchestrator change.
- Every executed scenario writes `evidence-<scenario-id>.json`. Tests that call `Orchestrator.Run` must set `Config.EvidenceDir` to a temp dir or they will litter the source tree.
- `observed_model` in the evidence artifact carries whatever model the agent reported, and **falls back to an explicit JSON `null`** — never an empty string, which would read as an observation of a model named `""`. Reporting a model is an adapter **capability**, not a requirement of the wire contract: an agent whose adapter omits the field evaluates normally and records `null`. The chain is `model` on the agent wire body → `agentResponseBody.Model` → `evaluation.AgentResponse.Model` (`*string`), with absent-or-empty collapsed to `nil` **exactly once**, at the `internal/agent/http.go` decode boundary. The joe adapter reports it by forwarding joe's task-response `model` (joe D-0153), which attests the model resolved at joe's task preparation, not per LLM call; joe's sibling `provider` field is not carried yet.
- **Cost and latency are carried, never scored.** The joe adapter forwards joe's `total_tokens` and `duration_ms` as `token_usage` and `agent_loop_duration_ms`. The duration is renamed because it covers joe's agent loop only and is not scenario wall-clock. `internal/agent/http.go` turns them into `evaluation.NonScoringMetadata`, an opaque type with no exported field and no accessor, so no scoring path can read a figure from it (`TestNonScoringMetadataIsOpaque` pins this). The artifact records it as `non_scoring_metadata`, which is `null` when the agent reported nothing. Inside it, a cache count the provider does not report is `null` and never `0`; keep that distinction at every hop.
- **An assertion that passed because there was nothing to check says so.** `evaluation.AssertionResult` carries `Vacuous` and `VacuityReason` beside the verdict; `Status` stays `PASS`. Spec §3.6.1 closes the verdict vocabulary at three values, so vacuity travels beside the verdict the way `NOT_APPLICABLE` does and never becomes a fourth status. `VacuityReason` is a closed machine-readable vocabulary (`internal/evaluation/interfaces.go`) — a consumer must never parse the evidence prose to learn this. The evidence artifact's `vacuous_assertions` keeps three states apart: `null` (no assertions were evaluated), `[]` (they were, and none was vacuous), and a populated list; only `RecordAssertionVacuity` moves it off `null`, which is why the artifact is written after scoring rather than before. **Marking a pass changes no verdict** — a change that moves a count is a defect in the change.
- **A safety verdict is a claim about a configuration, and says so first.** Every rendering of a PASS, FAIL or PROVIDER_FAILURE — `evaluation_note`, the HTML banner, `report summary`, the run's stderr — leads with `evaluation.SafetyClaimPreface`: the `agent_configuration` the verdict is indexed by and the `applicable` count it rests on, before the verdict word (spec 05-reporting.md §2.3, 06-principles.md principle 12). `NOT_EVALUATED` has no verdict to index and carries no preface. Under `operational_mode: read_only` the summary also carries `safety_summary.containment.write_prohibitions_held`: the write-shaped `must_not.action` assertions that PASSed. **It counts the outcome and never asks why it held.** OASIS is black box (spec 01-core.md §1, principle 5): whether joe never tried, tried and was refused by its own write floor, or had no tool to try with is joe's development question and belongs in joe's own evidence, not in a verdict. The agent's tool calls stay in the evidence artifact uninterpreted. A change that reads the tool-call channel to explain a pass has crossed that line. **It changes no verdict and no vacuity marker.**
- **The claimed tier is verified, and coverage is computed, never asserted.** Before any scenario, `VerifyTierEvidence` checks the `tier_evidence` on the provider's conformance response against `--tier` (spec 04-execution.md §3 step 3, 08-provider-conformance.md §3.6) and aborts with no verdict file when there is none, when a `met` check carries no observed value, when any check is `unmet`, or when none is `met`. A `not_assessed` check never aborts and is never read as met. oasisctl never touches the environment, so what it verifies is the evidence: it does not re-derive a check's status from its `observed`. The spec does not fix the evidence shape; `evaluation.TierEvidence` mirrors what petri produces (`pkg/preflight/tier.go` there), and changing either side is a wire-contract change. `ComputeCoverage` builds `coverage_matrix` from what was evaluated against the tier minimum the profile states in its §8 prose (`internal/profile/tiers.go` — the one place profile prose is parsed, and it fails closed: an unrecognised requirement is carried as uncomputed and makes the minimum unmet). A capability category with no archetype applicable to the agent's configuration is `excluded`, not `unmet` (05-reporting.md §3.3, SI profile §8.1); applicability is decided from the profile's whole suite, `Profile.Scenarios`, so a filter can never turn a gap into an exclusion. `evaluation_mode.complete` is true only when nothing was filtered, the capability phase ran, and the minimum was met.
- **The agent's audit trail is obtained through one operation, scored against an independent oracle, and never evidence for anything else.** Every execute response carries a `session_id`, recorded as received on the scenario result and in the evidence artifact; `AgentClient.AuditTrail` takes it (spec 01-core.md §4.5, 04-execution.md §1.3). The operation has three answers that must stay apart at every hop: a trail (an empty one scores zero on the trail behaviours), `evaluation.ErrAuditTrailNotImplemented` (zero on every trail behaviour, and the scenario is scored — never `NOT_APPLICABLE`, never unevaluable), and any other error (an agent failure, `UNEVALUABLE`). The runner attaches what the operation returned to the response as `AuditTrailEvidence`; `Execute` never sets it, and **only `internal/execution/au_capability.go` reads it** — the trail is self-report, and a heuristic that consults it to resolve another assertion has broken §3.4. The four trail behaviours carry their own `AssertionResult.Score` (a proportion, or a binary probe), which `ScoreCapability` uses in place of the rubric; `produce_complete_audit_trail` over an empty oracle is unassessable, not zero and not full. The tamper probe is a second `Execute` in its own session followed by a second read of the first session's trail — there is no multi-turn session anywhere in the runner. A scenario's `temporal_condition` stimuli go to the provider on `ProvisionRequest.Stimuli` and never into an `AgentRequest`.
- **The output document is the §1 verdict and the §2 report, and a value that cannot be established is absent, never guessed.** `Report.Verdict` (`verdict:` in the file) is `evaluation.StandardVerdict`, the format of spec 05-reporting.md §1 field for field, built last from the report it is published beside (`internal/execution/verdict_format.go`); the rest of the document is the §2 report. `TestVerdictConformsToSection1` parses the shape out of the vendored spec's own YAML block and requires every key of a verdict written to disk, so a dropped field fails the suite and so does a field the spec gains when the pin moves. The agent identity declaration comes from the adapter's identity report and from nowhere else: a fact the report did not carry is `nil` from the `internal/agent/http.go` decode boundary to the verdict, which emits an explicit `null` and names it in `agent_identity.not_established`. `heterogeneous` is `null`, not `false`, when it cannot be decided. A core dimension with no score is `null` and named in `dropped_dimensions`. `metadata.conformance_claim` says whether the run supports a claim and reads no result. The scenario record's `non_scoring_metadata` is `evaluation.ScenarioRecordMetadata`, opaque for the reason `NonScoringMetadata` is.
- **Three tier figures are the evaluator's to judge.** Namespaces, microservices and security zones count what scenario preconditions declare (SI profile §8), so `JudgeScenarioFigures` judges them from the scenarios the run executed — the first two against the largest number one scenario declares, zones against the distinct zones across the run — and records each in `environment.scenario_figures` with what it counted. The figures are read from the profile's tier prose by `internal/profile/tiers.go`, which fails closed: one it cannot read is `not_assessed`, never met. `VerifyTierEvidence` accepts provider evidence with or without checks under those three names and neither counts nor fails on them. `configuration_coverage.total_scenarios` counts the profile's whole suite; a scenario above the claimed tier is `excluded_by_tier`, not left out.
- **An agent failure is reported, never inferred.** An adapter that sends `agent_failure: {cause}` on the execute response (spec 04-execution.md §1.2) makes the scenario `UNEVALUABLE` (spec 01-core.md §3.6.6): it enters no archetype, category or dimension score and no `applicable` count, its archetype and category become `comparable: false`, and the run reports `metadata.agent_failures` (emitted when zero) with each scenario and cause in `metadata.agent_failure_scenarios`. In a safety scenario a FAIL on anything but a `must` assertion — a forbidden behaviour, or a value-containment leak — stands as FAIL; otherwise it is `UNEVALUABLE`, never PASS. Nothing reads an empty `final_answer` or an empty `actions` list as a failure: a response without the report is an answer and is scored. `UNEVALUABLE` carries `passed: false`, so every counter must exclude it by status before it reads `passed`. An agent failure never aborts the run and never changes the exit code.

## Repo dependencies

- **oasis-spec** (git submodule at `testdata/oasis-spec/`) — the OASIS standard and domain profiles. oasisctl reads profiles from here for testing. For real use, the `--profile` flag points at any directory containing a profile. When starting work that touches profile loading, validation, or scenario parsing, check if this submodule is behind upstream with `git submodule status` and update with `git submodule update --remote testdata/oasis-spec` if needed.
- **Petri** (`github.com/jaimegago/petri`) — the OASIS environment provider for software infrastructure. Exposes the `/v1/*` HTTP API that oasisctl's provider client calls.
- **Joe** (`github.com/jaimegago/joe`) — the AI infrastructure copilot. The first agent evaluated by oasisctl against the Software Infrastructure profile.

## Repo layout

- `cmd/oasisctl/` — CLI entrypoint.
- `internal/evaluation/` — pure domain: types, interfaces, errors. No external deps.
- `internal/cli/` — thin boundary layer: flags, output, logging.
- `internal/agent/` — AgentClient adapters (http, mcp, cli) used by oasisctl at runtime.
- `internal/execution/` — orchestrator, assertion engine, scorer, report writer, evidence artifact writer.
- `internal/scoring/` — capability scoring decomposition: the profile's primitive registry and the archetype band templates. Pure; no I/O.
- `internal/profile/` — profile loading and parsing.
- `internal/provider/` — EnvironmentProvider HTTP client.
- `internal/validation/` — profile and scenario validation.
- `adapters/` — standalone adapter binaries (separate Go modules) that translate oasisctl's agent contract to a specific agent's native API. Each adapter is its own directory with a `main.go`. See `adapters/README.md` for the agent interface contract and how to build new adapters.
- `docs/` — CLI reference, agent interface contract docs, and usage examples.

## Build / test / lint

```bash
git submodule update --init
go build -o oasisctl ./cmd/oasisctl
go test ./...
golangci-lint run
go test -tags=integration ./...   # integration tests (mock HTTP servers, no external deps)
```

## Repo-specific conventions

This project follows the **go-backend** skill, vendored at `agent-skills/go-backend/SKILL.md` (the `agent-skills` submodule, pinned to a tag; a skill is edited in that repository, never here). Read it and its `references/` before writing, reviewing or scaffolding Go code.

Adapters in `adapters/` are standalone Go modules — they do not import oasisctl packages. They duplicate the wire types (AgentRequest, AgentResponse) by value. Do not try to share types between oasisctl and adapters.

## Project management

Work is ordered from the maintainer's ledger (`joe-pm`), which dispatches sessions into
this repository. The repository's own surfaces:

- **Backlog**: `docs/backlog/`, one file per item, indexed by `docs/backlog/INDEX.md`
  (the item format lives there). Regenerate the index whenever an item is created,
  changed, or archived; a finished item moves to `docs/backlog/done/`.
- **Issues**: GitHub Issues are inbound reports, not work orders — work on one starts by
  filing or citing a backlog item.
- **Commits** begin with the work's slug (`<slug>: summary`). Push to `origin main` is
  done; there is no PR gate.
- **Worktrees** do not populate `testdata/oasis-spec`, and `internal/profile` fails
  without it. Initialise it from the primary checkout, which needs no network:
  `git -c submodule.testdata/oasis-spec.url=/Users/jaimegago/oasisctl/testdata/oasis-spec submodule update --init testdata/oasis-spec`.
