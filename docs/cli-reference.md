# CLI Reference

## oasisctl run

Execute an OASIS evaluation against an agent and environment provider.

```bash
oasisctl run \
  --profile ./profiles/software-infrastructure \
  --suite ./suites/tier1.yaml \
  --agent-url http://agent:8080 \
  --provider-url http://provider:9090 \
  --tier 1
```

### Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--profile` | string | | Path to domain profile directory |
| `--suite` | string | | Path to suite YAML file (required unless `--config` provides it) |
| `--config` | string | | Path to run configuration YAML file (see [example](examples/run-config.yaml)) |
| `--agent-url` | string | | Agent HTTP endpoint |
| `--agent-token` | string | | Agent auth token |
| `--agent-adapter` | string | `http` | Agent adapter type: `http`, `mcp`, `cli` |
| `--agent-command` | string | | Agent CLI binary path (for `cli` adapter) |
| `--provider-url` | string | | Environment provider HTTP endpoint |
| `--tier` | int | | Claimed complexity tier (1, 2, or 3) — required |
| `--output` | string | stdout | Report output file path (required when `--format html`) |
| `--evidence-dir` | string | alongside `--output` | Directory for per-scenario evidence artifacts; defaults to the working directory when the report goes to stdout |
| `--format` | string | `yaml` | Report format: `yaml`, `json`, or `html` |
| `--open` | bool | `false` | Open HTML report in default browser (only with `--format html`) |
| `--parallel` | int | `1` | Max concurrent scenarios |
| `--timeout` | string | `5m` | Per-scenario timeout (Go duration format) |
| `--dry-run` | bool | `false` | Validate inputs without executing |
| `--verbose` | bool | `false` | Verbose execution output |
| `--safety-only` | bool | `false` | Run only safety scenarios, skip capability scoring |
| `--category` | string slice | | Filter scenarios by category (repeatable) |
| `--subcategory` | string slice | | Filter scenarios by subcategory (repeatable) |
| `--scenario` | string slice | | Filter scenarios by ID glob pattern (repeatable) |

CLI flags override values from `--config`. See [run-config.yaml](examples/run-config.yaml) for the config file format.

### Agent configuration schema

If the profile defines an `agent_configuration_schema`, oasisctl queries the agent adapter's `GET /identity-and-configuration` endpoint at evaluation start. The reported configuration is used to:

- **Filter scenarios** — scenarios with `applicability` conditions that don't match the agent's configuration are marked `NOT_APPLICABLE` (not PASS, not FAIL).
- **Merge conditional assertions** — scenarios with `conditional` assertion blocks are merged based on the agent's configuration.
- **Populate the verdict** — the agent's name, version, and configuration appear in the report metadata.
- **Emit coverage warnings** — if more than 50% of scenarios in a safety category are `NOT_APPLICABLE`, the report includes a warning.

### Tier verification

`--tier` is a claim, and it is verified before any scenario runs. The provider's `GET /v1/conformance` response must carry `tier_evidence`:

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

The run aborts, with no verdict file, when:

- `tier_evidence` is absent or has no checks;
- a check names no requirement, or has a status outside the three;
- a `met` check carries no observed value;
- `tier` is lower than `--tier`;
- `met` is false or any check is `unmet` — the error names each unmet requirement, what was required and what was observed;
- no check at all is `met`.

A `not_assessed` check never aborts the run and is never read as met.

On a run that starts, the evidence is recorded in the verdict as `environment.tier_evidence`, with a one-line rendering in `environment.evidence` that lists the checks reported and not assessed apart from the rest.

### Coverage and completeness

Every report carries a `coverage_matrix`: for each category the profile declares, the archetypes evaluated, the archetypes skipped and why, and whether the claimed tier's minimum was met. The minimum is read from the profile's own tier requirements.

- An archetype counts as evaluated when at least one of its scenarios reached a verdict or a score. A scenario that was `NOT_APPLICABLE`, hit a provider failure, carried an agent failure, errored before it was scored, or had every behaviour unassessable covers nothing.
- A capability category with no archetype applicable to the agent's configuration is reported `excluded`, not `unmet`, and does not count against the minimum. Under `operational_mode: read_only` that is Operational Execution in the Software Infrastructure profile (profile §8.1).
- `coverage_matrix.minimum_coverage_met` is `false` when any other category is below the minimum, when the profile states no minimum for the claimed tier, or when the tier carries a coverage requirement oasisctl does not compute. `coverage_matrix.unmet` names each cause.

`metadata.evaluation_mode.complete` is `true` only when no filter was active, the capability phase ran, and minimum coverage was met. `metadata.evaluation_mode.incomplete_reasons` says why otherwise. Neither changes the exit code.

### Filtering modes

Run a safety-only assessment (conformant — no capability scenarios executed):

```bash
oasisctl run --profile ./profiles/sw-infra --provider-url http://provider:9090 \
  --agent-url http://agent:8080 --tier 1 --safety-only
```

Run only specific categories (produces an incomplete evaluation):

```bash
oasisctl run --profile ./profiles/sw-infra --provider-url http://provider:9090 \
  --agent-url http://agent:8080 --tier 1 \
  --category boundary-enforcement --category prompt-injection-resistance
```

Run only specific subcategories:

```bash
oasisctl run --profile ./profiles/sw-infra --provider-url http://provider:9090 \
  --agent-url http://agent:8080 --tier 1 --subcategory permission-boundary
```

Run only specific scenarios by ID pattern:

```bash
oasisctl run --profile ./profiles/sw-infra --provider-url http://provider:9090 \
  --agent-url http://agent:8080 --tier 1 --scenario "safety.sec.*"
```

Flags combine: `--safety-only --category X` runs only safety scenarios in category X. When any filter is active the report is labeled as incomplete (except `--safety-only` alone, which is a conformant safety assessment).

Use `--dry-run` with filters to preview how many scenarios match:

```bash
oasisctl run --profile ./profiles/sw-infra --provider-url http://provider:9090 \
  --agent-url http://agent:8080 --tier 1 --safety-only --dry-run
```

### Agent adapters

| Adapter | Flag value | Status |
|---|---|---|
| HTTP (default) | `http` | Implemented |
| MCP | `mcp` | Stub |
| CLI (subprocess) | `cli` | Stub |

See [agent-interface-contract.md](agent-interface-contract.md) for the full agent interface specification.

### Exit codes

- `0` — evaluation passed (safety gate passed, capability scored)
- `1` — evaluation failed (safety gate failed or error)

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

Print a concise one-line summary of a verdict file.

```bash
oasisctl report summary --input verdict.yaml
```

### Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--input` | string | | Path to verdict YAML or JSON file — required |

Output format:

```
Safety: PASS | Scenarios: 12 passed, 0 failed | Categories: sec:PASS, be:PASS
```

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
  execution/          # evaluation orchestrator, assertion engine, scorer, evidence artifacts
  cli/                # cobra commands
testdata/
  oasis-spec/         # oasis-spec git submodule (profiles, scenarios)
```
