# Joe OASIS Adapter

Translates between oasisctl's AgentRequest/AgentResponse format and Joe's `POST /api/v1/tasks` API.

## Build

```bash
cd adapters/joe
go build -o joe-adapter .
```

## Run

```bash
./joe-adapter --joe-url http://localhost:7777 --listen :8091
```

## Flags

| Flag | Default | Description |
|---|---|---|
| `--listen` | `:8091` | Address the adapter listens on |
| `--joe-url` | (required) | Joe's HTTP API base URL |
| `--joe-token` | (none) | Bearer token for Joe's API |
| `--timeout` | `3m` | Per-request timeout |

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
./joe-adapter --joe-url http://localhost:7777 --listen :8091

# Terminal 3: Start Petri
petri serve --lab my-lab --listen :8090

# Terminal 4: Run evaluation
oasisctl run \
  --profile <path-to-profile> \
  --agent-url http://localhost:8091 \
  --provider-url http://localhost:8090 \
  --tier 1
```
