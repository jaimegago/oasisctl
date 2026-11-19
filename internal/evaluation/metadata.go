package evaluation

import "encoding/json"

// NonScoringMetadata is cost and latency context for one agent execution: the
// agent's token accounting and the time it spent inside its own agent loop.
//
// It is context for a result, never an input to one. That is enforced by the
// type rather than by convention: every field is unexported and the only
// exported methods are MarshalJSON and UnmarshalJSON, so code holding one can carry it and write it
// out but cannot read a number from it. A scoring path that wanted to would have
// to serialize the value and parse it back — a circumvention that shows in
// review — rather than reach for a field. TestNonScoringMetadataIsOpaque pins
// the field and method sets.
//
// Two absences are kept apart from zero, and both survive to the artifact as
// JSON null:
//
//   - a cache count the agent's provider does not report (Gemini reports no
//     cache writes; an OpenAI-compatible provider reports neither count);
//   - a figure the agent did not report at all.
type NonScoringMetadata struct {
	tokens              *TokenAccounting
	agentLoopDurationMs *int
	// steps is the agent's own step count for the execution, as its adapter
	// reported it, or nil when it reported none. It is set by WithSteps and
	// is as unreadable as the rest.
	steps *int
}

// TokenAccounting is the input to NewNonScoringMetadata. The cache counts are
// nil when the provider does not report them, and non-nil — zero included —
// when it does.
type TokenAccounting struct {
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  *int
	CacheWriteTokens *int
}

// NewNonScoringMetadata copies its inputs into an opaque value. It returns nil
// when the agent reported neither figure, so an agent that reports nothing has
// no metadata rather than an empty one.
func NewNonScoringMetadata(tokens *TokenAccounting, agentLoopDurationMs *int) *NonScoringMetadata {
	if tokens == nil && agentLoopDurationMs == nil {
		return nil
	}
	m := &NonScoringMetadata{}
	if tokens != nil {
		t := TokenAccounting{
			InputTokens:      tokens.InputTokens,
			OutputTokens:     tokens.OutputTokens,
			CacheReadTokens:  copyInt(tokens.CacheReadTokens),
			CacheWriteTokens: copyInt(tokens.CacheWriteTokens),
		}
		m.tokens = &t
	}
	m.agentLoopDurationMs = copyInt(agentLoopDurationMs)
	return m
}

// WithSteps returns the metadata with the agent's reported step count
// recorded, building one where the agent reported nothing else. A nil count
// returns the receiver unchanged, so an adapter that reports no step count
// leaves an agent that reported nothing with no metadata at all.
//
// It is a writer and gives nothing a way to read a figure back out.
func (m *NonScoringMetadata) WithSteps(steps *int) *NonScoringMetadata {
	if steps == nil {
		return m
	}
	out := &NonScoringMetadata{}
	if m != nil {
		*out = *m
	}
	out.steps = copyInt(steps)
	return out
}

// metadataJSON is the serialized shape, shared by both directions so the
// artifact round-trips exactly.
type metadataJSON struct {
	Tokens              *tokensJSON `json:"tokens"`
	AgentLoopDurationMs *int        `json:"agent_loop_duration_ms"`
	Steps               *int        `json:"steps"`
}

type tokensJSON struct {
	InputTokens      int  `json:"input_tokens"`
	OutputTokens     int  `json:"output_tokens"`
	CacheReadTokens  *int `json:"cache_read_tokens"`
	CacheWriteTokens *int `json:"cache_write_tokens"`
}

// MarshalJSON writes every field, absent ones as null, so a reader never has
// to tell a missing key from a missing measurement.
func (m *NonScoringMetadata) MarshalJSON() ([]byte, error) {
	out := metadataJSON{AgentLoopDurationMs: m.agentLoopDurationMs, Steps: m.steps}
	if t := m.tokens; t != nil {
		out.Tokens = &tokensJSON{
			InputTokens:      t.InputTokens,
			OutputTokens:     t.OutputTokens,
			CacheReadTokens:  t.CacheReadTokens,
			CacheWriteTokens: t.CacheWriteTokens,
		}
	}
	return json.Marshal(out)
}

// UnmarshalJSON reads the shape MarshalJSON writes, so an artifact read back
// from disk carries the same metadata it was written with. It adds no way to
// read a figure out: the result is as opaque as the original.
func (m *NonScoringMetadata) UnmarshalJSON(data []byte) error {
	var in metadataJSON
	if err := json.Unmarshal(data, &in); err != nil {
		return err
	}
	*m = NonScoringMetadata{agentLoopDurationMs: in.AgentLoopDurationMs, steps: in.Steps}
	if t := in.Tokens; t != nil {
		m.tokens = &TokenAccounting{
			InputTokens:      t.InputTokens,
			OutputTokens:     t.OutputTokens,
			CacheReadTokens:  t.CacheReadTokens,
			CacheWriteTokens: t.CacheWriteTokens,
		}
	}
	return nil
}

func copyInt(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// ScenarioRecordMetadata is the non-scoring metadata of one scenario record
// (spec/05-reporting.md §1.2): step count, duration and token usage.
//
// It is reported and never scored, and the type holds that the way
// NonScoringMetadata does: every field is unexported and the only exported
// methods serialize it, so a scoring path holding one can write it out and
// cannot read a number from it. TestScenarioRecordMetadataIsOpaque pins the
// field and method sets.
//
// The three figures have two sources and each is null when its source gave
// nothing:
//
//   - steps and tokens are the agent's own, as its adapter reported them;
//   - duration is the runner's wall clock around the agent's execution of the
//     scenario, in seconds. It measures provider latency and load as much as
//     the agent, which is why the spec forbids scoring it.
type ScenarioRecordMetadata struct {
	steps           *int
	durationSeconds *float64
	tokens          *int
}

// NewScenarioRecordMetadata builds a scenario record's non-scoring metadata
// from what the agent reported and the wall clock the runner measured. The
// token figure is input plus output tokens. A nil wallClockSeconds is a
// scenario whose execution the runner did not time.
func NewScenarioRecordMetadata(agent *NonScoringMetadata, wallClockSeconds *float64) *ScenarioRecordMetadata {
	out := &ScenarioRecordMetadata{}
	if agent != nil {
		out.steps = copyInt(agent.steps)
		if t := agent.tokens; t != nil {
			total := t.InputTokens + t.OutputTokens
			out.tokens = &total
		}
	}
	if wallClockSeconds != nil {
		d := *wallClockSeconds
		out.durationSeconds = &d
	}
	return out
}

// recordMetadataWire is the serialized shape, shared by JSON and YAML and by
// both directions. Every key is written, absent figures as null.
type recordMetadataWire struct {
	Steps    *int     `json:"steps" yaml:"steps"`
	Duration *float64 `json:"duration" yaml:"duration"`
	Tokens   *int     `json:"tokens" yaml:"tokens"`
}

func (m *ScenarioRecordMetadata) wire() recordMetadataWire {
	return recordMetadataWire{Steps: m.steps, Duration: m.durationSeconds, Tokens: m.tokens}
}

func (m *ScenarioRecordMetadata) fromWire(w recordMetadataWire) {
	*m = ScenarioRecordMetadata{steps: w.Steps, durationSeconds: w.Duration, tokens: w.Tokens}
}

// MarshalJSON writes {steps, duration, tokens}, absent figures as null.
func (m *ScenarioRecordMetadata) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.wire())
}

// UnmarshalJSON reads the shape MarshalJSON writes, so a saved verdict
// re-renders with the metadata it was written with.
func (m *ScenarioRecordMetadata) UnmarshalJSON(data []byte) error {
	var w recordMetadataWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	m.fromWire(w)
	return nil
}

// MarshalYAML writes the same shape as MarshalJSON.
func (m *ScenarioRecordMetadata) MarshalYAML() (interface{}, error) {
	return m.wire(), nil
}

// UnmarshalYAML reads the shape MarshalYAML writes.
func (m *ScenarioRecordMetadata) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var w recordMetadataWire
	if err := unmarshal(&w); err != nil {
		return err
	}
	m.fromWire(w)
	return nil
}
