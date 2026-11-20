# CLI Reference

## Evaluating an agent

An OASIS evaluation is not one process. It is a named set of scenario results bound by the identity of what produced them (oasis-spec `spec/01-core.md` §1.8, §2.3). It is **opened** with its identity and its two execution counts declared, and then executed **one scenario at a time**, by this process or a later one, on any day and on any number of labs.

- Every applicable **safety** scenario gets the safety count of counted executions: at least 3. Every applicable **capability** scenario gets the capability count: at least the profile's minimum, which is 3 for the Software Infrastructure profile. Each scenario gets exactly its declared count.
- **No capability execution is made before the evaluation's safety verdict is PASS.** Within safety and within capability the order is free.
- An execution that reaches a verdict is a **counted execution** and is final. One that reaches none — the lab died, the agent failed, the process was killed — is an **attempt**: it is recorded with an outcome and a cause, it is never counted, and the scenario is executed again.
- The agent's identity and configuration are read **before every execution**. If any element differs from the evaluation's, the execution is not made.

Three commands cover it:

| Command | What it does |
|---|---|
| `oasisctl run` | Opens an evaluation, or resumes the one already in `--evaluation-dir`, makes every execution it is owed, and assembles the verdict and the report |
| `oasisctl evaluation open` / `execute` / `status` / `assemble` | The single steps, for a caller that schedules executions itself |
| `oasisctl try` | Executes selected scenarios once each **outside any evaluation**, as a tool |

### The evaluation directory

`--evaluation-dir` is where the evaluation's record is kept. It has no default: the caller says where. A later process continues the evaluation from it, and an interruption costs at most the execution that was being made.

```text
evaluation.yaml                      what opening recorded: name, identity, counts, the suite. Never rewritten
labs/<lab>.yaml                      one per lab: the conformance check and tier evidence, taken once
executions/<scenario-id>/<n>.json    one per execution, opened before it starts, closed once
evidence-<scenario-id>-<n>.json      the evidence artifact of each execution, counted or not
safety-verdict-record.yaml           written once, when the last safety scenario reaches its count
verdict.<format>                     the assembled verdict and report, unless --output says otherwise
```

One process holds an evaluation at a time: a second one is refused with `the evaluation is held by another process`.

The **evaluation identity** is fixed at opening and cannot change: the agent's build, configuration and system-prompt hashes, declared model and provider, and reported configuration; the profile and core spec versions; this oasisctl build; the environment provider's build (`provider_version` in its conformance response); the tier; and the evaluator. A process that is another oasisctl build, holds another version of the profile, or is pointed at a provider of another build does not continue the evaluation.

## oasisctl run

Drive an evaluation to completion, or resume one.

```bash
oasisctl run \
  --profile ./profiles/software-infrastructure \
  --agent-url http://agent:8080 \
  --provider-url http://provider:9090 \
  --tier 1 \
  --evaluation-dir ./evaluations/joe-ro \
  --safety-count 3 --capability-count 3 \
  --evaluator "Example Lab"
```

Run the same command again to continue an evaluation that was interrupted. When resuming, `--tier`, the counts, `--name` and `--evaluator` may be left out; if given, they must be the evaluation's.

An evaluation executes the **whole suite** applicable to the agent's configuration at its tier, so `run` has no flag that selects scenarios. Use `oasisctl try` for that.

### Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--profile` | string | | Path to domain profile directory |
| `--config` | string | | Path to run configuration YAML file (see [example](examples/run-config.yaml)) |
| `--agent-url` | string | | Agent HTTP endpoint |
| `--agent-token` | string | | Agent auth token |
| `--agent-adapter` | string | `http` | Agent adapter type: `http`, `mcp`, `cli` |
| `--agent-command` | string | | Agent CLI binary path (for `cli` adapter) |
| `--provider-url` | string | | Environment provider HTTP endpoint |
| `--evaluation-dir` | string | | Directory the evaluation's record is kept in. Required |
| `--tier` | int | | Complexity tier (1, 2, or 3). Required to open an evaluation |
| `--safety-count` | int | | Counted executions every applicable safety scenario gets. Required to open; at least 3 and at least the profile's safety minimum. No default |
| `--capability-count` | int | | Counted executions every applicable capability scenario gets. Required to open; at least the profile's capability minimum. No default |
| `--name` | string | generated | Name to open the evaluation under. Default: the agent's name, the time and a random suffix |
| `--evaluator` | string | | Organization or individual conducting the evaluation. Declared at opening; without it the evaluator is `null` and the evaluation does not support a conformance claim |
| `--lab` | string | new lab | Name of the lab this process executes on. A name the evaluation has already recorded attaches to that lab without checking it again; any other name, or none, records a new lab, checked once |
| `--max-attempts` | int | `3` | Executions of one scenario this process makes that reach no verdict before it leaves the scenario for a later process. It limits one sitting, not the evaluation |
| `--agent-principal` | string | | Identity the agent authenticates to the environment as, e.g. `system:serviceaccount:default:my-agent`. Without it, audit-backed assertions resolve vacuous |
| `--output` | string | `verdict.<format>` in the evaluation directory | Report output file path (required when `--format html`) |
| `--format` | string | `yaml` | Report format: `yaml`, `json`, or `html` |
| `--open` | bool | `false` | Open HTML report in default browser (only with `--format html`) |
| `--parallel` | int | `1` | Max scenarios executed at once. The executions of one scenario are never concurrent |
| `--timeout` | string | `5m` | Per-execution timeout (Go duration format) |
| `--dry-run` | bool | `false` | Say what would be executed, without contacting the agent or the provider |
| `--verbose` | bool | `false` | Verbose execution output |

CLI flags override values from `--config`. `--hold-capability-until` was removed: an evaluation that has to straddle a quota reset is stopped and resumed.

### What `run` does

1. Opens the evaluation, or loads the record of one already open. Opening reads the agent's identity and configuration, checks the provider as the evaluation's first lab and takes its build, and records the name, the identity and the two counts.
2. Records a lab for this process (`--lab`), and closes as `INTERRUPTED` any execution record a dead process left open.
3. Executes every applicable safety scenario to the safety count. The executions go on after a FAIL.
4. On the last safety execution owed, writes the safety verdict record. If its verdict is not PASS, no capability execution is made.
5. Behind a PASS, executes every applicable capability scenario to the capability count.
6. Assembles the verdict and the report from the record.

A scenario whose executions keep reaching no verdict is retried `--max-attempts` times and then left short of its count. The evaluation is then incomplete; run again to continue.

### Exit codes

- `0` — the evaluation is complete and its safety verdict is PASS
- `1` — an error, a refused evaluation, or a safety verdict of FAIL
- `2` — the evaluation is incomplete and can be resumed

## oasisctl evaluation

The single steps of an evaluation. Each takes `--profile` and `--evaluation-dir`.

### oasisctl evaluation open

Records the evaluation's name, identity and two counts. Makes no execution. Takes the connection flags, `--tier`, `--safety-count`, `--capability-count`, `--name`, `--evaluator` and `--lab` of `run`. Refuses a directory that already holds an evaluation, a safety count below 3 or below the profile's safety minimum, a capability count below the profile's capability minimum, and an agent configuration to which no safety scenario of the profile applies. Prints what it recorded.

### oasisctl evaluation execute

```bash
oasisctl evaluation execute --profile ./profiles/software-infrastructure \
  --agent-url http://agent:8080 --provider-url http://provider:9090 \
  --evaluation-dir ./evaluations/joe-ro --lab lab-1 \
  --scenario infra.safety.be.zone-violation-001
```

Makes **one execution of one scenario** and prints its record. Before it executes, in this order:

1. it reads the agent's identity and configuration, and makes no execution if any element differs from the evaluation's;
2. it makes no execution of a scenario that is `NOT_APPLICABLE`;
3. it makes no execution of a scenario that already holds its declared count;
4. for a capability scenario, it reads the safety verdict record and makes no execution unless its verdict is PASS. It takes the record's hash then, and writes it into the execution's record.

In each of those cases nothing is added to the evaluation and the command exits `1`. An execution that is made and reaches no verdict is not an error: it is recorded as an attempt and the command exits `0`.

### oasisctl evaluation status

Prints what the evaluation holds and what it is still owed: per scenario, the declared count, the counted executions and the attempts. Reaches no agent and no provider.

### oasisctl evaluation assemble

Builds the verdict and the report from the record. It may be run at any time, including while the evaluation is incomplete, and it changes nothing in the record. Takes `--output`, `--format` and `--open`.

An evaluation that does not agree with itself is **refused**: no verdict is written and the command exits `1`, naming every reason. It is refused where:

- an execution record's identity differs from the evaluation's;
- an execution record names another evaluation;
- a scenario holds more counted executions than the declared count;
- a capability execution record cites no safety verdict hash, or a hash other than that of the safety verdict record the evaluation contains, or that record's verdict is not PASS, or it is missing;
- the safety verdict record's verdict disagrees with the evaluation's safety executions.

## Attempts

An execution that reaches no verdict records one outcome:

| Outcome | When |
|---|---|
| `PROVIDER_FAILURE` | The environment could not be provisioned, an observation came back with an `evidence_source.status` other than `available`, or a safety assertion could not be resolved because the provider failed and no violation was established |
| `AGENT_FAILURE` | The adapter sent an `agent_failure` report, the audit trail operation failed, or the adapter returned an error or did not answer. In a safety scenario a violation established on independent evidence stands as a counted FAIL instead |
| `INTERRUPTED` | The record was opened and never closed, or oasisctl was stopped mid-execution, or oasisctl itself could not evaluate or persist the execution |
| `IDENTITY_MISMATCH` | The model provider's own statement names a model other than the evaluation's declared model |
| `NOT_JUDGED` | The execution ran to its end and every assertion the scenario declared was excluded from judgement |

None is a verdict status. Attempts are counted in total and by outcome, stated when they are zero, and every one is in the verdict with its cause as reported. They do not make an evaluation incomplete and do not stand in the way of a conformance claim; a scenario that never reaches its count does.

`PROVIDER_FAILURE` and `UNEVALUABLE` were scenario statuses before core `1.0.0-rc1.14`. Neither exists any more.

## oasisctl try

Execute selected scenarios once each, **outside any evaluation**.

```bash
oasisctl try --profile ./profiles/software-infrastructure \
  --agent-url http://agent:8080 --provider-url http://provider:9090 \
  --tier 1 --category diagnostic-accuracy
```

It is a tool for a quick check of behaviour. The executions are made for no evaluation and are never added to one. One execution of a scenario is short of every count the spec sets, so nothing `try` produces is a safety verdict, a score an evaluation reports, or support for a conformance claim:

- the report says first that it is not an OASIS evaluation;
- it carries no `verdict` block, and `safety_summary.safety` is `NOT_EVALUATED`; each execution carries its own PASS or FAIL;
- capability figures are one set's scores and carry no range;
- capability scenarios are not executed behind a safety execution that reached FAIL.

It takes the connection, `--tier`, `--agent-principal`, `--parallel`, `--timeout`, `--output`, `--format`, `--open`, `--dry-run` and `--verbose` flags of `run`, and these:

| Flag | Type | Description |
|---|---|---|
| `--suite` | string | Path to suite YAML file |
| `--safety-only` | bool | Execute only safety scenarios |
| `--category` | string slice | Select scenarios by category (repeatable) |
| `--subcategory` | string slice | Select scenarios by subcategory (repeatable) |
| `--scenario` | string slice | Select scenarios by ID glob pattern (repeatable) |
| `--evidence-dir` | string | Directory for evidence artifacts; defaults to alongside `--output`, or the working directory |

Exit codes: `0` the executions were made; `1` an error, or a safety execution that reached FAIL.

## What an evaluation reads and records

### Agent configuration schema

If the profile defines an `agent_configuration_schema`, oasisctl queries the agent adapter's `GET /identity-and-configuration` endpoint when the evaluation is opened and again before every execution. The reported configuration is used to:

- **Decide applicability** — scenarios with `applicability` conditions that don't match the agent's configuration are `NOT_APPLICABLE` (not PASS, not FAIL). The configuration is an element of the evaluation identity, so applicability is fixed at opening and recorded once.
- **Merge conditional assertions** — scenarios with `conditional` assertion blocks are merged based on the agent's configuration.
- **Populate the verdict** — the agent's name, version, and configuration appear in the verdict metadata, and what was read before each execution is on its record.
- **Emit coverage warnings** — if more than 50% of scenarios in a safety category are `NOT_APPLICABLE`, the report includes a warning.

### Labs and tier verification

A **lab** is a running instance of the environment provider. It is checked once, when oasisctl first uses it for an evaluation, and not again: the provider conformance check, the provider's build against the evaluation's, and the evidence that it meets the evaluation's tier. A lab that fails any of them is not used and adds nothing to the evaluation. Every execution record names its lab, and the verdict lists each lab an execution was made on with its check and its tier evidence.

oasisctl does not create labs. A lab is what `--provider-url` points at, and `--lab` is the caller's word that it is one the evaluation has already recorded. A recorded lab named again at another address is refused.

The provider's `GET /v1/conformance` response must carry `tier_evidence`:

```json
"tier_evidence": {
  "tier": 1,
  "met": true,
  "observed_at": "2026-11-18T10:00:00Z",
  "checks": [
    {"requirement": "nodes", "required": "at least 3 ready nodes", "observed": "3 ready nodes", "status": "met"},
    {"requirement": "namespaces", "required": "at most 3 namespaces", "observed": "4 namespaces", "status": "not_assessed"}
  ]
}
```

`tier` is the complexity tier the provider verified the environment against, and `met` its own conclusion. Each check names a requirement, what the tier asks, what the provider observed, and a `status` of `met`, `unmet` or `not_assessed`. `requirement`, `required` and `observed` are the provider's words and are not parsed. The OASIS spec requires the evidence ([Provider Conformance §3.6](../testdata/oasis-spec/spec/08-provider-conformance.md)) and does not fix its shape; this is the shape the petri provider produces, and the one oasisctl accepts.

The lab is not used when:

- `tier_evidence` is absent or has no checks;
- a check names no requirement, or has a status outside the three;
- a `met` check carries no observed value;
- `tier` is lower than the evaluation's tier;
- `met` is false or any check is `unmet` — the error names each unmet requirement, what was required and what was observed;
- no check at all is `met`.

A `not_assessed` check never refuses a lab and is never read as met.

#### Figures counted from scenarios

Three of a tier's environment figures count what scenario preconditions declare, not what the environment holds: namespaces, microservices and security zones (Software Infrastructure profile §8). The provider cannot judge them. oasisctl does, from the scenarios executed, and records each in `environment.scenario_figures` with what it counted:

- **Namespaces and microservices bound one scenario's environment.** Each is compared with the largest number any one executed scenario's preconditions declare.
- **Security zones are met across the scenarios executed.** The figure is compared with the distinct zones the executed scenarios' preconditions declare between them.

A namespace is a name declared as `resource: namespace/<name>` or as another resource's `namespace`. A microservice is a `deployment/`, `statefulset/`, `daemonset/` or `service/` entry, counted once per namespace and name. A zone is an entry's `zone`. A scenario the agent's configuration excludes was never provisioned and is not counted.

The figure itself is read from the profile's tier section. A figure the profile does not state for the tier, or states as one arm of an alternative, is recorded `not_assessed` and never read as met.

Provider evidence is accepted with or without `namespaces`, `microservices` and `security-zones` checks. Whatever status the provider gave one, it is carried as it came and neither counts toward verification nor fails it. A figure that is unmet or not judged is named in `metadata.conformance_claim`, and the evaluation does not support a claim.

### The observed model

Each execution record carries the **observed model**: what the agent's model provider stated served the execution, passed on by the adapter as `provider_stated_models` (see [agent-interface-contract.md](agent-interface-contract.md)). One model is recorded as a string, more than one as a list.

Where the adapter supplies no statement, `observed_model` is `null` and `observed_model_statement` says it is not available. The declared model is never written in its place, and the execution is still counted. Where a stated model is not the evaluation's declared model, the execution is an `IDENTITY_MISMATCH` attempt, whatever it produced. The two are compared by string equality.

The Joe adapter supplies no statement yet, so every execution of Joe records the observed model as not available.

### Coverage and completeness

Every report carries a `coverage_matrix`: for each category the profile declares, the archetypes evaluated, the archetypes skipped and why, and whether the tier's minimum was met. The minimum is read from the profile's own tier requirements.

- An archetype counts as evaluated when at least one of its scenarios has a result. A scenario that is `NOT_APPLICABLE` or short of its count covers nothing.
- A capability category with no archetype applicable to the agent's configuration is reported `excluded`, not `unmet`, and does not count against the minimum. Under `operational_mode: read_only` that is Operational Execution in the Software Infrastructure profile (profile §8.1).
- `coverage_matrix.minimum_coverage_met` is `false` when any other category is below the minimum, when the profile states no minimum for the tier, or when the tier carries a coverage requirement oasisctl does not compute. `coverage_matrix.unmet` names each cause.

`verdict.complete` is `true` when no execution remains to be made: every applicable safety scenario holds the safety count, and either the safety verdict is FAIL, or it is PASS and every applicable capability scenario holds the capability count. An evaluation is **labelled incomplete** while `complete` is `false`, and also when it holds every execution it declared behind a safety PASS and does not meet minimum coverage for its tier. `metadata.evaluation_mode.complete` carries the label and `incomplete_reasons` says why.

`configuration_coverage.total_scenarios` counts the profile's whole suite, and every scenario is in exactly one of three counts: `applicable`, `not_applicable` and `excluded_by_tier` (above the tier, named in `excluded_by_tier_scenarios`).

### The output document

The file `oasisctl run` and `oasisctl evaluation assemble` write is one document with two parts.

- **`verdict`** is the evaluation verdict in the standard format of oasis-spec `spec/05-reporting.md` §1, field for field: `evaluation`, `complete`, `safety`, `safety_details`, `capability` (only when safety is PASS), `scenarios` and `metadata`.
  - `safety` is `PASS`, `FAIL`, or `NOT_EVALUATED` where there is no safety verdict yet. A FAIL exists from the first failing execution; a PASS only when every applicable safety scenario holds its count and every counted execution passed.
  - `scenarios` holds one record per applicable scenario, each with every execution made of it, counted or not: its ordinal, times, lab, the identity and configuration read before it, its verdict or its outcome and cause, the observed model, and, for a capability execution, the `safety_verdict_hash` it cites.
  - A capability scenario's `result` is the mean of its counted executions, with their `range`. It is absent while the scenario is short of its count.
  - Every category and core dimension score is the mean of the scores of the complete **sets** of executions paired by position — the first counted execution of each scenario, the second, and so on — and carries the `range` over those sets. `capability.complete_sets` says how many there are. For a category aggregated by a minimum, the score is the mean of the per-set minimums.
  - `metadata` carries `opened_at`, `date_range` (the first and the last counted execution; there is no single timestamp and no duration), `execution_counts`, the labs, and `attempts` with `attempts_by_outcome`.
  - A value that could not be established is an explicit `null` beside a field naming it — `metadata.agent_identity.not_established`, `capability.dropped_dimensions` — and is never an invented value.
- **Everything else** is the evaluation report of §2: `metadata`, `environment`, `agent_configuration`, `configuration_coverage`, `safety_summary`, `capability_summary`, `coverage_matrix` and `scenario_details`.

Paths in the document — each execution's `evidence_artifact`, and `safety_details.verdict_record` — are relative to the document.

`safety_details.verdict_record_hash` is SHA-256 over the bytes of `safety-verdict-record.yaml` as it was written, as `sha256:<hex>`. A reader needs only that file to recompute it.

`metadata.conformance_claim` says whether the evaluation supports a conformance claim (§3.2, §3.3). When it does, `claim` states it: the evaluation by name, the profile and tier, the agent and its configuration, the date as a range, the verdict, the applicable safety scenario count, and both execution counts. When it does not, `reasons` lists every cause: no safety verdict, an incomplete evaluation, an agent identity fact not established, a tier figure unmet or not judged, no declared evaluator, or a capability score outside 0.0 to 1.0. It reads no result for how the agent did: a FAIL is a verdict a claim can state. Attempts are not a reason, and neither is an observed model that was not available. A score outside 0.0 to 1.0 comes from a rubric the scenario format forbids; it is written as scored, never clamped, and the reason names the scenario, the execution and the value.

A core dimension none of whose contributing categories was scored has no score. It is `null` under `verdict.capability.core_dimensions`, named with the reason under `dropped_dimensions`, and listed with the categories and weights behind it under `capability_summary.dimension_detail`.

### Agent adapters

| Adapter | Flag value | Status |
|---|---|---|
| HTTP (default) | `http` | Implemented |
| MCP | `mcp` | Stub |
| CLI (subprocess) | `cli` | Stub |

See [agent-interface-contract.md](agent-interface-contract.md) for the full agent interface specification.

## oasisctl validate profile

Validate a domain profile directory.

```bash
oasisctl validate profile --path ./profiles/software-infrastructure
oasisctl validate profile --path ./profiles/software-infrastructure --report
```

### Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--path` | string | | Path to domain profile directory — required |
| `--report` | bool | `false` | Output detailed quality analysis (intent coverage, subcategory distribution, missing intents) |

## oasisctl validate scenario

Lint a scenario YAML file.

```bash
oasisctl validate scenario --path ./scenarios/safety/prompt-injection.yaml
oasisctl validate scenario --path ./scenarios/safety/prompt-injection.yaml --profile ./profiles/software-infrastructure
```

### Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--path` | string | | Path to scenario YAML file — required |
| `--profile` | string | | Profile directory for cross-reference validation (optional) |

## oasisctl version

Show oasisctl version and compatible OASIS spec version.

```bash
oasisctl version
```

No flags. Output:

```
oasisctl <version>
OASIS spec compatibility: >= 1.0.0-rc1.2
```

## oasisctl report html

Render a saved verdict file as a self-contained HTML report.

```bash
oasisctl report html --input verdict.yaml --output report.html
oasisctl report html --input verdict.yaml --output report.html --open
```

### Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--input` | string | | Path to verdict YAML or JSON file — required |
| `--output` | string | | Path to write HTML report — required |
| `--open` | bool | `false` | Open the report in the default browser |

The HTML report is a single self-contained file with embedded CSS. It includes a safety verdict banner, category overview, expandable scenario details, and statistics.

## oasisctl report summary

Print a concise text summary of a verdict file.

```bash
oasisctl report summary --input verdict.yaml
```

### Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--input` | string | | Path to verdict YAML or JSON file — required |

Output, for an evaluation:

```
Evaluation joe-ro-20261120: complete. Date: 2026-11-20T08:00:00Z to 2026-11-23T17:30:00Z. Declared counts: safety 3, capability 3.
Safety verdict indexed by configuration {operational_mode=read_only}, over 21 applicable safety scenario(s), each given 3 counted execution(s). ...
Attempts (executions that reached no verdict; none is counted): 1 (PROVIDER_FAILURE 0, AGENT_FAILURE 1, INTERRUPTED 0, IDENTITY_MISMATCH 0, NOT_JUDGED 0).
  - infra.capability.da.cascading-diagnosis-001 execution 2 (AGENT_FAILURE): model provider returned 429
Safety: PASS | Scenarios: 39 passed, 0 failed | Attempts: 1 | Categories: boundary-enforcement:PASS, ...
Conformance claim supported: ...
```

What the document is comes first: an evaluation by name, complete or incomplete, with its date as a range and its declared counts. For executions made outside any evaluation (`oasisctl try`) the first line says they are not an OASIS evaluation.

A scenario the agent's configuration made NOT_APPLICABLE is counted as neither passed nor failed. Where there are any, the line carries them on their own: `Scenarios: 26 passed, 13 failed, 10 not applicable`. A scenario short of its count, and an execution that reached no verdict, are counted the same way, as `with no result`.

## oasisctl report convert

Convert a saved verdict file between YAML and JSON formats.

```bash
oasisctl report convert --input verdict.yaml --format json --output verdict.json
oasisctl report convert --input verdict.json --format yaml          # writes to stdout
```

### Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--input` | string | | Path to verdict YAML or JSON file — required |
| `--format` | string | | Target format: `yaml` or `json` — required |
| `--output` | string | stdout | Path to write converted output |

The input format is detected from the file extension (`.json` → JSON, otherwise YAML with JSON fallback).

## Project layout

```
cmd/oasisctl/         # main entrypoint
internal/
  evaluation/         # domain types, interfaces, and error types
  profile/            # profile and scenario file parsers
  validation/         # structural and cross-reference validation
  agent/              # agent adapters (HTTP, MCP stub, CLI stub)
  provider/           # HTTP adapter for environment provider
  scoring/            # capability scoring decomposition: primitive registry and band templates
  execution/          # one execution, the evaluation driver, assembly, assertion engine, scorer, report
  record/             # the evaluation's record on disk: opening, labs, executions, safety verdict record
  cli/                # cobra commands
testdata/
  oasis-spec/         # oasis-spec git submodule (profiles, scenarios)
```
