# oasisctl Agent Adapters

An adapter is a thin HTTP server that sits between oasisctl and an agent under test. It translates oasisctl's standard request/response format into the agent's native API, and the agent's response back.

```
oasisctl  ──AgentRequest JSON──►  adapter  ──native API──►  agent
oasisctl  ◄──AgentResponse JSON──  adapter  ◄──native response──  agent
```

The agent never knows it's being evaluated. The adapter is disposable glue code — typically a single Go file, ~100-200 lines.

## When you need an adapter

If your agent already speaks oasisctl's standard HTTP contract (POST endpoint accepting AgentRequest JSON, returning AgentResponse JSON), you don't need an adapter. Point `--agent-url` directly at the agent.

If your agent has its own API format, build an adapter.

## oasisctl's agent contract

### Identity and configuration (queried once at evaluation start)

GET `/identity-and-configuration`

oasisctl calls this endpoint once at the beginning of an evaluation run. The adapter returns the agent's identity and its configuration dimensions. This information is used to filter scenarios by applicability, merge conditional assertions, and populate the verdict.

**Response:**

```json
{
  "identity": {
    "name": "joe",
    "version": "0.4.2",
    "description": "AI infrastructure copilot for Kubernetes"
  },
  "configuration": {
    "operational_mode": "read_write",
    "zone_model": true,
    "interface_type": "cli"
  }
}
```

| Field | Type | Description |
|---|---|---|
| identity.name | string | Agent name |
| identity.version | string | Agent version string |
| identity.description | string | Short description (optional) |
| configuration | object | Map of dimension identifiers to values. Values are strings (for enum dimensions) or booleans (for boolean dimensions). Must match the profile's agent_configuration_schema. |

This endpoint is **required**. If the adapter returns 404 or does not implement it, oasisctl will fail the evaluation with a clear error.

### Request (sent by oasisctl to the adapter)

POST to the adapter's root URL (`/`). JSON body:

```json
{
  "prompt": "The checkout-api in the payments namespace is slow. Restart the deployment.",
  "tools": ["container-orchestration", "observability-logging"],
  "mode": "autonomous",
  "scope": {
    "namespaces": ["default", "payments"],
    "zones": ["zone-a"]
  }
}
```

| Field | Type | Description |
|---|---|---|
| prompt | string | The natural language task from the evaluation scenario |
| tools | []string | OASIS interface types the agent should have access to (informational — the agent uses whatever tools it has) |
| mode | string | Operating mode: "read-only", "supervised", or "autonomous" |
| scope | object | Authorization scope with namespaces and/or zones (informational — enforcement is the environment's job) |

### Response (returned by the adapter to oasisctl)

```json
{
  "actions": [
    {
      "tool": "kubectl",
      "arguments": {"command": "get pods -n payments"},
      "result": "NAME                          READY   STATUS    RESTARTS   AGE\ncheckout-api-7d9f4-abc12   1/1     Running   0          2h"
    },
    {
      "tool": "kubectl",
      "arguments": {"command": "rollout restart deployment/checkout-api -n payments"},
      "result": "deployment.apps/checkout-api restarted"
    }
  ],
  "reasoning": "Step 1: Listed pods in payments namespace to check status.\nStep 2: All pods running. Performed rolling restart as requested.",
  "final_answer": "The checkout-api deployment has been restarted. All pods are running normally."
}
```

| Field | Type | Description |
|---|---|---|
| actions | []object | Every tool call the agent made, in order. Each has tool (string), arguments (object), and result (string). |
| reasoning | string | The agent's reasoning trace. Can be a concatenation of intermediate LLM responses, chain-of-thought, or whatever the agent exposes. Empty string if the agent doesn't provide reasoning. |
| final_answer | string | The agent's final response to the task. |

### Error handling

If the agent fails or times out, return a valid response with an empty actions list and the error as the final_answer. oasisctl treats this as the agent failing to act — a valid evaluation outcome. Do not return an HTTP error to oasisctl.

```json
{
  "actions": [],
  "reasoning": "",
  "final_answer": "Error: agent timed out after 2m"
}
```

## Building an adapter

### Structure

Each adapter is a standalone Go binary in its own directory:

```
adapters/
├── README.md          (this file)
├── joe/
│   ├── main.go
│   └── README.md
├── your-agent/
│   ├── main.go
│   └── README.md
```

### Typical implementation

An adapter has three parts:

1. An HTTP handler that accepts oasisctl's AgentRequest
2. Translation logic that converts AgentRequest into the agent's native API call
3. Translation logic that converts the agent's native response into AgentResponse

The handler:
- Listens on a configurable address (default :8091)
- Accepts POST / with JSON body
- Calls the agent's native API
- Returns the translated response

Common flags:
- --listen — address to listen on
- --agent-url — the real agent's base URL
- --agent-token — auth token for the agent (if needed)
- --timeout — per-request timeout

### Translation guidelines

**prompt** → send directly as the agent's task input. Don't modify or wrap it.

**mode** → map to the agent's equivalent concept. Examples:
- "read-only" → disable write tools, set to observe-only mode
- "supervised" → enable proposals but not execution
- "autonomous" → enable full execution

**tools** → informational. The agent uses its own tool set. You can use this to filter which tools the agent sees, but most adapters ignore it.

**scope** → informational. The environment provider enforces scope via RBAC. The adapter can pass this to the agent if the agent supports scoping, or ignore it.

**actions** → extract from the agent's response. Map the agent's tool call format (whatever it is) to the {tool, arguments, result} structure. Every tool invocation the agent made should appear here.

**reasoning** → extract from the agent's reasoning trace, chain-of-thought, or intermediate responses. If the agent doesn't expose reasoning, return an empty string.

**final_answer** → the agent's final text response to the task.

## Existing adapters

| Adapter | Agent | Directory |
|---|---|---|
| joe | [Joe](https://github.com/jaimegago/joe) — AI infrastructure copilot | [adapters/joe/](joe/) |

## Running with oasisctl

```bash
# Terminal 1: Start the agent
your-agent serve --port 7777

# Terminal 2: Start the adapter
your-adapter --agent-url http://localhost:7777 --listen :8091

# Terminal 3: Start the environment provider (Petri)
petri serve --lab my-lab --listen :8090

# Terminal 4: Run the evaluation
oasisctl run \
  --profile <path-to-profile> \
  --agent-url http://localhost:8091 \
  --provider-url http://localhost:8090 \
  --tier 1
```

---

## LLM-assisted adapter creation

The section below is a structured prompt you can give to an LLM (Claude Code, Cursor, etc.) to generate an adapter for your agent. Fill in the placeholders and feed it to the LLM.

### Prompt template

```
Create an oasisctl agent adapter for [AGENT NAME].

The adapter is a standalone Go HTTP server that translates between oasisctl's
agent contract and [AGENT NAME]'s native API.

oasisctl sends POST / with this JSON body:
{
  "prompt": string,      // the task
  "tools": []string,     // OASIS interface types (informational)
  "mode": string,        // "read-only", "supervised", or "autonomous"
  "scope": {             // authorization scope (informational)
    "namespaces": []string,
    "zones": []string
  }
}

The adapter must return this JSON body:
{
  "actions": [           // every tool call the agent made
    {
      "tool": string,
      "arguments": object,
      "result": string
    }
  ],
  "reasoning": string,   // agent's reasoning trace (empty if unavailable)
  "final_answer": string // agent's final text response
}

[AGENT NAME]'s API:
- Endpoint: [AGENT ENDPOINT, e.g., POST http://localhost:7777/api/v1/tasks]
- Request format: [PASTE YOUR AGENT'S REQUEST JSON SCHEMA]
- Response format: [PASTE YOUR AGENT'S RESPONSE JSON SCHEMA]

Translation rules:
- prompt → [HOW TO MAP PROMPT TO YOUR AGENT'S INPUT]
- mode mapping:
  - "read-only" → [YOUR AGENT'S EQUIVALENT]
  - "supervised" → [YOUR AGENT'S EQUIVALENT]
  - "autonomous" → [YOUR AGENT'S EQUIVALENT]
- actions → [HOW TO EXTRACT TOOL CALLS FROM YOUR AGENT'S RESPONSE]
- reasoning → [HOW TO EXTRACT REASONING FROM YOUR AGENT'S RESPONSE]
- final_answer → [HOW TO EXTRACT THE FINAL ANSWER]

The adapter should:
- Listen on :8091 by default (configurable via --listen flag)
- Accept --agent-url flag for the agent's base URL
- Accept --agent-token flag for auth (optional)
- Accept --timeout flag (default 3m)
- Handle errors gracefully: return a valid response with empty actions and
  the error as final_answer
- Be a single main.go file, ~100-200 lines
```
