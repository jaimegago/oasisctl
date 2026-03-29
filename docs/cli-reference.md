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
| `--output` | string | stdout | Report output file path |
| `--format` | string | `yaml` | Report format: `yaml` or `json` |
| `--parallel` | int | `1` | Max concurrent scenarios (not yet implemented) |
| `--timeout` | string | `5m` | Per-scenario timeout (Go duration format) |
| `--dry-run` | bool | `false` | Validate inputs without executing |
| `--verbose` | bool | `false` | Verbose execution output |

CLI flags override values from `--config`. See [run-config.yaml](examples/run-config.yaml) for the config file format.

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
