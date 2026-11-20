package record

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

func opening() evaluation.EvaluationOpening {
	evaluator := "tester"
	return evaluation.EvaluationOpening{
		Evaluation: "eval-1",
		OpenedAt:   time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC),
		Identity: evaluation.EvaluationIdentity{
			Agent: "agent", AgentVersion: "1.0.0",
			AgentConfiguration: evaluation.AgentConfiguration{"operational_mode": "read_only", "zone_model": true},
			Evaluator:          &evaluator,
		},
		ExecutionCounts: evaluation.ExecutionCounts{Safety: 3, Capability: 4},
		Scenarios: []evaluation.OpeningScenario{
			{ScenarioID: "s.one", Classification: evaluation.ClassificationSafety, Applicable: true},
		},
	}
}

// What opening recorded is the evaluation's from then on: a later holder of
// the record reads it back as it was written, and it is not opened twice.
func TestStore_TheOpeningOutlivesItsProcessAndIsNotRewritten(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evaluation")
	require.False(t, Exists(dir))

	s, err := Create(dir, opening())
	require.NoError(t, err)
	s.Close()
	require.True(t, Exists(dir))

	_, err = Create(dir, opening())
	require.ErrorIs(t, err, ErrAlreadyOpened)

	s, err = Open(dir)
	require.NoError(t, err)
	defer s.Close()
	assert.Equal(t, opening(), s.Opening())

	_, err = Open(filepath.Join(t.TempDir(), "nothing-here"))
	require.ErrorIs(t, err, ErrNoEvaluation)
}

// One process holds an evaluation at a time: two could each find a scenario
// one short of its count and both execute it.
func TestStore_IsHeldByOneProcessAtATime(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evaluation")
	s, err := Create(dir, opening())
	require.NoError(t, err)

	_, err = Open(dir)
	require.ErrorIs(t, err, ErrLocked)

	s.Close()
	again, err := Open(dir)
	require.NoError(t, err, "the record is released when its holder lets go")
	again.Close()
}

// An execution's record is opened before the execution starts, gets the next
// ordinal among the scenario's executions, and is closed once.
func TestStore_ExecutionRecordsAreOpenedThenClosedOnce(t *testing.T) {
	s, err := Create(filepath.Join(t.TempDir(), "evaluation"), opening())
	require.NoError(t, err)
	defer s.Close()

	started := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	first, err := s.OpenExecution("s.one", evaluation.ClassificationSafety, evaluation.ExecutionRecord{Evaluation: "eval-1", StartedAt: started, Lab: "lab-1"})
	require.NoError(t, err)
	assert.Equal(t, 1, first.Record.Execution)
	assert.Equal(t, evaluation.ExecutionOpen, first.State)

	// Found open by a reader, it reads as the INTERRUPTED attempt it would be.
	found, err := s.ScenarioExecutions("s.one")
	require.NoError(t, err)
	require.Len(t, found, 1)
	read := found[0].AsRead()
	assert.False(t, read.Counted)
	assert.Equal(t, evaluation.AttemptInterrupted, read.Outcome)
	assert.NotEmpty(t, read.Cause)
	assert.Nil(t, read.EndedAt)

	// A record that is neither counted nor an attempt cannot be closed.
	require.Error(t, s.CloseExecution(first))

	ended := started.Add(time.Minute)
	first.Record.Counted = true
	first.Record.Verdict = evaluation.StatusVerdict(evaluation.ScenarioPass)
	first.Record.EndedAt = &ended
	require.NoError(t, s.CloseExecution(first))
	require.ErrorIs(t, s.CloseExecution(first), ErrRecordClosed, "a closed record is not altered")

	second, err := s.OpenExecution("s.one", evaluation.ClassificationSafety, evaluation.ExecutionRecord{Evaluation: "eval-1", StartedAt: ended, Lab: "lab-1"})
	require.NoError(t, err)
	assert.Equal(t, 2, second.Record.Execution, "attempts and counted executions share one sequence of ordinals")
	second.Record.Outcome = evaluation.AttemptProviderFailure
	second.Record.Cause = "lab lost"
	require.NoError(t, s.CloseExecution(second))

	all, err := s.Executions()
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "PASS", all[0].Record.Verdict.Status())
	assert.True(t, all[0].Record.Counted)
	assert.Equal(t, evaluation.AttemptProviderFailure, all[1].Record.Outcome)
	assert.Equal(t, evaluation.ExecutionClosed, all[1].State)
}

// The safety verdict record is written once, and what is read back is the
// bytes that were written: the hash is taken over them.
func TestStore_TheSafetyVerdictRecordIsWrittenOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evaluation")
	s, err := Create(dir, opening())
	require.NoError(t, err)
	defer s.Close()

	none, err := s.SafetyVerdictRecord()
	require.NoError(t, err)
	assert.Nil(t, none)

	data, err := MarshalSafetyVerdictRecord(evaluation.SafetyVerdictRecord{
		Evaluation: "eval-1", Safety: evaluation.SafetyVerdictPass,
		ExecutionCounts: evaluation.ExecutionCounts{Safety: 3, Capability: 4},
		NotApplicable:   []evaluation.ExcludedScenario{}, Scenarios: []evaluation.ScenarioRecord{},
	})
	require.NoError(t, err)
	require.NoError(t, s.WriteSafetyVerdictRecord(data))
	require.ErrorIs(t, s.WriteSafetyVerdictRecord(data), ErrSafetyRecordWritten)

	read, err := s.SafetyVerdictRecord()
	require.NoError(t, err)
	assert.Equal(t, data, read)
	onDisk, err := os.ReadFile(filepath.Join(dir, SafetyVerdictRecordFile))
	require.NoError(t, err)
	assert.Equal(t, evaluation.HashSafetyVerdictRecord(onDisk), evaluation.HashSafetyVerdictRecord(data))

	rec, err := ParseSafetyVerdictRecord(read)
	require.NoError(t, err)
	assert.Equal(t, evaluation.SafetyVerdictPass, rec.Safety)
	assert.Equal(t, "eval-1", rec.Evaluation)
}

// A lab is recorded once under its identifier.
func TestStore_Labs(t *testing.T) {
	s, err := Create(filepath.Join(t.TempDir(), "evaluation"), opening())
	require.NoError(t, err)
	defer s.Close()

	first := evaluation.LabRecord{Lab: "lab-1", CreatedAt: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC), Provider: "petri", ProviderVersion: "0.1.0+33bc680"}
	second := evaluation.LabRecord{Lab: "lab-2", CreatedAt: first.CreatedAt.Add(24 * time.Hour), Provider: "petri", ProviderVersion: "0.1.0+33bc680"}
	require.NoError(t, s.AddLab(second))
	require.NoError(t, s.AddLab(first))
	require.Error(t, s.AddLab(first), "a lab identifier is unique within the evaluation")

	labs, err := s.Labs()
	require.NoError(t, err)
	require.Len(t, labs, 2)
	assert.Equal(t, "lab-1", labs[0].Lab, "in order of creation")
	assert.Equal(t, "lab-2", labs[1].Lab)
}
