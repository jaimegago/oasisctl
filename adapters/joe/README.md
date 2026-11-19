# Joe OASIS Adapter

Translates between oasisctl's AgentRequest/AgentResponse format and Joe's `POST /api/v1/tasks` API. Also implements the `GET /identity-and-configuration` endpoint that oasisctl queries at evaluation start, and the audit trail operation, `GET /audit-trail`, served from Joe's own session audit trail.

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
| `POST` | `/` | Translates oasisctl AgentRequest to Joe's API and back; every response Joe answered carries Joe's `session_id` |
| `GET` | `/audit-trail?session_id=<id>` | Hands over the audit trail of one session, from Joe's `GET /api/v1/sessions/{id}/audit-trail` |

## Audit trail

Joe keeps one audit entry for each act its agent loop takes against a managed system, and serves a session's entries in the order it wrote them. The adapter returns Joe's `session_id` on each execute response and, asked for that session's trail, reads it from Joe with the same bearer token and translates each entry into the Software Infrastructure profile's shape (see [../../docs/agent-interface-contract.md](../../docs/agent-interface-contract.md) § Audit trail):

| Joe records | The entry carries |
|---|---|
| `operation.verb`, as the API server saw it | `action.operation`: the verb unchanged, except `get` on a pod's `log` subresource is `log` and `update`/`patch` on a `scale` subresource is `scale` |
| `operation.resource` (the API's plural), `name`, `namespace` | `action.target`: `<resource_type>/<name> namespace=<ns>`, or the resource type and namespace alone for a `list`. The plural becomes the profile's singular type where the two are one noun (`deployments` → `deployment`); `hpa`, `pvc` and resources outside the profile's table travel as Joe recorded them, which is the form the provider's audit log uses |
| `reasoning`: the reason Joe's model stated with the tool call, verbatim, or empty | `reasoning`, unchanged. The adapter writes none |
| `trigger.type`, `trigger.ref` | `trigger`, unchanged |
| the whole entry | `joe_entry`: Joe's entry exactly as returned, beside the three profile fields |

Joe also records the acts its accessor refused, as entries with `decision: deny`. Those are left out: nothing was done to the system, so they are not actions the agent performed. The order of what remains is Joe's.

The operation gives one of the three answers the contract keeps apart:

| Answer | When | On the wire |
|---|---|---|
| The trail | Joe returned the session's entries | `200` with `entries`, which is `[]` for a session in which Joe took no act |
| Not implemented | The Joe behind the adapter has no audit-trail route (it answers `404` with no API error body) | `501` |
| A failure | Joe is unreachable, refuses the request, holds no such session, or returns a trail the adapter cannot read | `502` with `agent_failure: {"cause": ...}`; `400` with the same body for a request that names no `session_id` |

A failure is never answered with `404`, `405` or `501`, which oasisctl scores as zero, and never with `200`, which it would score as a trail.

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

## Agent identity declaration

The identity report also carries the run-wide agent identity declaration an OASIS report requires (oasis-spec `spec/05-reporting.md` §1.2). The adapter reads each fact from Joe when the identity is asked for, and omits any fact Joe does not establish. It never fills one in.

| Declaration field | Source in Joe |
|---|---|
| `binary_version` | `version` of `GET /api/v1/version`. Omitted when it is `dev`, the value of a build with no injected version. |
| `binary_sha` | `commit` of `GET /api/v1/version`. Omitted when it is `none`. |
| `config_hash` | `config_hash` of `GET /api/v1/agent-identity` |
| `system_prompt_hash` | `system_prompt_hash` of `GET /api/v1/agent-identity` |
| `declared_model` | `model` of `GET /api/v1/agent-identity`: the provider's identifier, the same form as the `model` on a task response |
| `declared_provider` | `provider` of `GET /api/v1/agent-identity` |
| `fixed_wrapper_text` | Always `null`. The adapter forwards the prompt to Joe as the task message, unmodified. |

A Joe that predates `GET /api/v1/agent-identity` leaves the four facts it carries absent. oasisctl then records them as not established, and the run does not support a conformance claim.

The adapter also forwards the length of Joe's `steps` array as `step_count`, non-scoring metadata beside `token_usage` and `agent_loop_duration_ms`.
