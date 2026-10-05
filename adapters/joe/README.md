# Joe OASIS Adapter

Translates between oasisctl's AgentRequest/AgentResponse format and Joe's `POST /api/v1/tasks` API. Also implements the `GET /identity-and-configuration` endpoint that oasisctl queries at evaluation start.

## Build

```bash
cd adapters/joe
go build -o joe-adapter .
```

## Run

```bash
./joe-adapter \
  --joe-url http://localhost:7777 \
  --operational-mode read_write \
  --zone-model=true \
  --listen :8091
```

For a read-only evaluation:

```bash
./joe-adapter \
  --joe-url http://localhost:7777 \
  --operational-mode read_only \
  --zone-model=true \
  --listen :8091
```

## Flags

| Flag | Default | Description |
|---|---|---|
| `--listen` | `:8091` | Address the adapter listens on |
| `--joe-url` | (required) | Joe's HTTP API base URL |
| `--joe-token` | (none) | Bearer token for Joe's API |
| `--timeout` | `3m` | Per-request timeout |
| `--operational-mode` | (required) | Joe's operational mode: `read_only` or `read_write` |
| `--zone-model` | `true` | Whether Joe has security zones enabled |
| `--joe-version` | `unknown` | Joe's version string (reported in identity endpoint) |

The adapter fails at startup if `--operational-mode` is missing or not one of `read_only` / `read_write`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/identity-and-configuration` | Returns agent identity and configuration (called once at evaluation start) |
| `POST` | `/` | Translates oasisctl AgentRequest to Joe's API and back |

## Agent failure report

When joe's turn ends with `status` `error` or `context_overflow` — an LLM or provider error, at any iteration count — the adapter sends an `agent_failure` report whose cause carries joe's status, iteration count and error verbatim, e.g. `joe status=error iterations=0: <joe's error>`. It does the same, with the adapter's own message, when it cannot reach joe or cannot read its reply. oasisctl then records the scenario as unevaluable rather than scoring the empty response as a miss; see [../../docs/agent-interface-contract.md](../../docs/agent-interface-contract.md).

joe stopping itself under one of its own limits — `timeout`, `max_iterations_reached`, `runaway_terminated`, `cost_limit_exceeded` — is not reported: what joe produced within its limits is scored. Neither is a `completed` turn, however empty its answer.

## Mode mapping

| oasisctl mode | Joe safety_tier |
|---|---|
| `read-only` | `observe` |
| `supervised` | `record` |
| `autonomous` | `act` |

## Usage with oasisctl

```bash
# Terminal 1: Start Joe
joe serve --port 7777

# Terminal 2: Start the adapter
./joe-adapter --joe-url http://localhost:7777 --operational-mode read_write --listen :8091

# Terminal 3: Start Petri
petri serve --lab my-lab --listen :8090

# Terminal 4: Run evaluation
oasisctl run \
  --profile <path-to-profile> \
  --agent-url http://localhost:8091 \
  --provider-url http://localhost:8090 \
  --tier 1
```

oasisctl queries `GET /identity-and-configuration` once at evaluation start. The reported configuration determines which scenarios are applicable and how conditional assertions are merged.
