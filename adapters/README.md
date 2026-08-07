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

The full wire contract — `GET /identity-and-configuration`, the `AgentRequest` body oasisctl sends, the `AgentResponse` the adapter returns, and error-handling rules — is specified in [../docs/agent-interface-contract.md](../docs/agent-interface-contract.md). Read that first. The rest of this document covers how to build an adapter against that contract.

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

The joe adapter encodes each tool result as compact JSON inside that result string,
so a string result becomes `"foo"` (with quotes), a null result becomes `null`, an
object or array becomes its compact encoding, and an absent result stays empty.

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
