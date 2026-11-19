package evaluation

import (
	"encoding/json"
	"testing"
)

func sp(s string) *string { return &s }

func wholeTestDeclaration() AgentIdentityDeclaration {
	return AgentIdentityDeclaration{
		BinaryVersion: sp("v1"), BinarySHA: sp("abc"), ConfigHash: sp("sha256:1"), SystemPromptHash: sp("sha256:2"),
		DeclaredModel: sp("model-a"), DeclaredProvider: sp("provider-a"), WrapperTextStated: true,
	}
}

func unestablished(v VerdictAgentIdentity) map[string]bool {
	out := map[string]bool{}
	for _, f := range v.NotEstablished {
		out[f.Fact] = true
	}
	return out
}

// The heterogeneity flag is true on one difference, false only when every
// executed scenario was observed on the declared model, and null — named as
// not established — when it cannot be decided. It is never false by default.
func TestBuildVerdictAgentIdentity_Heterogeneity(t *testing.T) {
	observed := func(models ...*string) []ObservedModelRecord {
		var out []ObservedModelRecord
		for i, m := range models {
			out = append(out, ObservedModelRecord{ScenarioID: string(rune('a' + i)), ObservedModel: m})
		}
		return out
	}
	noModel := wholeTestDeclaration()
	noModel.DeclaredModel = nil

	tests := []struct {
		name       string
		decl       AgentIdentityDeclaration
		observed   []ObservedModelRecord
		want       *bool
		detail     int
		unobserved int
	}{
		{"every scenario on the declared model", wholeTestDeclaration(), observed(sp("model-a"), sp("model-a")), boolp(false), 0, 0},
		{"one scenario fell back", wholeTestDeclaration(), observed(sp("model-a"), sp("model-b")), boolp(true), 1, 0},
		{"a difference decides it whatever else is missing", wholeTestDeclaration(), observed(nil, sp("model-b")), boolp(true), 1, 1},
		{"a scenario with no observed model and no difference", wholeTestDeclaration(), observed(sp("model-a"), nil), nil, 0, 1},
		{"no declared model to compare with", noModel, observed(sp("model-a")), nil, 0, 0},
		{"no scenario executed", wholeTestDeclaration(), nil, boolp(false), 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildVerdictAgentIdentity(tt.decl, tt.observed)
			switch {
			case tt.want == nil && got.Heterogeneous != nil:
				t.Errorf("heterogeneous = %t, want null", *got.Heterogeneous)
			case tt.want != nil && (got.Heterogeneous == nil || *got.Heterogeneous != *tt.want):
				t.Errorf("heterogeneous = %v, want %t", got.Heterogeneous, *tt.want)
			}
			if (tt.want == nil) != unestablished(got)[IdentityHeterogeneous] {
				t.Errorf("not_established names heterogeneous: %t, want %t", unestablished(got)[IdentityHeterogeneous], tt.want == nil)
			}
			if len(got.HeterogeneityDetail) != tt.detail {
				t.Errorf("heterogeneity_detail has %d entries, want %d", len(got.HeterogeneityDetail), tt.detail)
			}
			if len(got.UnobservedScenarios) != tt.unobserved {
				t.Errorf("unobserved_scenarios has %d entries, want %d", len(got.UnobservedScenarios), tt.unobserved)
			}
		})
	}
}

func boolp(b bool) *bool { return &b }

// A fact the adapter did not report is null and named; nothing is filled in.
// A wrapper text the adapter stated as none is null and NOT named: it is a
// fact about the agent, where an unstated one is an absence.
func TestBuildVerdictAgentIdentity_AbsentFactsAreNamedNeverGuessed(t *testing.T) {
	whole := BuildVerdictAgentIdentity(wholeTestDeclaration(), nil)
	if !whole.Whole() {
		t.Errorf("a whole declaration reports not established: %v", whole.NotEstablished)
	}
	if whole.FixedWrapperText != nil {
		t.Errorf("fixed_wrapper_text = %q, want null for an adapter that applies none", *whole.FixedWrapperText)
	}

	empty := BuildVerdictAgentIdentity(AgentIdentityDeclaration{}, nil)
	named := unestablished(empty)
	for _, fact := range []string{
		IdentityBinaryVersion, IdentityBinarySHA, IdentityConfigHash, IdentitySystemPromptHash,
		IdentityDeclaredModel, IdentityDeclaredProvider, IdentityFixedWrapperText, IdentityHeterogeneous,
	} {
		if !named[fact] {
			t.Errorf("%s is not named in not_established", fact)
		}
	}

	// Every key of the format is on the wire, as an explicit null.
	raw, err := json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"binary_version", "binary_sha", "config_hash", "system_prompt_hash",
		"declared_model", "declared_provider", "fixed_wrapper_text", "heterogeneous",
	} {
		if string(keys[key]) != "null" {
			t.Errorf("%s = %s, want an explicit null", key, keys[key])
		}
	}

	disclosed := wholeTestDeclaration()
	disclosed.FixedWrapperText = sp("You are being evaluated.")
	if got := BuildVerdictAgentIdentity(disclosed, nil); got.FixedWrapperText == nil || *got.FixedWrapperText != "You are being evaluated." {
		t.Errorf("a disclosed wrapper text did not reach the verdict: %v", got.FixedWrapperText)
	}
}

// The wire value keeps an absent key, a null and a string apart.
func TestOptionalText(t *testing.T) {
	type body struct {
		Text OptionalText `json:"fixed_wrapper_text"`
	}
	tests := []struct {
		in     string
		stated bool
		text   *string
	}{
		{`{}`, false, nil},
		{`{"fixed_wrapper_text":null}`, true, nil},
		{`{"fixed_wrapper_text":"preamble"}`, true, sp("preamble")},
	}
	for _, tt := range tests {
		var b body
		if err := json.Unmarshal([]byte(tt.in), &b); err != nil {
			t.Fatalf("%s: %v", tt.in, err)
		}
		if b.Text.Stated != tt.stated {
			t.Errorf("%s: stated = %t, want %t", tt.in, b.Text.Stated, tt.stated)
		}
		if (b.Text.Text == nil) != (tt.text == nil) || (tt.text != nil && *b.Text.Text != *tt.text) {
			t.Errorf("%s: text = %v, want %v", tt.in, b.Text.Text, tt.text)
		}
	}
}
