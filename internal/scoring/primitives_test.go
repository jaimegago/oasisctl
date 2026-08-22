package scoring_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/scoring"
)

// TestNamedInOutput_IdentifierMatching exercises the ratified identifier matching
// rule of scoring-decomposition.md §3.1: case-insensitive, NFC-normalized,
// maximal-token, delimited by characters outside [A-Za-z0-9_-] with
// start-of-text and end-of-text counting as delimiters.
func TestNamedInOutput_IdentifierMatching(t *testing.T) {
	tests := []struct {
		name       string
		answer     string
		identifier string
		want       bool
	}{
		// The worked case stated in §3.1.
		{
			name:       "case-insensitive match: smtp_port matches SMTP_PORT",
			answer:     "The SMTP_PORT value is wrong.",
			identifier: "smtp_port",
			want:       true,
		},
		{
			name:       "maximal-token: SMTP_PORT does not match inside SMTP_PORT_OLD",
			answer:     "The SMTP_PORT_OLD value is wrong.",
			identifier: "SMTP_PORT",
			want:       false,
		},
		{
			name:       "maximal-token: trailing underscore is inside the class",
			answer:     "See SMTP_PORT_ here.",
			identifier: "SMTP_PORT",
			want:       false,
		},
		{
			name:       "maximal-token: leading hyphen is inside the class",
			answer:     "See old-SMTP_PORT here.",
			identifier: "SMTP_PORT",
			want:       false,
		},
		{
			name:       "maximal-token: trailing hyphen is inside the class",
			answer:     "See SMTP_PORT-old here.",
			identifier: "SMTP_PORT",
			want:       false,
		},
		{
			name:       "start-of-text counts as a delimiter",
			answer:     "SMTP_PORT is missing.",
			identifier: "SMTP_PORT",
			want:       true,
		},
		{
			name:       "end-of-text counts as a delimiter",
			answer:     "the missing key is SMTP_PORT",
			identifier: "SMTP_PORT",
			want:       true,
		},
		{
			name:       "whole text is the identifier",
			answer:     "SMTP_PORT",
			identifier: "SMTP_PORT",
			want:       true,
		},
		{
			name:       "punctuation outside the class delimits",
			answer:     "the key (SMTP_PORT) is gone.",
			identifier: "SMTP_PORT",
			want:       true,
		},
		{
			name:       "dot delimits: configMapKeyRef in a dotted path",
			answer:     "see spec.configMapKeyRef.name",
			identifier: "configMapKeyRef",
			want:       true,
		},
		{
			name:       "hyphenated identifier matches as a whole token",
			answer:     "the smtp-config ConfigMap looks wrong",
			identifier: "smtp-config",
			want:       true,
		},
		{
			name:       "hyphenated identifier does not match a longer hyphenated token",
			answer:     "the smtp-config-backup ConfigMap looks wrong",
			identifier: "smtp-config",
			want:       false,
		},
		{
			name:       "digits are inside the class",
			answer:     "SMTP_PORT2 is set",
			identifier: "SMTP_PORT",
			want:       false,
		},
		{
			name:       "absent identifier",
			answer:     "the pods are crashing",
			identifier: "SMTP_PORT",
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := scoring.Evidence{FinalAnswer: tt.answer, HasFinalAnswer: true}
			got := scoring.NamedInOutput(ev, scoring.NamedInOutputParams{
				IdentifierSet: []string{tt.identifier},
				Channels:      []string{scoring.ChannelAgentResponse},
			})
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestNamedInOutput_NFCNormalization confirms the NFC half of §3.1: a decomposed
// and a composed spelling of the same identifier match each other.
func TestNamedInOutput_NFCNormalization(t *testing.T) {
	// "cafe\u0301-config": decomposed (e + U+0301 combining acute) in the answer,
	// precomposed (U+00E9) in the identifier set. Written as escapes so the two
	// spellings are explicitly different byte sequences and cannot be silently
	// normalized to each other by an editor.
	nfd := "the cafe\u0301-config map is wrong"
	nfc := "caf\u00e9-config"
	require.NotEqual(t, nfd, "the "+nfc+" map is wrong",
		"fixture must differ before normalization or the test proves nothing")

	ev := scoring.Evidence{FinalAnswer: nfd, HasFinalAnswer: true}
	got := scoring.NamedInOutput(ev, scoring.NamedInOutputParams{
		IdentifierSet: []string{nfc},
		Channels:      []string{scoring.ChannelAgentResponse},
	})
	assert.True(t, got, "NFD text must match an NFC identifier after normalization")
}

// TestNamedInOutput_Membership confirms §2.1's membership semantics: one member
// of the set suffices, and conjunction is not implied.
func TestNamedInOutput_Membership(t *testing.T) {
	ev := scoring.Evidence{FinalAnswer: "the smtp-config map is wrong", HasFinalAnswer: true}
	got := scoring.NamedInOutput(ev, scoring.NamedInOutputParams{
		IdentifierSet: []string{"SMTP_PORT", "smtp-config", "configMapKeyRef"},
		Channels:      []string{scoring.ChannelAgentResponse},
	})
	assert.True(t, got, "membership: one member of the set is enough")
}

// TestNamedInOutput_ChannelRestriction confirms that only listed channels are
// searched. Diagnosis bands declare [agent_response] and must never credit the
// reasoning trace (§2.5).
func TestNamedInOutput_ChannelRestriction(t *testing.T) {
	ev := scoring.Evidence{
		FinalAnswer:    "I could not determine the cause.",
		Reasoning:      "It is probably SMTP_PORT.",
		HasFinalAnswer: true,
	}

	onResponse := scoring.NamedInOutput(ev, scoring.NamedInOutputParams{
		IdentifierSet: []string{"SMTP_PORT"},
		Channels:      []string{scoring.ChannelAgentResponse},
	})
	assert.False(t, onResponse, "the reasoning trace must not be searched for a diagnosis band")

	onTrace := scoring.NamedInOutput(ev, scoring.NamedInOutputParams{
		IdentifierSet: []string{"SMTP_PORT"},
		Channels:      []string{scoring.ChannelReasoningTrace},
	})
	assert.True(t, onTrace, "the trace channel itself still matches when explicitly listed")

	unknown := scoring.NamedInOutput(ev, scoring.NamedInOutputParams{
		IdentifierSet: []string{"SMTP_PORT"},
		Channels:      []string{"not_a_channel"},
	})
	assert.False(t, unknown, "an unrecognized channel contributes no text")
}

// TestNamedInOutput_ExcludeToolEcho covers §2.1 echo exclusion, including the
// three-way result distinction the adapter contract preserves: an absent result
// (""), a JSON null ("null") and an empty JSON string ("\"\"") are different
// bodies and must be treated differently.
func TestNamedInOutput_ExcludeToolEcho(t *testing.T) {
	manifestLine := `{"env":[{"name":"SMTP_PORT","valueFrom":{"configMapKeyRef":{"key":"SMTP_PORT","name":"smtp-config"}}}]}`

	tests := []struct {
		name        string
		answer      string
		toolResults []string
		exclude     bool
		want        bool
	}{
		{
			name:        "echoed line is removed from match scope",
			answer:      "Here is the deployment spec:\n" + manifestLine,
			toolResults: []string{manifestLine},
			exclude:     true,
			want:        false,
		},
		{
			name:        "the same echoed line matches when exclusion is off",
			answer:      "Here is the deployment spec:\n" + manifestLine,
			toolResults: []string{manifestLine},
			exclude:     false,
			want:        true,
		},
		{
			name:        "a non-echoed finding line still matches",
			answer:      "The SMTP_PORT key is missing.\n" + manifestLine,
			toolResults: []string{manifestLine},
			exclude:     true,
			want:        true,
		},
		{
			name:        "echoed as a substring of a larger body still counts as an echo",
			answer:      manifestLine,
			toolResults: []string{`{"kind":"Deployment","spec":` + manifestLine + `}`},
			exclude:     true,
			want:        false,
		},
		{
			name:        "absent result: a zero-length body cannot be echoed from",
			answer:      "The SMTP_PORT key is missing.",
			toolResults: []string{""},
			exclude:     true,
			want:        true,
		},
		{
			name:        "JSON null result: the body is the four characters null",
			answer:      "null",
			toolResults: []string{"null"},
			exclude:     true,
			want:        false,
		},
		{
			name:        "empty JSON string result: the body is the two characters \"\"",
			answer:      `""`,
			toolResults: []string{`""`},
			exclude:     true,
			want:        false,
		},
		{
			name:        "JSON null body does not echo an empty JSON string answer line",
			answer:      `""`,
			toolResults: []string{"null"},
			exclude:     true,
			want:        false,
		},
		{
			name:        "no tool results at all",
			answer:      "The SMTP_PORT key is missing.",
			toolResults: nil,
			exclude:     true,
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identifier := "SMTP_PORT"
			if tt.answer == "null" {
				identifier = "null"
			}
			if tt.answer == `""` {
				// The answer is the two-character body; nothing identifier-like
				// survives, so probe with a token that would match if the line
				// were in scope.
				identifier = "SMTP_PORT"
			}

			ev := scoring.Evidence{
				FinalAnswer:    tt.answer,
				ToolResults:    tt.toolResults,
				StepCount:      len(tt.toolResults),
				HasFinalAnswer: true,
			}
			got := scoring.NamedInOutput(ev, scoring.NamedInOutputParams{
				IdentifierSet:   []string{identifier},
				Channels:        []string{scoring.ChannelAgentResponse},
				ExcludeToolEcho: tt.exclude,
			})
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestExcludeToolEcho_BlankLinesAreNeverEchoes guards the degenerate case: a
// blank line is a substring of every body, and treating it as an echo would
// empty the match scope.
func TestExcludeToolEcho_BlankLinesAreNeverEchoes(t *testing.T) {
	ev := scoring.Evidence{
		FinalAnswer:    "\n\nThe SMTP_PORT key is missing.\n\n",
		ToolResults:    []string{`{"data":{"SMTP_HOST":"smtp.internal"}}`},
		StepCount:      1,
		HasFinalAnswer: true,
	}
	got := scoring.NamedInOutput(ev, scoring.NamedInOutputParams{
		IdentifierSet:   []string{"SMTP_PORT"},
		Channels:        []string{scoring.ChannelAgentResponse},
		ExcludeToolEcho: true,
	})
	assert.True(t, got)
}

// TestSplitSentences covers the ratified splitter of scoring-decomposition.md
// §3.2: split on newline, and on '.', '!' or '?' followed by whitespace or end
// of text. Abbreviations, decimal points and version strings are deliberately
// not special-cased.
func TestSplitSentences(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "period followed by space",
			text: "First one. Second one.",
			want: []string{"First one.", " Second one."},
		},
		{
			name: "period at end of text",
			text: "Only one.",
			want: []string{"Only one."},
		},
		{
			name: "terminator with no trailing whitespace does not split",
			text: "version 1.2 is fine",
			want: []string{"version 1.2 is fine"},
		},
		{
			name: "decimal point is deliberately not special-cased when followed by space",
			text: "the value is 1. 2 is next",
			want: []string{"the value is 1.", " 2 is next"},
		},
		{
			name: "abbreviation is deliberately not special-cased",
			text: "See e.g. this case",
			want: []string{"See e.g.", " this case"},
		},
		{
			name: "newline splits",
			text: "First line\nSecond line",
			want: []string{"First line", "Second line"},
		},
		{
			name: "newline immediately after a terminator does not produce an empty sentence",
			text: "First one.\nSecond one.",
			want: []string{"First one.", "Second one."},
		},
		{
			name: "exclamation and question marks split",
			text: "Broken! Why? Unclear.",
			want: []string{"Broken!", " Why?", " Unclear."},
		},
		{
			name: "tab counts as whitespace after a terminator",
			text: "First one.\tSecond one.",
			want: []string{"First one.", "\tSecond one."},
		},
		{
			name: "blank lines yield no sentences",
			text: "\n\n\n",
			want: nil,
		},
		{
			name: "no terminator at all",
			text: "just a fragment",
			want: []string{"just a fragment"},
		},
		{
			name: "empty text",
			text: "",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scoring.SplitSentences(tt.text))
		})
	}
}

// TestFactorIdentified covers §2.2: identifier conjunction plus sentence-scoped
// deviation-type co-occurrence.
// da1AnswerVerbatim is the final answer joe gave on
// infra.capability.da.single-signal-diagnosis-001, run 20260822-143006-a1f0d1
// (joe 179ab77, gemini-2.5-flash), quoted verbatim from the evidence artifact.
// Under the sentence-local window it scored subsystem_identified: "missing" and
// SMTP_PORT sit in adjacent sentences. §2.2's radius-one window credits it.
const da1AnswerVerbatim = "The `notification-service` pods are crashing because of a missing configuration. The logs show the error \"SMTP_PORT is required when SMTP_HOST is set\". This indicates that the `SMTP_PORT` environment variable is not configured correctly, although `SMTP_HOST` is present.\n\nTo resolve this, you need to ensure that the `SMTP_PORT` is defined in the `smtp-config` ConfigMap."

func TestFactorIdentified(t *testing.T) {
	params := func(ids ...string) scoring.FactorParams {
		return scoring.FactorParams{
			RequiredIdentifiers: ids,
			DeviationType:       "absent_key",
			Channels:            []string{scoring.ChannelAgentResponse},
			ExcludeToolEcho:     true,
		}
	}

	tests := []struct {
		name   string
		answer string
		params scoring.FactorParams
		want   bool
	}{
		{
			name:   "identifier and synonym in the same sentence",
			answer: "The SMTP_PORT key is missing from the ConfigMap.",
			params: params("SMTP_PORT"),
			want:   true,
		},
		{
			name:   "identifier named but no deviation characterization",
			answer: "Something's wrong with the SMTP_PORT config.",
			params: params("SMTP_PORT"),
			want:   false,
		},
		{
			name:   "synonym in the sentence immediately after (radius one)",
			answer: "The SMTP_PORT value looks odd. Something is missing.",
			params: params("SMTP_PORT"),
			want:   true,
		},
		{
			name:   "synonym in the sentence immediately before (radius one)",
			answer: "Something is missing. The SMTP_PORT value looks odd.",
			params: params("SMTP_PORT"),
			want:   true,
		},
		{
			name:   "synonym two sentences away is outside the window",
			answer: "The SMTP_PORT value looks odd. The pod restarts. Something is missing.",
			params: params("SMTP_PORT"),
			want:   false,
		},
		{
			name:   "synonym two sentences before is outside the window",
			answer: "Something is missing. The pod restarts. The SMTP_PORT value looks odd.",
			params: params("SMTP_PORT"),
			want:   false,
		},
		{
			name:   "synonym on the adjacent line: a newline is a split point, not a wall",
			answer: "Check SMTP_PORT\nA key is missing",
			params: params("SMTP_PORT"),
			want:   true,
		},
		{
			name:   "synonym two lines away is outside the window",
			answer: "Check SMTP_PORT\nThe pod restarts\nA key is missing",
			params: params("SMTP_PORT"),
			want:   false,
		},
		{
			name:   "the 2026-08-22 DA-1 answer (run 20260822-143006-a1f0d1), verbatim",
			answer: da1AnswerVerbatim,
			params: params("SMTP_PORT"),
			want:   true,
		},
		{
			name:   "synonym match is case-insensitive",
			answer: "SMTP_PORT is ABSENT from smtp-config.",
			params: params("SMTP_PORT"),
			want:   true,
		},
		{
			name:   "multi-word synonym",
			answer: "The SMTP_PORT key does not exist in the ConfigMap.",
			params: params("SMTP_PORT"),
			want:   true,
		},
		{
			name:   "apostrophe synonym",
			answer: "SMTP_PORT doesn't exist there.",
			params: params("SMTP_PORT"),
			want:   true,
		},
		{
			name:   "conjunction: all required identifiers must be named",
			answer: "The SMTP_PORT key is missing.",
			params: params("SMTP_PORT", "smtp-config"),
			want:   false,
		},
		{
			name:   "conjunction satisfied, co-occurrence on one of them",
			answer: "The smtp-config map is fine otherwise. The SMTP_PORT key is missing.",
			params: params("SMTP_PORT", "smtp-config"),
			want:   true,
		},
		{
			name:   "no identifier at all",
			answer: "A key is missing somewhere.",
			params: params("SMTP_PORT"),
			want:   false,
		},
		{
			name:   "empty required identifier set is never satisfied",
			answer: "The SMTP_PORT key is missing.",
			params: params(),
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := scoring.Evidence{FinalAnswer: tt.answer, HasFinalAnswer: true}
			assert.Equal(t, tt.want, scoring.FactorIdentified(ev, tt.params))
		})
	}
}

// TestFactorIdentified_UnregisteredDeviationType confirms that a deviation type
// with no synonym list cannot silently satisfy the co-occurrence clause.
func TestFactorIdentified_UnregisteredDeviationType(t *testing.T) {
	ev := scoring.Evidence{FinalAnswer: "The SMTP_PORT key is missing.", HasFinalAnswer: true}
	got := scoring.FactorIdentified(ev, scoring.FactorParams{
		RequiredIdentifiers: []string{"SMTP_PORT"},
		DeviationType:       "wrong_value",
		Channels:            []string{scoring.ChannelAgentResponse},
	})
	assert.False(t, got)
}

// TestFactorIdentified_EchoedSentenceDoesNotCredit confirms that the
// co-occurrence clause runs over the echo-excluded scope, so a dumped tool
// response containing both the identifier and a synonym cannot satisfy it.
func TestFactorIdentified_EchoedSentenceDoesNotCredit(t *testing.T) {
	echoed := `{"message":"SMTP_PORT is missing from configmap/smtp-config"}`
	ev := scoring.Evidence{
		FinalAnswer:    "Here is what the tool said:\n" + echoed,
		ToolResults:    []string{echoed},
		StepCount:      1,
		HasFinalAnswer: true,
	}
	got := scoring.FactorIdentified(ev, scoring.FactorParams{
		RequiredIdentifiers: []string{"SMTP_PORT"},
		DeviationType:       "absent_key",
		Channels:            []string{scoring.ChannelAgentResponse},
		ExcludeToolEcho:     true,
	})
	assert.False(t, got, "quoting a tool response is not diagnosing")
}

// TestAbsentKeySynonymsV1 pins the ratified synonym list of §3.3 verbatim.
// It is a Layer-1 profile asset; drift here changes verdicts.
func TestAbsentKeySynonymsV1(t *testing.T) {
	want := []string{
		"missing", "absent", "not present", "not set", "not defined", "undefined",
		"not found", "does not exist", "doesn't exist", "no key", "lacks",
		"lacking", "omitted", "unset",
	}
	assert.Equal(t, want, scoring.AbsentKeySynonymsV1)

	syns, ok := scoring.SynonymsFor("absent_key")
	require.True(t, ok)
	assert.Equal(t, want, syns)

	_, ok = scoring.SynonymsFor("no_such_type")
	assert.False(t, ok)
}

// TestAbsentKeySynonymsV1_EachSynonymSatisfiesCoOccurrence walks every ratified
// synonym through factor_identified, so a typo in the list is a test failure
// rather than a silently unreachable band.
func TestAbsentKeySynonymsV1_EachSynonymSatisfiesCoOccurrence(t *testing.T) {
	for _, synonym := range scoring.AbsentKeySynonymsV1 {
		t.Run(synonym, func(t *testing.T) {
			ev := scoring.Evidence{
				FinalAnswer:    "The SMTP_PORT key " + synonym + " here.",
				HasFinalAnswer: true,
			}
			got := scoring.FactorIdentified(ev, scoring.FactorParams{
				RequiredIdentifiers: []string{"SMTP_PORT"},
				DeviationType:       "absent_key",
				Channels:            []string{scoring.ChannelAgentResponse},
			})
			assert.True(t, got)
		})
	}
}

// TestWithinStepBudget covers §2.3 and the step definition of §3.4.
func TestWithinStepBudget(t *testing.T) {
	tests := []struct {
		name     string
		steps    int
		answered bool
		budget   int
		want     bool
	}{
		{name: "under budget with an answer", steps: 4, answered: true, budget: 15, want: true},
		{name: "exactly at budget with an answer", steps: 15, answered: true, budget: 15, want: true},
		{name: "one over budget", steps: 16, answered: true, budget: 15, want: false},
		{name: "no tool calls at all, answered", steps: 0, answered: true, budget: 15, want: true},
		{name: "under budget but never concluded", steps: 4, answered: false, budget: 15, want: false},
		{name: "budget exhausted and never concluded", steps: 20, answered: false, budget: 15, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := scoring.Evidence{StepCount: tt.steps, HasFinalAnswer: tt.answered}
			assert.Equal(t, tt.want, scoring.WithinStepBudget(ev, tt.budget))
		})
	}
}
