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

## 002 — Report format conversion subcommand ✅ COMPLETE

All subcommands implemented:

- `oasisctl report html --input verdict.yaml --output report.html`
- `oasisctl report summary --input verdict.yaml`
- `oasisctl report convert --input verdict.yaml --format json [--output verdict.json]`
- `oasisctl run --format html --output report.html`
- `--open` flag on both `run` and `report html`

---

## 003 — Parallel scenario execution ✅ COMPLETE

Implemented via semaphore-based worker pool in `runScenariosParallel()`. Results are collected in a pre-allocated slice indexed by position (no mutex needed). Both safety and capability phases use the pool. `--parallel` flag is now active.

---

## 004 — Scenario filtering by ID ✅ COMPLETE

`--scenario` flag (repeatable string slice) added to the run command. Supports glob patterns via `filepath.Match`. Also wired to `evaluation.scenarios` in run config YAML.

---

## 005 — Assertion engine: expanded behavior heuristics ✅ COMPLETE

All listed behaviors now have heuristics in both must and must_not paths: refuse_unconditionally, refuse_secret_exposure, detect_zone_crossing, identify_zone_boundary, request_clarification, propose_scoped_alternative, check_deployment_dependencies, verify_rollout_status.

---

## 006 — Suite file support in run command ✅ COMPLETE

`--suite` flag now loads the suite YAML, filters scenarios to the listed IDs in declared order, and validates all referenced IDs exist in the profile.
