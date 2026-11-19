package evaluation

import (
	"encoding/json"
	"reflect"
	"testing"
)

func intPtr(n int) *int { return &n }

// TestNonScoringMetadataIsOpaque is what makes the metadata unable to become
// scoring, not merely unread by it (joe-pm threads/cost-latency-metadata-wire.md,
// invariant 1). A field or accessor added here gives every assertion, band and
// verdict a way to read a cost or latency figure; this test fails first.
func TestNonScoringMetadataIsOpaque(t *testing.T) {
	typ := reflect.TypeOf(NonScoringMetadata{})
	for i := 0; i < typ.NumField(); i++ {
		if f := typ.Field(i); f.IsExported() {
			t.Errorf("NonScoringMetadata has exported field %s", f.Name)
		}
	}
	// WithSteps is a writer: it takes a count in and returns the same opaque
	// type, so it gives no caller a way to read a figure.
	allowed := map[string]bool{"MarshalJSON": true, "UnmarshalJSON": true, "WithSteps": true}
	ptr := reflect.TypeOf(&NonScoringMetadata{})
	for i := 0; i < ptr.NumMethod(); i++ {
		m := ptr.Method(i)
		if !allowed[m.Name] {
			t.Errorf("NonScoringMetadata has exported method %s; only serialization and writers are permitted", m.Name)
		}
		if m.Name == "WithSteps" {
			if m.Type.NumOut() != 1 || m.Type.Out(0) != ptr {
				t.Errorf("WithSteps must return only *NonScoringMetadata, got %s", m.Type)
			}
		}
	}
}

// TestScenarioRecordMetadataIsOpaque holds the scenario record's non-scoring
// metadata to the same rule: reported, never readable by anything that scores
// (spec/05-reporting.md §1.2).
func TestScenarioRecordMetadataIsOpaque(t *testing.T) {
	typ := reflect.TypeOf(ScenarioRecordMetadata{})
	for i := 0; i < typ.NumField(); i++ {
		if f := typ.Field(i); f.IsExported() {
			t.Errorf("ScenarioRecordMetadata has exported field %s", f.Name)
		}
	}
	allowed := map[string]bool{"MarshalJSON": true, "UnmarshalJSON": true, "MarshalYAML": true, "UnmarshalYAML": true}
	ptr := reflect.TypeOf(&ScenarioRecordMetadata{})
	for i := 0; i < ptr.NumMethod(); i++ {
		if name := ptr.Method(i).Name; !allowed[name] {
			t.Errorf("ScenarioRecordMetadata has exported method %s; only serialization is permitted", name)
		}
	}
}

// Steps and tokens come from the agent, duration from the runner, and each is
// null when its source gave nothing.
func TestScenarioRecordMetadata_JSON(t *testing.T) {
	secs := 12.5
	tests := []struct {
		name  string
		agent *NonScoringMetadata
		wall  *float64
		want  string
	}{
		{
			name:  "everything reported",
			agent: NewNonScoringMetadata(&TokenAccounting{InputTokens: 900, OutputTokens: 40}, nil).WithSteps(intPtr(7)),
			wall:  &secs,
			want:  `{"steps":7,"duration":12.5,"tokens":940}`,
		},
		{
			name: "the agent reported nothing",
			wall: &secs,
			want: `{"steps":null,"duration":12.5,"tokens":null}`,
		},
		{
			name:  "steps only, execution not timed",
			agent: (*NonScoringMetadata)(nil).WithSteps(intPtr(0)),
			want:  `{"steps":0,"duration":null,"tokens":null}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewScenarioRecordMetadata(tt.agent, tt.wall)
			got, err := json.Marshal(m)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("marshal = %s, want %s", got, tt.want)
			}
			var back ScenarioRecordMetadata
			if err := json.Unmarshal(got, &back); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			again, _ := json.Marshal(&back)
			if string(again) != tt.want {
				t.Errorf("round trip = %s, want %s", again, tt.want)
			}
		})
	}
}

func TestNewNonScoringMetadata_NothingReportedIsNil(t *testing.T) {
	if m := NewNonScoringMetadata(nil, nil); m != nil {
		t.Errorf("NewNonScoringMetadata(nil, nil) = %v, want nil", m)
	}
}

// A reported zero and an unreported count serialize differently, and both
// survive a round trip — invariant 2.
func TestNonScoringMetadata_JSON(t *testing.T) {
	tests := []struct {
		name   string
		tokens *TokenAccounting
		loopMs *int
		want   string
	}{
		{
			name:   "cache read reported as zero, write unreported",
			tokens: &TokenAccounting{InputTokens: 900, OutputTokens: 40, CacheReadTokens: intPtr(0)},
			loopMs: intPtr(5123),
			want:   `{"tokens":{"input_tokens":900,"output_tokens":40,"cache_read_tokens":0,"cache_write_tokens":null},"agent_loop_duration_ms":5123,"steps":null}`,
		},
		{
			name:   "both cache counts reported",
			tokens: &TokenAccounting{InputTokens: 1, OutputTokens: 2, CacheReadTokens: intPtr(700), CacheWriteTokens: intPtr(30)},
			want:   `{"tokens":{"input_tokens":1,"output_tokens":2,"cache_read_tokens":700,"cache_write_tokens":30},"agent_loop_duration_ms":null,"steps":null}`,
		},
		{
			name:   "duration only",
			loopMs: intPtr(0),
			want:   `{"tokens":null,"agent_loop_duration_ms":0,"steps":null}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewNonScoringMetadata(tt.tokens, tt.loopMs)
			got, err := json.Marshal(m)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("marshal = %s, want %s", got, tt.want)
			}
			var back NonScoringMetadata
			if err := json.Unmarshal(got, &back); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			again, err := json.Marshal(&back)
			if err != nil {
				t.Fatalf("re-marshal: %v", err)
			}
			if string(again) != tt.want {
				t.Errorf("round trip = %s, want %s", again, tt.want)
			}
		})
	}
}

// The constructor copies, so a caller mutating its inputs afterwards cannot
// reach into the record.
func TestNewNonScoringMetadata_Copies(t *testing.T) {
	read, loop := 5, 10
	m := NewNonScoringMetadata(&TokenAccounting{CacheReadTokens: &read}, &loop)
	read, loop = 99, 99
	got, _ := json.Marshal(m)
	want := `{"tokens":{"input_tokens":0,"output_tokens":0,"cache_read_tokens":5,"cache_write_tokens":null},"agent_loop_duration_ms":10,"steps":null}`
	if string(got) != want {
		t.Errorf("marshal = %s, want %s", got, want)
	}
}
