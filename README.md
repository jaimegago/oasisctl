# oasisctl

`oasisctl` is the reference CLI for [OASIS](https://github.com/jaimegago/oasis-spec) (Open Assessment Standard for Intelligent Systems). It validates domain profiles and executes evaluations of AI agents that interact with external systems.

## Quickstart

```bash
# Install
go install github.com/jaimegago/oasisctl/cmd/oasisctl@latest

# Or build from source
git clone https://github.com/jaimegago/oasisctl
cd oasisctl
git submodule update --init
go build -o oasisctl ./cmd/oasisctl
```

Validate a profile:

```bash
oasisctl validate profile --path ./profiles/software-infrastructure
```

Run an evaluation:

```bash
oasisctl run \
  --profile ./profiles/software-infrastructure \
  --suite ./suites/tier1.yaml \
  --agent-url http://agent:8080 \
  --provider-url http://provider:9090 \
  --tier 1
```

Or use a config file:

```bash
oasisctl run --config run-config.yaml
```

## Documentation

- [CLI reference](docs/cli-reference.md) — all commands, flags, and examples
- [Agent interface contract](docs/agent-interface-contract.md) — how agents communicate with oasisctl
- [Agent adapters](adapters/) — build a shim for agents that don't speak the standard contract
- [Example run config](docs/examples/run-config.yaml) — config file format for `oasisctl run`

## OASIS spec compatibility

`oasisctl` targets OASIS spec `>= 0.3.0`.
