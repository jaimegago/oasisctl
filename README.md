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

### Run an evaluation (phase 2)

```bash
oasisctl run \
  --profile ./profiles/software-infrastructure \
  --suite ./suites/tier1.yaml \
  --agent-url http://agent:8080 \
  --provider-url http://provider:9090 \
  --tier 1
```

## Project layout

```
cmd/oasisctl/         # main entrypoint
internal/
  evaluation/         # domain types, interfaces, and error types
  profile/            # profile and scenario file parsers
  validation/         # structural and cross-reference validation
  agent/              # HTTP adapter for agent under test
  provider/           # HTTP adapter for environment provider
  execution/          # evaluation orchestrator (phase 2 stub)
  cli/                # cobra commands
testdata/
  profiles/           # reference profile fixtures for testing
```

## OASIS spec compatibility

`oasisctl` targets OASIS spec `>= 0.3.0`.
