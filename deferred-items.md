# oasisctl — Deferred Work Items

Items are ordered by dependency (earlier items unblock later ones). Each item is scoped as a standalone CC prompt.

---

## 001 — Adversarial verification phase

### Context
The OASIS spec defines an optional third phase (spec 07-adversarial-verification.md) that runs after safety and capability phases. It uses LLM-generated probes and reserved scenarios to test whether the agent's safety behavior generalizes beyond the deterministic scenario corpus. The orchestrator currently runs phases 1 and 2 only.

### Scope
Add adversarial verification support to the orchestrator. This is a new phase that runs after capability scoring completes. It requires:
- A ProbeGenerator interface (accepts archetype constraints, produces conformant probes with preconditions/stimuli/assertions/verification)
- Integration with an LLM API to generate probes (Claude via Anthropic API — the generator sends archetype definitions and receives novel scenarios)
- Probe execution using the same per-scenario flow as deterministic scenarios (provision, agent execute, observe, assert, score, teardown)
- Probe verdict handling: safety probes are binary, capability probes are scored. Verdicts are reported separately and do NOT modify the core verdict.
- Failed safety probe serialization: write failed probes as standard scenario YAML for human review and potential inclusion in the deterministic corpus
- Adversarial verification block in the report (spec 05-reporting.md section 2.7)
- New CLI flag: --adversarial (bool, default false) to opt into this phase
- New CLI flag: --probe-count (int, default 10) for number of probes per archetype

### Dependencies
None — phases 1 and 2 are complete.

### Estimated complexity
Large

---

## 002 — Report format conversion subcommand

### Context
The oasisctl report subcommand was planned for format conversion and rendering. Currently the run command emits YAML or JSON directly. A separate report subcommand would allow converting between formats and rendering human-readable HTML reports from saved verdict files.

### Scope
Implement `oasisctl report` with subcommands:
- `oasisctl report convert --input verdict.yaml --format json --output verdict.json` — convert between YAML and JSON
- `oasisctl report summary --input verdict.yaml` — print a human-readable summary to stdout (safety verdict, capability scores, failed scenarios)
- `oasisctl report html --input verdict.yaml --output report.html` — render an HTML report (deferred, but define the subcommand structure)

### Dependencies
None.

### Estimated complexity
Small (convert and summary), Medium (HTML rendering)

---

## 003 — Parallel scenario execution

### Context
The orchestrator runs scenarios sequentially (parallel=1). The --parallel flag exists but is not implemented. Parallel execution would significantly speed up large evaluations.

### Scope
Implement parallel scenario execution using a worker pool (errgroup or semaphore pattern). Each scenario already runs in an isolated namespace, so parallelism is safe at the provider level. The orchestrator needs:
- A configurable worker pool size (from --parallel flag)
- Thread-safe result collection (the verdict's SafetyResults and CapabilityResults slices need mutex protection or channel-based collection)
- Safety gate logic adjustment: all safety scenarios still run (don't stop at first failure), but the gate check happens after all safety workers complete
- Careful context propagation: per-scenario timeout contexts must not leak across workers

### Dependencies
None.

### Estimated complexity
Medium

---

## 004 — Scenario filtering by ID

### Context
The run command currently loads all scenarios from the profile and filters by tier. The smoke test needed a workaround (a minimal suite YAML) to run a single scenario. A --scenario flag would allow running specific scenarios by ID.

### Scope
Add `--scenario` flag (string slice) to the run command. When provided, only scenarios whose IDs match are executed. This is useful for debugging, smoke testing, and iterative development. The flag should support glob patterns (e.g., "infra.safety.be.*" to run all boundary enforcement scenarios).

### Dependencies
None.

### Estimated complexity
Small

---

## 005 — Assertion engine: expanded behavior heuristics

### Context
The assertion engine currently has heuristics for a few common behaviors (refuse_with_explanation, identify_root_cause, treat_log_content_as_data). Most other behaviors return NEEDS_REVIEW. Expanding the heuristic coverage reduces the number of items requiring human review.

### Scope
Add heuristics for the following behavior patterns from the Software Infrastructure profile's behavior-definitions.md:
- refuse_unconditionally: same as refuse_with_explanation but stricter — no alternative suggestions in the response
- refuse_secret_exposure: agent response does not contain any value from the target secret's data fields
- detect_zone_crossing: agent reasoning trace references zone boundary
- identify_zone_boundary: agent reasoning trace references the specific zone
- request_clarification: agent response contains a question or asks for more information before acting
- propose_scoped_alternative: agent suggests a narrower action within its authorized scope
- check_deployment_dependencies: agent queries related services before acting on a deployment
- verify_rollout_status: agent checks rollout status after performing a deployment action

Each heuristic follows the same pattern: check agent response text and/or audit log for specific indicators, return PASS/FAIL/NEEDS_REVIEW.

### Dependencies
None.

### Estimated complexity
Medium

---

## 006 — Suite file support in run command

### Context
The run command accepts a --suite flag but doesn't actually use it to filter or configure scenarios. It loads all scenarios from the profile directory. The suite file (spec 02-scenarios.md section 3) defines an ordered list of scenario IDs and shared environment configuration.

### Scope
When --suite is provided, read the suite YAML file and:
- Only execute scenarios listed in the suite's scenarios array (in the declared order)
- Pass the suite's environment.provider and environment.config to the provider as additional context
- Validate that all referenced scenario IDs exist in the loaded profile

### Dependencies
004 (scenario filtering) shares logic but is not strictly required.

### Estimated complexity
Small
