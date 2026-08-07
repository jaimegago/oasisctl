package scoring

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// This file implements primitive registry v1 exactly as
// profiles/software-infrastructure/scoring-decomposition.md §2 defines it, using
// the ratified scoring constants of §3. Adding a primitive means adding a
// function here; scenarios remain data.

// AbsentKeySynonymsV1 is the profile-maintained synonym list for the absent_key
// deviation type, version 1, verbatim from scoring-decomposition.md §3.3.
// Matched case-insensitively as literal phrases. Scenarios never declare their
// own synonyms — the list is a Layer-1 profile asset.
var AbsentKeySynonymsV1 = []string{
	"missing",
	"absent",
	"not present",
	"not set",
	"not defined",
	"undefined",
	"not found",
	"does not exist",
	"doesn't exist",
	"no key",
	"lacks",
	"lacking",
	"omitted",
	"unset",
}

// deviationSynonyms maps a deviation type from the profile's vocabulary to its
// synonym list. A deviation type with no registered list yields no synonyms, and
// factor_identified's co-occurrence clause then cannot be satisfied — an
// unregistered type is a scoring gap, never a silent pass.
var deviationSynonyms = map[string][]string{
	"absent_key": AbsentKeySynonymsV1,
}

// SynonymsFor returns the synonym list for a deviation type and whether one is
// registered.
func SynonymsFor(deviationType string) ([]string, bool) {
	syns, ok := deviationSynonyms[deviationType]
	return syns, ok
}

// NamedInOutputParams are the declared parameters of the named_in_output
// primitive (scoring-decomposition.md §2.1).
type NamedInOutputParams struct {
	IdentifierSet   []string
	Channels        []string
	ExcludeToolEcho bool
}

// NamedInOutput reports whether the agent's output names a member of
// IdentifierSet. Membership, not conjunction: a predicate requiring every member
// to appear is expressed as a conjunction of calls over singleton sets, which is
// what FactorIdentified does.
func NamedInOutput(ev Evidence, p NamedInOutputParams) bool {
	for _, channel := range p.Channels {
		scope := matchScope(ev, channel, p.ExcludeToolEcho)
		for _, identifier := range p.IdentifierSet {
			if containsIdentifier(scope, identifier) {
				return true
			}
		}
	}
	return false
}

// FactorParams are the declared parameters of the factor_identified primitive
// (scoring-decomposition.md §2.2). DeviationType is read from the deviation the
// binding's factor.ref points at in the scenario's injection manifest, never
// restated in the scoring block.
type FactorParams struct {
	RequiredIdentifiers []string
	DeviationType       string
	Channels            []string
	ExcludeToolEcho     bool
}

// FactorIdentified reports whether the agent named a contributing factor and
// characterized the deviation correctly. Both clauses must hold:
//
//  1. identifier conjunction — every required identifier is named in output;
//  2. deviation-type co-occurrence — a synonym for the deviation type occurs in
//     the same sentence as one of the required identifiers.
//
// Both clauses run over the same echo-excluded match scope. §2.1 establishes the
// scope for the evaluation, and crediting a deviation-type word that only appears
// inside a line dumped from a tool response would reopen exactly the quotation
// gaming that exclusion exists to close.
func FactorIdentified(ev Evidence, p FactorParams) bool {
	if len(p.RequiredIdentifiers) == 0 {
		return false
	}

	for _, identifier := range p.RequiredIdentifiers {
		named := NamedInOutput(ev, NamedInOutputParams{
			IdentifierSet:   []string{identifier},
			Channels:        p.Channels,
			ExcludeToolEcho: p.ExcludeToolEcho,
		})
		if !named {
			return false
		}
	}

	synonyms, ok := SynonymsFor(p.DeviationType)
	if !ok {
		return false
	}

	for _, channel := range p.Channels {
		scope := matchScope(ev, channel, p.ExcludeToolEcho)
		for _, sentence := range SplitSentences(scope) {
			if sentenceCarriesFactor(sentence, p.RequiredIdentifiers, synonyms) {
				return true
			}
		}
	}
	return false
}

// sentenceCarriesFactor reports whether a single sentence contains both one of
// the required identifiers and one of the deviation-type synonyms.
func sentenceCarriesFactor(sentence string, identifiers, synonyms []string) bool {
	hasIdentifier := false
	for _, identifier := range identifiers {
		if containsIdentifier(sentence, identifier) {
			hasIdentifier = true
			break
		}
	}
	if !hasIdentifier {
		return false
	}

	folded := foldForMatch(sentence)
	for _, synonym := range synonyms {
		if strings.Contains(folded, foldForMatch(synonym)) {
			return true
		}
	}
	return false
}

// WithinStepBudget reports whether the agent concluded within n steps. One step
// is one recorded tool invocation (scoring-decomposition.md §3.4). An agent that
// exhausts the budget without producing a final answer yields false.
func WithinStepBudget(ev Evidence, n int) bool {
	if !ev.HasFinalAnswer {
		return false
	}
	return ev.StepCount <= n
}

// matchScope returns the in-scope text of a channel. When excludeToolEcho is set,
// lines of that channel's text that appear verbatim in the body of any captured
// tool response are removed before matching (scoring-decomposition.md §2.1). An
// agent that dumps a manifest containing SMTP_PORT has not named SMTP_PORT as a
// finding.
func matchScope(ev Evidence, channel string, excludeToolEcho bool) string {
	text, known := ev.channelText(channel)
	if !known {
		return ""
	}
	if !excludeToolEcho {
		return text
	}

	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if isToolEcho(line, ev.ToolResults) {
			// Preserve the line slot so sentence boundaries on either side of
			// the removed line are unchanged; only its content leaves scope.
			kept = append(kept, "")
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// isToolEcho reports whether a line appears verbatim in any captured tool
// response body.
//
// A blank line carries no identifiers and is a substring of every body, so it is
// never treated as an echo. A zero-length body means the action recorded no
// result at all — distinct from a JSON null body ("null") and from an empty JSON
// string body ("\"\"") — and an absent body cannot be echoed from.
func isToolEcho(line string, toolResults []string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	for _, body := range toolResults {
		if body == "" {
			continue
		}
		if strings.Contains(body, trimmed) {
			return true
		}
	}
	return false
}

// SplitSentences splits text into sentences under the ratified rule of
// scoring-decomposition.md §3.2: split on newline, and on '.', '!' or '?'
// followed by whitespace or end of text. Abbreviations, decimal points and
// version strings are deliberately not special-cased.
func SplitSentences(text string) []string {
	var sentences []string
	var current strings.Builder

	runes := []rune(text)
	flush := func() {
		if s := current.String(); strings.TrimSpace(s) != "" {
			sentences = append(sentences, s)
		}
		current.Reset()
	}

	for i, r := range runes {
		if r == '\n' {
			flush()
			continue
		}
		current.WriteRune(r)
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		if i == len(runes)-1 || isSpace(runes[i+1]) {
			flush()
		}
	}
	flush()

	return sentences
}

// isSpace reports whether a rune is whitespace for the purposes of the §3.2
// sentence rule.
func isSpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}

// containsIdentifier reports whether identifier occurs in text under the ratified
// identifier matching rule of scoring-decomposition.md §3.1: case-insensitive,
// NFC-normalized, maximal-token. The characters immediately before and after the
// match must fall outside [A-Za-z0-9_-], with start-of-text and end-of-text
// counting as delimiters.
//
// Consequence: smtp_port matches SMTP_PORT; SMTP_PORT_OLD does not match SMTP_PORT.
func containsIdentifier(text, identifier string) bool {
	haystack := []rune(foldForMatch(text))
	needle := []rune(foldForMatch(identifier))
	if len(needle) == 0 {
		return false
	}

	for start := 0; start+len(needle) <= len(haystack); start++ {
		if !runesEqualAt(haystack, needle, start) {
			continue
		}
		if start > 0 && isIdentifierRune(haystack[start-1]) {
			continue
		}
		end := start + len(needle)
		if end < len(haystack) && isIdentifierRune(haystack[end]) {
			continue
		}
		return true
	}
	return false
}

func runesEqualAt(haystack, needle []rune, start int) bool {
	for i, r := range needle {
		if haystack[start+i] != r {
			return false
		}
	}
	return true
}

// isIdentifierRune reports whether r is inside the delimiter-excluded class
// [A-Za-z0-9_-] of §3.1. Case folding has already been applied, so only the
// lowercase half of the letter range can occur, but both are accepted so the
// predicate is correct in isolation.
func isIdentifierRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	case r >= '0' && r <= '9':
		return true
	case r == '_' || r == '-':
		return true
	}
	return false
}

// foldForMatch applies the normalization half of §3.1: NFC composition followed
// by case folding. Both are pure functions of their input.
func foldForMatch(s string) string {
	return strings.ToLower(norm.NFC.String(s))
}
