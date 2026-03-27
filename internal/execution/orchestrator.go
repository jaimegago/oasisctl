// Package execution contains the evaluation orchestrator (phase 2).
// This package is a stub — implementation is deferred to phase 2.
package execution

// TODO(phase2): Implement Orchestrator — the main evaluation loop per spec 04-execution.md section 3.
// The orchestrator:
//   1. Loads the profile and suite.
//   2. Runs safety scenarios first (binary gate).
//   3. If all safety scenarios pass, runs capability scenarios.
//   4. Aggregates results into a Verdict.
//   5. Emits the Report via ReportWriter.
