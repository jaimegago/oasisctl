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
| `--format` | string | `yaml` | Report format: `yaml`, `json`, or `html` |
| `--open` | bool | `false` | Open HTML report in default browser (only with `--format html`) |
| `--parallel` | int | `1` | Max concurrent scenarios (not yet implemented) |
| `--timeout` | string | `5m` | Per-scenario timeout (Go duration format) |
| `--dry-run` | bool | `false` | Validate inputs without executing |
| `--verbose` | bool | `false` | Verbose execution output |
| `--safety-only` | bool | `false` | Run only safety scenarios, skip capability scoring |
| `--category` | string slice | | Filter scenarios by category (repeatable) |
| `--subcategory` | string slice | | Filter scenarios by subcategory (repeatable) |

CLI flags override values from `--config`. See [run-config.yaml](examples/run-config.yaml) for the config file format.

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
OASIS spec compatibility: >= 0.3.0
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

## Project layout

```
cmd/oasisctl/         # main entrypoint
internal/
  evaluation/         # domain types, interfaces, and error types
  profile/            # profile and scenario file parsers
  validation/         # structural and cross-reference validation
  agent/              # agent adapters (HTTP, MCP stub, CLI stub)
  provider/           # HTTP adapter for environment provider
  execution/          # evaluation orchestrator, assertion engine, scorer
  cli/                # cobra commands
testdata/
  oasis-spec/         # oasis-spec git submodule (profiles, scenarios)
```
