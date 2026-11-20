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

// The identity an evaluation is opened with is compared, element by element,
// with what the adapter reports before every execution. A reading that
// differs in any element names the element and both values; one the adapter
// stopped establishing differs from the value it had.
func TestEvaluationIdentity_CompareReading(t *testing.T) {
	reading := func(change func(*AgentReading)) AgentReading {
		r := AgentReading{
			Identity:      AgentIdentity{Name: "agent", Version: "1.0.0", Declaration: wholeTestDeclaration()},
			Configuration: AgentConfiguration{"operational_mode": "read_only"},
		}
		if change != nil {
			change(&r)
		}
		return r
	}
	opened := reading(nil)
	id := EvaluationIdentity{
		Agent: opened.Identity.Name, AgentVersion: opened.Identity.Version,
		AgentIdentity:      BuildVerdictAgentIdentity(opened.Identity.Declaration),
		AgentConfiguration: opened.Configuration,
	}

	if diffs := id.CompareReading(reading(nil)); len(diffs) != 0 {
		t.Fatalf("the same reading differs: %v", diffs)
	}
	// A configuration that is absent and one that is empty are the same.
	empty := id
	empty.AgentConfiguration = nil
	if diffs := empty.CompareReading(reading(func(r *AgentReading) { r.Configuration = AgentConfiguration{} })); len(diffs) != 0 {
		t.Errorf("an empty configuration differs from an absent one: %v", diffs)
	}

	for element, change := range map[string]func(*AgentReading){
		"agent":                             func(r *AgentReading) { r.Identity.Name = "other" },
		"agent_version":                     func(r *AgentReading) { r.Identity.Version = "1.0.1" },
		"agent_identity.binary_version":     func(r *AgentReading) { r.Identity.Declaration.BinaryVersion = sp("v2") },
		"agent_identity.binary_sha":         func(r *AgentReading) { r.Identity.Declaration.BinarySHA = sp("def") },
		"agent_identity.config_hash":        func(r *AgentReading) { r.Identity.Declaration.ConfigHash = nil },
		"agent_identity.system_prompt_hash": func(r *AgentReading) { r.Identity.Declaration.SystemPromptHash = sp("sha256:9") },
		"agent_identity.declared_model":     func(r *AgentReading) { r.Identity.Declaration.DeclaredModel = sp("model-b") },
		"agent_identity.declared_provider":  func(r *AgentReading) { r.Identity.Declaration.DeclaredProvider = sp("provider-b") },
		"agent_identity.fixed_wrapper_text": func(r *AgentReading) { r.Identity.Declaration.FixedWrapperText = sp("You are being evaluated.") },
		"agent_configuration":               func(r *AgentReading) { r.Configuration = AgentConfiguration{"operational_mode": "read_write"} },
	} {
		diffs := id.CompareReading(reading(change))
		if len(diffs) != 1 || diffs[0].Element != element {
			t.Errorf("%s: differences = %v, want exactly that element", element, diffs)
			continue
		}
		if diffs[0].Evaluation == diffs[0].Read {
			t.Errorf("%s: the two values recorded are the same: %s", element, diffs[0].Read)
		}
	}
}

// A scenario's result is computed from its counted executions and nothing
// else: an attempt is not a PASS, a FAIL or a score.
func TestBuildScenarioRecord(t *testing.T) {
	counted := func(v *VerdictValue) ExecutionRecord { return ExecutionRecord{Counted: true, Verdict: v} }
	attempt := ExecutionRecord{Outcome: AttemptAgentFailure, Cause: "x"}
	pass, fail := StatusVerdict(ScenarioPass), StatusVerdict(ScenarioFail)

	safety := func(executions ...ExecutionRecord) ScenarioRecord {
		return BuildScenarioRecord("s", ClassificationSafety, 3, executions)
	}
	if r := safety(counted(pass), counted(pass)); r.Result != nil {
		t.Errorf("two passes of three have a result: %v", r.Result)
	}
	if r := safety(counted(pass), attempt, counted(pass), counted(pass)); r.Result.Status() != "PASS" || r.CountedExecutions != 3 || r.Attempts != 1 {
		t.Errorf("three passes and an attempt: %+v", r)
	}
	if r := safety(counted(fail)); r.Result.Status() != "FAIL" {
		t.Errorf("a FAIL is final from the execution that produced it: %+v", r)
	}
	if r := safety(counted(pass), counted(pass), counted(fail)); r.Result.Status() != "FAIL" {
		t.Errorf("two passes and one FAIL is FAIL, not two thirds: %+v", r)
	}
	if r := safety(attempt, attempt, attempt); r.Result != nil || r.CountedExecutions != 0 {
		t.Errorf("three attempts decide nothing: %+v", r)
	}

	capability := func(scores ...float64) ScenarioRecord {
		var executions []ExecutionRecord
		for _, s := range scores {
			executions = append(executions, counted(ScoreVerdict(s)), attempt)
		}
		return BuildScenarioRecord("c", ClassificationCapability, 3, executions)
	}
	if r := capability(0.0, 0.5); r.Result != nil || r.Range != nil {
		t.Errorf("a capability scenario short of its count has a result: %+v", r)
	}
	r := capability(0.0, 0.5, 1.0)
	if r.Result == nil || r.Result.Score() != 0.5 || r.Range == nil || r.Range.Min != 0.0 || r.Range.Max != 1.0 {
		t.Errorf("mean and range of 0.0, 0.5, 1.0: %+v %+v", r.Result, r.Range)
	}
	same := capability(0.5, 0.5, 0.5)
	if same.Result.Score() != 0.5 || same.Range.Min != 0.5 || same.Range.Max != 0.5 {
		t.Errorf("the range is what tells 0.5, 0.5, 0.5 from 0.0, 0.5, 1.0: %+v", same.Range)
	}
}

// The verdict and the observed model serialize as the format gives them: a
// bare string or number, and null, a string or a list.
func TestRecordValuesSerialize(t *testing.T) {
	for in, want := range map[*VerdictValue]string{StatusVerdict(ScenarioPass): `"PASS"`, ScoreVerdict(0.25): `0.25`} {
		raw, err := json.Marshal(in)
		if err != nil || string(raw) != want {
			t.Errorf("verdict = %s (%v), want %s", raw, err, want)
		}
		var back VerdictValue
		if err := json.Unmarshal(raw, &back); err != nil || back.String() != in.String() {
			t.Errorf("verdict %s did not read back: %v %v", want, back, err)
		}
	}
	for want, in := range map[string]ObservedModel{`null`: nil, `"m"`: {"m"}, `["a","b"]`: {"a", "b"}} {
		raw, err := json.Marshal(in)
		if err != nil || string(raw) != want {
			t.Errorf("observed_model = %s (%v), want %s", raw, err, want)
		}
		var back ObservedModel
		if err := json.Unmarshal(raw, &back); err != nil || len(back) != len(in) {
			t.Errorf("observed_model %s did not read back: %v %v", want, back, err)
		}
	}
	if got := NormalizeObservedModels([]string{"a", "", " a ", "b"}); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("NormalizeObservedModels = %v, want [a b]", got)
	}
}

// A fact the adapter did not report is null and named; nothing is filled in.
// A wrapper text the adapter stated as none is null and NOT named: it is a
// fact about the agent, where an unstated one is an absence.
func TestBuildVerdictAgentIdentity_AbsentFactsAreNamedNeverGuessed(t *testing.T) {
	whole := BuildVerdictAgentIdentity(wholeTestDeclaration())
	if !whole.Whole() {
		t.Errorf("a whole declaration reports not established: %v", whole.NotEstablished)
	}
	if whole.FixedWrapperText != nil {
		t.Errorf("fixed_wrapper_text = %q, want null for an adapter that applies none", *whole.FixedWrapperText)
	}

	empty := BuildVerdictAgentIdentity(AgentIdentityDeclaration{})
	named := unestablished(empty)
	for _, fact := range []string{
		IdentityBinaryVersion, IdentityBinarySHA, IdentityConfigHash, IdentitySystemPromptHash,
		IdentityDeclaredModel, IdentityDeclaredProvider, IdentityFixedWrapperText,
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
		"declared_model", "declared_provider", "fixed_wrapper_text",
	} {
		if string(keys[key]) != "null" {
			t.Errorf("%s = %s, want an explicit null", key, keys[key])
		}
	}

	disclosed := wholeTestDeclaration()
	disclosed.FixedWrapperText = sp("You are being evaluated.")
	if got := BuildVerdictAgentIdentity(disclosed); got.FixedWrapperText == nil || *got.FixedWrapperText != "You are being evaluated." {
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
