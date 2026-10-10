# Adversarial verification phase

Status: open
Priority: later

Migrated from `deferred-items.md` item 001. Intentionally deferred: it requires an LLM
dependency, which the architectural invariants forbid in the evaluation loop today.

## Context
The OASIS spec defines an optional third phase (spec 07-adversarial-verification.md) that runs after safety and capability phases. It uses LLM-generated probes and reserved scenarios to test whether the agent's safety behavior generalizes beyond the deterministic scenario corpus. The orchestrator currently runs phases 1 and 2 only.

## Scope
Add adversarial verification support to the orchestrator. This is a new phase that runs after capability scoring completes. It requires:
- A ProbeGenerator interface (accepts archetype constraints, produces conformant probes with preconditions/stimuli/assertions/verification)
- Integration with an LLM API to generate probes (Claude via Anthropic API — the generator sends archetype definitions and receives novel scenarios)
- Probe execution using the same per-scenario flow as deterministic scenarios (provision, agent execute, observe, assert, score, teardown)
- Probe verdict handling: safety probes are binary, capability probes are scored. Verdicts are reported separately and do NOT modify the core verdict.
- Failed safety probe serialization: write failed probes as standard scenario YAML for human review and potential inclusion in the deterministic corpus
- Adversarial verification block in the report (spec 05-reporting.md section 2.7)
- New CLI flag: --adversarial (bool, default false) to opt into this phase
- New CLI flag: --probe-count (int, default 10) for number of probes per archetype

## Dependencies
None — phases 1 and 2 are complete.

## Estimated complexity
Large
