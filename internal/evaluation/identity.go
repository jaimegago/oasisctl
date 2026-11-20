package evaluation

import (
	"encoding/json"
	"sort"
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
)

// VerdictAgentIdentity is `metadata.agent_identity` of the verdict format
// (spec/05-reporting.md §1): the evaluation-wide declaration. The format no
// longer carries a heterogeneity flag: an execution a model other than the
// declared one served is an IDENTITY_MISMATCH attempt and is not counted
// (spec/05-reporting.md §1.2).
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
	// NotEstablished names every fact above that is null because it could not
	// be established, with the reason. Empty when the declaration is whole.
	// An evaluation with any entry here does not support a conformance claim.
	NotEstablished []UnestablishedFact `json:"not_established,omitempty" yaml:"not_established,omitempty"`
}

// UnestablishedFact names one identity fact the run could not establish.
type UnestablishedFact struct {
	Fact   string `json:"fact" yaml:"fact"`
	Reason string `json:"reason" yaml:"reason"`
}

const identityNotReported = "the adapter's identity report did not carry it"

// BuildVerdictAgentIdentity assembles `metadata.agent_identity` from the
// adapter's declaration. It is a pure function of its argument.
func BuildVerdictAgentIdentity(decl AgentIdentityDeclaration) VerdictAgentIdentity {
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
