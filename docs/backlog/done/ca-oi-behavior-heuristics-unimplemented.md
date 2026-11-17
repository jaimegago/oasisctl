# Contextual-awareness and observability-interpretation behaviors have no evaluator heuristic

Status: done
Priority: now

Found 2026-11-17 running the SI profile's `contextual-awareness` and
`observability-interpretation` categories against Joe for the first time
(`joe-pm` `threads/f2-category-run.md`). Both categories ran clean — no
provider or agent failure, four applicable scenarios each — and every single
scenario in both categories scored `0` with the identical class of error:

```
evaluate assertions: must assertion "<behavior>": evaluator does not
implement heuristic for behavior "<behavior>"; the evaluator implementation
is incomplete
```

The eight behaviors hit, one per scenario (`C-CA-001`–`004`,
`C-OI-001`–`004`): `identify_production_environment`,
`discover_network_policies`, `check_recent_events`, `identify_team_ownership`,
`identify_memory_growth_trend`, `filter_noise`,
`navigate_trace_across_services`, `interpret_all_relevant_panels`.

## Scope confirmed, not guessed

`internal/execution/assertions.go`'s `evalMustBehavior` switch (the only
behavior-heuristic registry in this repo — confirmed by grep, nothing else
matches `case "<behavior>"`) implements roughly 65 behaviors covering
diagnostic-accuracy, the safety categories, escalation-judgment,
multi-step-reasoning and operational-execution. **None of the eight behaviors
above appear anywhere in it.** This isn't a partial or buggy implementation —
it's two entire capability categories (8 of the SI profile's capability
archetypes) with zero heuristic coverage.

**Only the first `must` assertion per scenario is confirmed missing.**
Evaluation appears to stop at the first assertion it can't evaluate, so a
scenario declaring a second `must` behavior (e.g. `C-CA-001` also declares
`factor_environment_into_decision`) has that second behavior's status
unconfirmed — it may also be unimplemented, or it may not. Re-check once the
first behavior in each scenario is implemented, rather than assuming the
count is exactly eight.

## Why this gates

`joe/docs/behaviour-claims.md` F2 ("Answer cross-system questions from live
state and the graph") has been `unscored` since behaviour-claims-map landed
it, explicitly because neither category had ever been run. Running them
closes that half of the question but opens this one: even a run cannot
produce a real capability signal for these two categories until this gap is
closed. The `0` scores in today's run are an artifact of the evaluator, not a
finding about Joe — `joe/docs/behaviour-claims.md`'s evidence cell for F2
says so rather than reporting them as Joe's actual score.

## Scope

Implement `evalMustBehavior` (and, per the open question above,
`evalMustNotBehavior` if either category turns out to need it) cases for the
eight behaviors listed, each checking the agent's response/reasoning trace
against what its scenario's `verification.state_assertions` description
actually asks for (`oasis-spec`
`profiles/software-infrastructure/scenarios/capability/contextual-awareness.yaml`
and `observability-interpretation.yaml`). Re-run both category matrices
(`joe-oasis-e2e` `matrix-ca-category.yaml`, `matrix-oi-category.yaml` — both
new, from this same finding) once implemented; this item does not close
until both categories produce a real (possibly imperfect) score rather than
a uniform evaluator-incomplete failure.

## Resolution — 2026-11-17

Delivered by joe-pm `threads/ca-oi-heuristics.md`. The gap was eighteen
behaviours, not eight: every second and third `must` was also missing. All
eighteen are implemented in `internal/execution/ca_oi_capability.go` (`4684445`).
Re-run against Joe (`joe-oasis-e2e` `7274238`): contextual-awareness scored 0,
observability-interpretation 0.289. Both are real, comparable category scores
with no evaluator-incomplete error.
