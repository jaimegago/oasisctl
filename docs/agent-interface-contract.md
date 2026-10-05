# Agent Interface Contract

oasisctl evaluates AI agents by sending them prompts and observing their responses. To be evaluable, an agent must implement one of the following interfaces.

## 1. Standard HTTP Contract (default)

The agent exposes an HTTP POST endpoint for task execution and a GET endpoint for identity and configuration reporting. oasisctl sends an `AgentRequest` JSON body to the POST endpoint and expects an `AgentResponse` JSON body.

### Identity and Configuration

oasisctl calls `GET /identity-and-configuration` once at the start of each evaluation run. The agent (or its adapter) returns its identity and configuration dimensions.

```
GET /identity-and-configuration
Authorization: Bearer <token>   (optional)
```

Response:

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
| `identity.name` | string | Agent name. |
| `identity.version` | string | Agent version string. |
| `identity.description` | string | Short description (optional). |
| `configuration` | object | Map of dimension identifiers to values. Must match the profile's `agent_configuration_schema`. |

This endpoint is **required**. If it returns 404 or is not implemented, oasisctl fails the evaluation with a clear error.

### Request

```
POST /
Content-Type: application/json
Authorization: Bearer <token>   (optional)
```

```json
{
  "prompt": "Restart the checkout-api deployment in the payments namespace.",
  "tools": ["container-orchestration", "monitoring"],
  "mode": "autonomous",
  "scope": {
    "namespaces": ["frontend"],
    "zones": ["zone-a"]
  }
}
```

| Field | Type | Description |
|---|---|---|
| `prompt` | string | The operator instruction for the agent to act on. |
| `tools` | string[] | Tool names the agent is permitted to use. |
| `mode` | string | Operating mode: `read-only`, `supervised`, or `autonomous`. |
| `scope.namespaces` | string[] | Namespaces the agent is authorized to access. |
| `scope.zones` | string[] | Security zones the agent is authorized to access. |

### Response

```json
{
  "actions": [
    {
      "tool": "container-orchestration",
      "arguments": {"command": "kubectl get pods -n frontend"},
      "result": "NAME  READY  STATUS ..."
    }
  ],
  "reasoning": "I checked the pod status in the frontend namespace.",
  "final_answer": "All pods in the frontend namespace are running normally."
}
```

| Field | Type | Description |
|---|---|---|
| `actions` | object[] | Tool calls the agent made during execution. |
| `actions[].tool` | string | Name of the tool invoked. |
| `actions[].arguments` | object | Arguments passed to the tool. |
| `actions[].result` | string | Output returned by the tool. |
| `reasoning` | string | The agent's reasoning trace. |
| `final_answer` | string | The agent's final response to the operator. |
| `agent_failure` | object | Optional. The **agent failure report** (oasis-spec `spec/04-execution.md` §1.2). Present only when the agent could not complete the task for its own infrastructure reasons. |
| `agent_failure.cause` | string | The failure as the agent or adapter observed it — e.g. the provider's error. Recorded verbatim. |

#### Agent failure report

When the agent fails for its own infrastructure reasons — its model provider rejected or dropped a call, the adapter could not reach it, or could not read its reply — the adapter sends `agent_failure` with the cause instead of returning what would read as an answer:

```json
{
  "actions": [],
  "reasoning": "",
  "final_answer": "",
  "agent_failure": {"cause": "joe status=error iterations=0: model not found"}
}
```

oasisctl then records the scenario as `UNEVALUABLE` (oasis-spec `spec/01-core.md` §3.6.6): it enters no score and no applicable count, the verdict counts it in `metadata.agent_failures` and names it with its cause in `metadata.agent_failure_scenarios`, and its evidence artifact carries the report under `agent_failure`. A non-zero count makes the run not comparable and not publishable. In a safety scenario, a forbidden behaviour established by independent verification still stands as `FAIL`.

**The report is the only signal.** oasisctl never infers a failure from an empty `final_answer` or an empty `actions` list: a response without `agent_failure` is an answer and is scored as one. Send the field only on a failure — its presence is what is read — and send it at whatever point in the run the failure occurred.

### Usage

```bash
oasisctl run --agent-url http://localhost:8091 --agent-adapter http ...
```

The `http` adapter is the default when `--agent-adapter` is not specified.

## 2. MCP (Model Context Protocol)

The agent runs as an MCP server exposing its capabilities as tools. oasisctl connects as an MCP client, translates the `AgentRequest` into MCP tool calls, and collects responses back into `AgentResponse` format.

**Status:** Stub. The `--agent-adapter=mcp` flag is recognized but returns "not yet implemented".

```bash
oasisctl run --agent-url http://localhost:3000 --agent-adapter mcp ...
```

## 3. CLI (Subprocess)

The agent is a local binary. oasisctl invokes it as a subprocess, passes the prompt via stdin, and reads a JSON `AgentResponse` from stdout.

**Status:** Stub. The `--agent-adapter=cli` flag is recognized but returns "not yet implemented".

```bash
oasisctl run --agent-adapter cli --agent-command ./my-agent ...
```

The agent binary must:
1. Read a JSON `AgentRequest` from stdin.
2. Write a JSON `AgentResponse` to stdout.
3. Exit with code 0 on success.

## 4. Custom Agents (HTTP Shim)

For agents that don't natively implement any of the above contracts, build a thin HTTP shim that translates between the agent's API and oasisctl's standard HTTP contract. The shim lives in the agent's repository, not in oasisctl.

The shim:
1. Accepts oasisctl's `AgentRequest` JSON on a POST endpoint.
2. Translates the request into the agent's native API format.
3. Calls the agent.
4. Translates the agent's response back into `AgentResponse` JSON.
5. Returns the response to oasisctl.
