// Package record keeps an evaluation's record on disk, where the caller says,
// so that a later process can continue it (spec/04-execution.md §3.1): the
// opening, every lab, every execution, and the safety verdict record once it
// is written.
//
// The layout under the evaluation directory:
//
//	evaluation.yaml                      what opening recorded; never rewritten
//	labs/<lab>.yaml                      one per lab, written when it is checked
//	executions/<scenario-id>/<n>.json    one per execution, opened then closed
//	evidence-<scenario-id>-<n>.json      the evidence artifact of each execution
//	safety-verdict-record.yaml           written once; its bytes are hashed
//
// A store is held by one process at a time. Two processes executing for one
// evaluation could each read a scenario one short of its count and both
// execute it, which the declared count forbids, so the directory is locked
// for as long as a store on it is open.
package record

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

const (
	openingFile      = "evaluation.yaml"
	labsDir          = "labs"
	executionsDir    = "executions"
	safetyRecordFile = "safety-verdict-record.yaml"
	lockFile         = ".lock"
)

// SafetyVerdictRecordFile is the name of the safety verdict record within an
// evaluation directory.
const SafetyVerdictRecordFile = safetyRecordFile

// ErrNoEvaluation is returned when a directory holds no evaluation.
var ErrNoEvaluation = errors.New("the directory holds no evaluation")

// ErrAlreadyOpened is returned when an evaluation is opened in a directory
// that already holds one. What opening recorded does not change afterwards
// (spec/01-core.md §2.4), so it is never overwritten.
var ErrAlreadyOpened = errors.New("the directory already holds an evaluation")

// ErrLocked is returned when another process holds the evaluation.
var ErrLocked = errors.New("the evaluation is held by another process")

// ErrRecordClosed is returned on an attempt to close an execution record that
// is already closed. A closed record is not altered (spec/04-execution.md
// §3.4).
var ErrRecordClosed = errors.New("the execution record is already closed, and a closed record is not altered")

// ErrSafetyRecordWritten is returned on an attempt to write the safety verdict
// record a second time. It is written once and is not rewritten
// (spec/01-core.md §2.6).
var ErrSafetyRecordWritten = errors.New("the safety verdict record is already written, and it is not rewritten")

// Store is one evaluation's record.
type Store struct {
	dir     string
	opening evaluation.EvaluationOpening
	lock    *os.File
	mu      sync.Mutex
}

// Exists reports whether dir holds an evaluation.
func Exists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, openingFile))
	return err == nil
}

// Create opens a new evaluation in dir, recording what opening fixes. It
// refuses a directory that already holds one.
func Create(dir string, opening evaluation.EvaluationOpening) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create evaluation directory %s: %w", dir, err)
	}
	lock, err := acquireLock(filepath.Join(dir, lockFile))
	if err != nil {
		return nil, err
	}
	data, err := yaml.Marshal(opening)
	if err != nil {
		releaseLock(lock)
		return nil, fmt.Errorf("marshal the evaluation's opening: %w", err)
	}
	if err := writeOnce(filepath.Join(dir, openingFile), data); err != nil {
		releaseLock(lock)
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%w: %s", ErrAlreadyOpened, dir)
		}
		return nil, fmt.Errorf("record the evaluation's opening: %w", err)
	}
	return &Store{dir: dir, opening: opening, lock: lock}, nil
}

// Open loads the evaluation dir holds and takes its lock.
func Open(dir string) (*Store, error) {
	data, err := os.ReadFile(filepath.Join(dir, openingFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNoEvaluation, dir)
		}
		return nil, fmt.Errorf("read the evaluation's opening: %w", err)
	}
	var opening evaluation.EvaluationOpening
	if err := yaml.Unmarshal(data, &opening); err != nil {
		return nil, fmt.Errorf("parse the evaluation's opening in %s: %w", dir, err)
	}
	lock, err := acquireLock(filepath.Join(dir, lockFile))
	if err != nil {
		return nil, err
	}
	return &Store{dir: dir, opening: opening, lock: lock}, nil
}

// Close releases the evaluation. The record stays where it is.
func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	releaseLock(s.lock)
	s.lock = nil
}

// Dir is the evaluation directory.
func (s *Store) Dir() string { return s.dir }

// Opening is what opening the evaluation recorded.
func (s *Store) Opening() evaluation.EvaluationOpening { return s.opening }

// writeOnce writes a file that must not exist yet.
func writeOnce(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// replace writes a file through a temporary one, so a reader never sees half
// of it and a process killed mid-write leaves the old content in place.
func replace(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}

// safeName makes an identifier usable as one path element.
func safeName(id string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == filepath.Separator {
			return '_'
		}
		return r
	}, id)
}

// AddLab records a lab. A lab identifier is unique within the evaluation, so
// one that is already recorded is refused.
func (s *Store) AddLab(lab evaluation.LabRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.dir, labsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	data, err := yaml.Marshal(lab)
	if err != nil {
		return fmt.Errorf("marshal lab %s: %w", lab.Lab, err)
	}
	if err := writeOnce(filepath.Join(dir, safeName(lab.Lab)+".yaml"), data); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("lab %q is already recorded for this evaluation", lab.Lab)
		}
		return fmt.Errorf("record lab %s: %w", lab.Lab, err)
	}
	return nil
}

// Labs returns every lab recorded, in order of creation.
func (s *Store) Labs() ([]evaluation.LabRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(filepath.Join(s.dir, labsDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []evaluation.LabRecord
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, labsDir, e.Name()))
		if err != nil {
			return nil, err
		}
		var lab evaluation.LabRecord
		if err := yaml.Unmarshal(data, &lab); err != nil {
			return nil, fmt.Errorf("parse lab record %s: %w", e.Name(), err)
		}
		out = append(out, lab)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *Store) executionPath(scenarioID string, ordinal int) string {
	return filepath.Join(s.dir, executionsDir, safeName(scenarioID), fmt.Sprintf("%04d.json", ordinal))
}

// OpenExecution opens the record of a new execution of a scenario, before
// anything is provisioned (spec/04-execution.md §3.3 step 5), and returns the
// ordinal it was given: the next among that scenario's executions, attempts
// included. The record's Execution field is set to it.
func (s *Store) OpenExecution(scenarioID string, class evaluation.Classification, rec evaluation.ExecutionRecord) (evaluation.StoredExecution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.dir, executionsDir, safeName(scenarioID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return evaluation.StoredExecution{}, fmt.Errorf("create %s: %w", dir, err)
	}
	existing, err := s.scenarioExecutions(scenarioID)
	if err != nil {
		return evaluation.StoredExecution{}, err
	}
	rec.Execution = len(existing) + 1
	rec.Counted = false
	rec.Verdict = nil
	rec.Outcome = ""
	rec.Cause = ""
	rec.EndedAt = nil
	stored := evaluation.StoredExecution{
		ScenarioID:     scenarioID,
		Classification: class,
		State:          evaluation.ExecutionOpen,
		Record:         rec,
	}
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return evaluation.StoredExecution{}, fmt.Errorf("marshal execution record: %w", err)
	}
	if err := writeOnce(s.executionPath(scenarioID, rec.Execution), append(data, '\n')); err != nil {
		return evaluation.StoredExecution{}, fmt.Errorf("open execution record %d of %s: %w", rec.Execution, scenarioID, err)
	}
	return stored, nil
}

// CloseExecution closes an open execution record, as a counted execution or
// as an attempt. A record already closed is not altered.
func (s *Store) CloseExecution(stored evaluation.StoredExecution) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.executionPath(stored.ScenarioID, stored.Record.Execution)
	current, err := readExecution(path)
	if err != nil {
		return fmt.Errorf("read execution record %d of %s: %w", stored.Record.Execution, stored.ScenarioID, err)
	}
	if current.State == evaluation.ExecutionClosed {
		return fmt.Errorf("execution %d of %s: %w", stored.Record.Execution, stored.ScenarioID, ErrRecordClosed)
	}
	stored.State = evaluation.ExecutionClosed
	if why := stored.Record.Malformed(); why != "" {
		return fmt.Errorf("execution %d of %s cannot be closed: %s", stored.Record.Execution, stored.ScenarioID, why)
	}
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal execution record: %w", err)
	}
	if err := replace(path, append(data, '\n')); err != nil {
		return fmt.Errorf("close execution record %d of %s: %w", stored.Record.Execution, stored.ScenarioID, err)
	}
	return nil
}

func readExecution(path string) (evaluation.StoredExecution, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return evaluation.StoredExecution{}, err
	}
	var stored evaluation.StoredExecution
	if err := json.Unmarshal(data, &stored); err != nil {
		return evaluation.StoredExecution{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return stored, nil
}

// scenarioExecutions reads one scenario's execution records, in ordinal
// order. The caller holds the mutex.
func (s *Store) scenarioExecutions(scenarioID string) ([]evaluation.StoredExecution, error) {
	dir := filepath.Join(s.dir, executionsDir, safeName(scenarioID))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	type numbered struct {
		n    int
		name string
	}
	var files []numbered
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		files = append(files, numbered{n, e.Name()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].n < files[j].n })
	out := make([]evaluation.StoredExecution, 0, len(files))
	for _, f := range files {
		stored, err := readExecution(filepath.Join(dir, f.name))
		if err != nil {
			return nil, err
		}
		out = append(out, stored)
	}
	return out, nil
}

// ScenarioExecutions returns one scenario's execution records, in order of
// start.
func (s *Store) ScenarioExecutions(scenarioID string) ([]evaluation.StoredExecution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scenarioExecutions(scenarioID)
}

// Executions returns every execution record of the evaluation: for each
// scenario, in order of start, with the scenarios in a stable order.
func (s *Store) Executions() ([]evaluation.StoredExecution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root := filepath.Join(s.dir, executionsDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []evaluation.StoredExecution
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, e.Name()))
		if err != nil {
			return nil, err
		}
		var one []evaluation.StoredExecution
		for _, f := range files {
			if f.IsDir() || filepath.Ext(f.Name()) != ".json" {
				continue
			}
			stored, err := readExecution(filepath.Join(root, e.Name(), f.Name()))
			if err != nil {
				return nil, err
			}
			one = append(one, stored)
		}
		sort.Slice(one, func(i, j int) bool { return one[i].Record.Execution < one[j].Record.Execution })
		out = append(out, one...)
	}
	return out, nil
}

// SafetyVerdictRecord returns the bytes of the safety verdict record as it
// was written, or nil where none has been written. The bytes are what the
// hash is taken over (spec/05-reporting.md §1.4).
func (s *Store) SafetyVerdictRecord() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(s.dir, safetyRecordFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read the safety verdict record: %w", err)
	}
	return data, nil
}

// WriteSafetyVerdictRecord writes the safety verdict record. It is written
// once: a second write is refused.
func (s *Store) WriteSafetyVerdictRecord(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeOnce(filepath.Join(s.dir, safetyRecordFile), data); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrSafetyRecordWritten
		}
		return fmt.Errorf("write the safety verdict record: %w", err)
	}
	return nil
}

// ParseSafetyVerdictRecord reads a safety verdict record's bytes.
func ParseSafetyVerdictRecord(data []byte) (evaluation.SafetyVerdictRecord, error) {
	var doc evaluation.SafetyVerdictRecordDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return evaluation.SafetyVerdictRecord{}, fmt.Errorf("parse the safety verdict record: %w", err)
	}
	return doc.Record, nil
}

// MarshalSafetyVerdictRecord renders a safety verdict record as the document
// it is published as. The bytes returned are the bytes to write and to hash.
func MarshalSafetyVerdictRecord(rec evaluation.SafetyVerdictRecord) ([]byte, error) {
	return yaml.Marshal(evaluation.SafetyVerdictRecordDocument{Record: rec})
}
