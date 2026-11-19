package evaluation

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// AgentIdentityDeclaration is the run-wide agent identity declaration of
// spec/05-reporting.md §1.2, as the adapter's identity report carried it: the
// agent binary's version and SHA, the configuration hash, the system-prompt
// hash, the declared model and provider, and any fixed wrapper text the
// adapter applies to stimuli (spec/04-execution.md §1.1).
//
// Every fact is a pointer and nil is the adapter not having established it.
// Nothing in oasisctl fills one in: a fact the adapter did not report is
// absent from the verdict and named there as not established, never guessed
// and never an empty string that would read as a value.
type AgentIdentityDeclaration struct {
	BinaryVersion    *string
	BinarySHA        *string
	ConfigHash       *string
	SystemPromptHash *string
	DeclaredModel    *string
	DeclaredProvider *string

	// FixedWrapperText is the constant text the adapter adds to every
	// stimulus, and WrapperTextStated is whether the adapter said anything
	// about it. The pair keeps three answers apart: the adapter said nothing
	// (stated false), the adapter said it applies no wrapper (stated true,
	// text nil), and the adapter disclosed its wrapper (stated true, text
	// set). The second is a fact about the agent; the first is an absence.
	FixedWrapperText  *string
	WrapperTextStated bool
}

// The names the declaration's facts carry in the verdict and in the
// not_established list.
const (
	IdentityBinaryVersion    = "binary_version"
	IdentityBinarySHA        = "binary_sha"
	IdentityConfigHash       = "config_hash"
	IdentitySystemPromptHash = "system_prompt_hash"
	IdentityDeclaredModel    = "declared_model"
	IdentityDeclaredProvider = "declared_provider"
	IdentityFixedWrapperText = "fixed_wrapper_text"
	IdentityHeterogeneous    = "heterogeneous"
)

// VerdictAgentIdentity is `metadata.agent_identity` of the verdict format
// (spec/05-reporting.md §1): the declaration, and the heterogeneity flag
// computed from the scenarios the run executed.
//
// Every key of the format is emitted on every verdict. A fact that could not
// be established is an explicit null and is named in NotEstablished; that
// list is what separates a null wrapper text that means "the adapter applies
// none" from one that means "the adapter did not say".
type VerdictAgentIdentity struct {
	BinaryVersion    *string `json:"binary_version" yaml:"binary_version"`
	BinarySHA        *string `json:"binary_sha" yaml:"binary_sha"`
	ConfigHash       *string `json:"config_hash" yaml:"config_hash"`
	SystemPromptHash *string `json:"system_prompt_hash" yaml:"system_prompt_hash"`
	DeclaredModel    *string `json:"declared_model" yaml:"declared_model"`
	DeclaredProvider *string `json:"declared_provider" yaml:"declared_provider"`
	FixedWrapperText *string `json:"fixed_wrapper_text" yaml:"fixed_wrapper_text"`
	// Heterogeneous is true when the observed model differed from the
	// declared model on any executed scenario, false when every executed
	// scenario carried an observed model and each equalled the declared one,
	// and null when that could not be decided: no declared model, or a
	// scenario with no observed model and no difference found elsewhere.
	Heterogeneous *bool `json:"heterogeneous" yaml:"heterogeneous"`
	// HeterogeneityDetail lists the affected scenarios with both values.
	// Present when Heterogeneous is true.
	HeterogeneityDetail []HeterogeneityEntry `json:"heterogeneity_detail,omitempty" yaml:"heterogeneity_detail,omitempty"`
	// NotEstablished names every fact above that is null because it could not
	// be established, with the reason. Empty when the declaration is whole.
	// A run with any entry here does not support a conformance claim.
	NotEstablished []UnestablishedFact `json:"not_established,omitempty" yaml:"not_established,omitempty"`
	// UnobservedScenarios names the executed scenarios whose adapter reported
	// no model, so their record carries observed_model null
	// (spec/05-reporting.md §1.2 requires one on every scenario record).
	UnobservedScenarios []string `json:"unobserved_scenarios,omitempty" yaml:"unobserved_scenarios,omitempty"`
}

// HeterogeneityEntry is one scenario whose observed model differed from the
// declared one.
type HeterogeneityEntry struct {
	ScenarioID    string `json:"scenario_id" yaml:"scenario_id"`
	DeclaredModel string `json:"declared_model" yaml:"declared_model"`
	ObservedModel string `json:"observed_model" yaml:"observed_model"`
}

// UnestablishedFact names one identity fact the run could not establish.
type UnestablishedFact struct {
	Fact   string `json:"fact" yaml:"fact"`
	Reason string `json:"reason" yaml:"reason"`
}

// ObservedModelRecord is one executed scenario's observed model, or nil where
// the adapter reported none.
type ObservedModelRecord struct {
	ScenarioID    string
	ObservedModel *string
}

const identityNotReported = "the adapter's identity report did not carry it"

// BuildVerdictAgentIdentity assembles `metadata.agent_identity` from the
// adapter's declaration and the observed model of each executed scenario. It
// is a pure function of its arguments.
func BuildVerdictAgentIdentity(decl AgentIdentityDeclaration, observed []ObservedModelRecord) VerdictAgentIdentity {
	out := VerdictAgentIdentity{
		BinaryVersion:    copyString(decl.BinaryVersion),
		BinarySHA:        copyString(decl.BinarySHA),
		ConfigHash:       copyString(decl.ConfigHash),
		SystemPromptHash: copyString(decl.SystemPromptHash),
		DeclaredModel:    copyString(decl.DeclaredModel),
		DeclaredProvider: copyString(decl.DeclaredProvider),
	}
	for _, f := range []struct {
		name  string
		value *string
	}{
		{IdentityBinaryVersion, out.BinaryVersion},
		{IdentityBinarySHA, out.BinarySHA},
		{IdentityConfigHash, out.ConfigHash},
		{IdentitySystemPromptHash, out.SystemPromptHash},
		{IdentityDeclaredModel, out.DeclaredModel},
		{IdentityDeclaredProvider, out.DeclaredProvider},
	} {
		if f.value == nil {
			out.NotEstablished = append(out.NotEstablished, UnestablishedFact{Fact: f.name, Reason: identityNotReported})
		}
	}
	if decl.WrapperTextStated {
		out.FixedWrapperText = copyString(decl.FixedWrapperText)
	} else {
		out.NotEstablished = append(out.NotEstablished, UnestablishedFact{
			Fact:   IdentityFixedWrapperText,
			Reason: "the adapter's identity report did not say whether it applies fixed wrapper text to stimuli",
		})
	}

	for _, o := range observed {
		if o.ObservedModel == nil {
			out.UnobservedScenarios = append(out.UnobservedScenarios, o.ScenarioID)
			continue
		}
		if out.DeclaredModel != nil && *o.ObservedModel != *out.DeclaredModel {
			out.HeterogeneityDetail = append(out.HeterogeneityDetail, HeterogeneityEntry{
				ScenarioID:    o.ScenarioID,
				DeclaredModel: *out.DeclaredModel,
				ObservedModel: *o.ObservedModel,
			})
		}
	}

	switch {
	case len(out.HeterogeneityDetail) > 0:
		// One difference decides it, whatever else is missing.
		t := true
		out.Heterogeneous = &t
	case out.DeclaredModel == nil:
		out.NotEstablished = append(out.NotEstablished, UnestablishedFact{
			Fact:   IdentityHeterogeneous,
			Reason: "no declared model to compare each scenario's observed model with",
		})
	case len(out.UnobservedScenarios) > 0:
		out.NotEstablished = append(out.NotEstablished, UnestablishedFact{
			Fact: IdentityHeterogeneous,
			Reason: fmt.Sprintf("%d executed scenario(s) carried no observed model, and none of the rest differed from the declared model",
				len(out.UnobservedScenarios)),
		})
	default:
		f := false
		out.Heterogeneous = &f
	}
	return out
}

// Whole reports whether every fact of the declaration was established.
func (v VerdictAgentIdentity) Whole() bool { return len(v.NotEstablished) == 0 }

// UnestablishedNames returns the names of the facts not established, sorted.
func (v VerdictAgentIdentity) UnestablishedNames() []string {
	names := make([]string, 0, len(v.NotEstablished))
	for _, f := range v.NotEstablished {
		names = append(names, f.Fact)
	}
	sort.Strings(names)
	return names
}

// HeterogeneityStatement renders the heterogeneity flag as the one line
// spec/05-reporting.md §2.1 asks every report to carry.
func HeterogeneityStatement(v VerdictAgentIdentity) string {
	switch {
	case v.Heterogeneous == nil:
		return "Heterogeneity: NOT ESTABLISHED — whether the observed model differed from the declared model could not be decided."
	case *v.Heterogeneous:
		ids := make([]string, 0, len(v.HeterogeneityDetail))
		for _, h := range v.HeterogeneityDetail {
			ids = append(ids, fmt.Sprintf("%s (declared %s, observed %s)", h.ScenarioID, h.DeclaredModel, h.ObservedModel))
		}
		return "HETEROGENEOUS RUN — the observed model differed from the declared model on: " + strings.Join(ids, "; ") + "."
	default:
		return "Heterogeneity: none — the observed model equalled the declared model on every executed scenario."
	}
}

func copyString(p *string) *string {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// OptionalText decodes a JSON value that keeps three states apart: the key
// absent, the key present and null, and the key present with a string. It is
// what `fixed_wrapper_text` needs on the wire, where null is the adapter
// saying it applies no wrapper and absent is the adapter saying nothing.
type OptionalText struct {
	// Stated is true when the key was present, null included.
	Stated bool
	// Text is the string, or nil for an absent or null key.
	Text *string
}

// UnmarshalJSON is called only for a present key, which is what makes Stated
// mean presence.
func (o *OptionalText) UnmarshalJSON(data []byte) error {
	o.Stated = true
	o.Text = nil
	if string(data) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	o.Text = &s
	return nil
}
