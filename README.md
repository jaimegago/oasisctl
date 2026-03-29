# oasisctl

`oasisctl` is the reference CLI implementation of the OASIS (Objective Agent Safety and Intelligence Scoring) evaluation framework. It provides tooling to validate domain profiles and scenario files, and (in phase 2) to execute full evaluations against AI agents.

## What is OASIS?

OASIS is a structured framework for evaluating AI agents across two dimensions:

- **Safety**: Does the agent resist adversarial manipulation, respect boundaries, and avoid destructive actions?
- **Capability**: Does the agent perform domain tasks accurately, with appropriate reasoning and auditability?

Evaluations are defined as _domain profiles_ (describing a target environment and its expected behaviors) and _scenarios_ (individual test cases with stimuli, assertions, and scoring criteria). The framework is designed to be portable across domains; the `software-infrastructure` profile is the reference implementation.

## Status: Phase 1

Phase 1 implements profile and scenario validation. The `run` command (full agent evaluation) is deferred to phase 2.

| Feature | Status |
|---|---|
| Profile loading and validation | Done |
| Scenario YAML parsing (multi-doc) | Done |
| Behavior definition parsing | Done |
| Stimulus library parsing | Done |
| Cross-reference integrity checks | Done |
| `oasisctl validate profile` | Done |
| `oasisctl validate scenario` | Done |
| `oasisctl run` (agent evaluation) | Phase 2 |

## Installation

```bash
go install github.com/jaimegago/oasisctl/cmd/oasisctl@latest
```

Or build from source:

```bash
git clone https://github.com/jaimegago/oasisctl
cd oasisctl
git submodule update --init
go build -o oasisctl ./cmd/oasisctl
```

## Usage

### Validate a domain profile

```bash
oasisctl validate profile --path ./profiles/software-infrastructure
oasisctl validate profile --path ./profiles/software-infrastructure --report
```

The `--report` flag prints a summary of behaviors and stimuli defined in the profile.

### Validate a scenario file

```bash
oasisctl validate scenario --path ./profiles/software-infrastructure/scenarios/safety/prompt-injection-resistance.yaml
```

Cross-reference against a profile (checks behavior and stimulus library references):

```bash
oasisctl validate scenario \
  --path ./scenarios/safety/prompt-injection-resistance.yaml \
  --profile ./profiles/software-infrastructure
```

### Show version

```bash
oasisctl version
```

### Run an evaluation

With CLI flags:

```bash
oasisctl run \
  --profile ./profiles/software-infrastructure \
  --suite ./suites/tier1.yaml \
  --agent-url http://agent:8080 \
  --provider-url http://provider:9090 \
  --tier 1
```

With a config file (flags override config values):

```bash
oasisctl run --config run-config.yaml
oasisctl run --config run-config.yaml --tier 2
```

See [docs/examples/run-config.yaml](docs/examples/run-config.yaml) for the config file format.

### Agent adapters

oasisctl supports multiple ways to communicate with agents:

| Adapter | Flag | Status |
|---|---|---|
| HTTP (default) | `--agent-adapter http` | Implemented |
| MCP | `--agent-adapter mcp` | Stub |
| CLI (subprocess) | `--agent-adapter cli` | Stub |

See [docs/agent-interface-contract.md](docs/agent-interface-contract.md) for the full agent interface specification.

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

## OASIS spec compatibility

`oasisctl` targets OASIS spec `>= 0.3.0`.
