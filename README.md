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

Evaluate an agent. An evaluation is opened with its two execution counts declared, and every applicable scenario is executed that many times: safety first, and capability only behind a safety PASS. Its record is kept in `--evaluation-dir`:

```bash
oasisctl run \
  --profile ./profiles/software-infrastructure \
  --agent-url http://agent:8080 \
  --provider-url http://provider:9090 \
  --tier 1 \
  --evaluation-dir ./evaluations/my-agent \
  --safety-count 3 --capability-count 3
```

An evaluation is not one process. If it is interrupted, run the same command again: it continues from the record and loses at most the execution it was making. `oasisctl evaluation open`, `execute`, `status` and `assemble` are the single steps, for scheduling executions yourself.

Or use a config file:

```bash
oasisctl run --config run-config.yaml
```

Generate an HTML report from an evaluation's record:

```bash
oasisctl evaluation assemble --format html --output report.html --open \
  --profile ./profiles/software-infrastructure \
  --evaluation-dir ./evaluations/my-agent
```

Execute a few scenarios once each, outside any evaluation, for a quick check:

```bash
oasisctl try \
  --profile ./profiles/software-infrastructure \
  --agent-url http://agent:8080 \
  --provider-url http://provider:9090 \
  --tier 1 --category diagnostic-accuracy
```

Re-render a saved verdict as HTML, print a text summary, or convert between YAML and JSON:

```bash
oasisctl report html --input verdict.yaml --output report.html
oasisctl report summary --input verdict.yaml
oasisctl report convert --input verdict.yaml --format json --output verdict.json
```

## Documentation

- [CLI reference](docs/cli-reference.md) — all commands, flags, and examples
- [Agent interface contract](docs/agent-interface-contract.md) — how agents communicate with oasisctl
- [Agent adapters](adapters/) — build a shim for agents that don't speak the standard contract
- [Example run config](docs/examples/run-config.yaml) — config file format for `oasisctl run`

## OASIS spec compatibility

`oasisctl` evaluates against OASIS core `1.0.0-rc1.14` and the Software Infrastructure profile `0.3.0-rc4`, the text vendored at `testdata/oasis-spec`. A provider must declare a core version compatible with the profile's dependency, `>= 1.0.0-rc1.14`.
