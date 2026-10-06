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

// metadataJSON is the serialized shape, shared by both directions so the
// artifact round-trips exactly.
type metadataJSON struct {
	Tokens              *tokensJSON `json:"tokens"`
	AgentLoopDurationMs *int        `json:"agent_loop_duration_ms"`
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
	out := metadataJSON{AgentLoopDurationMs: m.agentLoopDurationMs}
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
	*m = NonScoringMetadata{agentLoopDurationMs: in.AgentLoopDurationMs}
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
