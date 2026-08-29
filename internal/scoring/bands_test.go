package scoring_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/scoring"
)

// da1Scenario returns a scenario whose Form B binding is the one ratified for
// infra.capability.da.single-signal-diagnosis-001 in the SI profile's
// diagnostic-accuracy.yaml. The parser-level test in internal/profile asserts
// that the vendored document parses into exactly this shape; this fixture keeps
// the band tests independent of file layout.
func da1Scenario() *evaluation.Scenario {
	return &evaluation.Scenario{
		ID:             "infra.capability.da.single-signal-diagnosis-001",
		Classification: evaluation.ClassificationCapability,
		Category:       "diagnostic-accuracy",
		Archetype:      "C-DA-001",
		Tier:           1,
		Injection: &evaluation.Injection{
			Deviations: []evaluation.Deviation{{
				ID:            "f1",
				Resource:      "configmap/smtp-config",
				DeviationType: "absent_key",
				Subject:       "SMTP_PORT",
			}},
			Symptom: evaluation.Symptom{
				Resource:  "deployment/notification-service",
				Condition: "CrashLoopBackOff",
			},
			Counterfactual: "restoring all deviations clears the symptom",
		},
		Scoring: evaluation.Scoring{
			ArchetypeTemplate: "C-DA-001",
			StepBudget:        15,
			Channels:          []string{"agent_response"},
			Factor: &evaluation.ScoringFactor{
				Ref:                 "f1",
				RequiredIdentifiers: []string{"SMTP_PORT"},
			},
			SubsystemSet:    []string{"smtp-config", "SMTP_HOST", "SMTP_PORT", "configMapKeyRef"},
			ExcludeToolEcho: true,
		},
	}
}

// adapterEvidence builds evidence in the shape the joe adapter emits: actions
// carrying full tool response bodies as compact-JSON strings, a reasoning trace,
// a final answer, and the diagnostic conclusion the agent declared.
//
// declaredCause is the cause the agent committed to. An EMPTY string models an
// agent that declared a conclusion and committed to nothing — an answer, and a
// different one from declaring nothing at all, which adapterEvidenceUndeclared
// models.
func adapterEvidence(declaredCause, answer, reasoning string, toolBodies []string, answered bool) scoring.Evidence {
	return buildEvidence(&evaluation.DiagnosticConclusion{RootCause: declaredCause}, answer, reasoning, toolBodies, answered)
}

// adapterEvidenceUndeclared is the same, for an agent that declared no
// diagnostic conclusion at all — the ordinary shape for an adapter that does not
// report one.
func adapterEvidenceUndeclared(answer, reasoning string, toolBodies []string, answered bool) scoring.Evidence {
	return buildEvidence(nil, answer, reasoning, toolBodies, answered)
}

// buildEvidence goes through EvidenceFromResponse rather than setting the
// projected fields directly, so these tests exercise the projection they depend
// on — the TrimSpace that makes a whitespace-only cause a non-commitment lives
// there, and a helper that set the fields by hand would assert a rule it had
// quietly reimplemented.
func buildEvidence(conclusion *evaluation.DiagnosticConclusion, answer, reasoning string, toolBodies []string, answered bool) scoring.Evidence {
	resp := &evaluation.AgentResponse{
		FinalAnswer: answer,
		Reasoning:   reasoning,
		Conclusion:  conclusion,
	}
	for i, body := range toolBodies {
		resp.Actions = append(resp.Actions, evaluation.AgentAction{
			ID:         "call_" + string(rune('a'+i%26)),
			Tool:       "kubectl_get",
			Arguments:  map[string]interface{}{"resource": "configmap/smtp-config"},
			Result:     body,
			DurationMs: 10,
		})
	}
	ev := scoring.EvidenceFromResponse(resp)
	// Allow modelling an agent that ran but never concluded.
	ev.HasFinalAnswer = answered
	return ev
}

const (
	// configMapBody is a realistic tool response: the smtp-config ConfigMap with
	// SMTP_PORT genuinely absent.
	configMapBody = `{"kind":"ConfigMap","metadata":{"name":"smtp-config","namespace":"default"},"data":{"SMTP_HOST":"smtp.internal"}}`
	// deploymentBody names SMTP_PORT via configMapKeyRef. An agent that dumps
	// this line has quoted a manifest, not diagnosed anything.
	deploymentBody = `{"kind":"Deployment","metadata":{"name":"notification-service"},"env":[{"name":"SMTP_PORT","valueFrom":{"configMapKeyRef":{"key":"SMTP_PORT","name":"smtp-config"}}}]}`
)

// TestCDA001_DecisionTable drives adapter-shaped evidence through the bound
// C-DA-001 template to each of the four rows of the §5.1 decision table.
func TestCDA001_DecisionTable(t *testing.T) {
	bound, err := scoring.Bind(da1Scenario())
	require.NoError(t, err)

	tests := []struct {
		name      string
		evidence  scoring.Evidence
		wantBand  string
		wantScore float64
	}{
		{
			name: "row 2: the declared cause names the key and characterizes the deviation",
			evidence: adapterEvidence(
				"The SMTP_PORT key is missing from the smtp-config ConfigMap.",
				"The notification-service pods are crashing.",
				"Checked the deployment, then the ConfigMap.",
				[]string{deploymentBody, configMapBody},
				true,
			),
			wantBand:  scoring.BandRootCauseIdentified,
			wantScore: 1.0,
		},
		{
			name: "row 2: a terse declaration is enough — the field is the whole subject",
			evidence: adapterEvidence(
				"SMTP_PORT not set in smtp-config",
				"See above.",
				"Checked the ConfigMap.",
				[]string{configMapBody},
				true,
			),
			wantBand:  scoring.BandRootCauseIdentified,
			wantScore: 1.0,
		},
		{
			name: "row 3: right locus, no deviation characterization",
			evidence: adapterEvidence(
				"Something in the SMTP_PORT config.",
				"Something's wrong with the SMTP_PORT config.",
				"Looked at the ConfigMap.",
				[]string{configMapBody},
				true,
			),
			wantBand:  scoring.BandSubsystemIdentified,
			wantScore: 0.5,
		},
		{
			name: "row 3: names the subsystem without naming the factor",
			evidence: adapterEvidence(
				"The smtp-config ConfigMap is involved somehow.",
				"I think the smtp-config ConfigMap is involved somehow.",
				"Looked around.",
				[]string{configMapBody},
				true,
			),
			wantBand:  scoring.BandSubsystemIdentified,
			wantScore: 0.5,
		},
		{
			name: "row 4: incorrect diagnosis names nothing in the subsystem set",
			evidence: adapterEvidence(
				"A network partition between nodes.",
				"The pods are crashing because of a network partition between nodes.",
				"Checked node health.",
				[]string{configMapBody},
				true,
			),
			wantBand:  scoring.BandIncorrect,
			wantScore: 0.0,
		},
		{
			name: "row 1: budget exhausted at sixteen actions beats a perfect declaration",
			evidence: adapterEvidence(
				"The SMTP_PORT key is missing from the smtp-config ConfigMap.",
				"The SMTP_PORT key is missing from the smtp-config ConfigMap.",
				"Long investigation.",
				sixteenBodies(),
				true,
			),
			wantBand:  scoring.BandBudgetExhausted,
			wantScore: 0.0,
		},
		{
			name: "row 1: never concluded beats a declaration, and is a real zero",
			evidence: adapterEvidence(
				"The SMTP_PORT key is missing from smtp-config.",
				"",
				"I suspect SMTP_PORT is missing from smtp-config.",
				[]string{configMapBody, deploymentBody},
				false,
			),
			wantBand:  scoring.BandBudgetExhausted,
			wantScore: 0.0,
		},
		{
			name: "boundary: exactly fifteen actions is within budget",
			evidence: adapterEvidence(
				"The SMTP_PORT key is missing from the smtp-config ConfigMap.",
				"The SMTP_PORT key is missing from the smtp-config ConfigMap.",
				"Investigation.",
				sixteenBodies()[:15],
				true,
			),
			wantBand:  scoring.BandRootCauseIdentified,
			wantScore: 1.0,
		},
		{
			name: "the answer text never earns a band on its own",
			evidence: adapterEvidence(
				"A network partition between nodes.",
				"The SMTP_PORT key is missing from the smtp-config ConfigMap.",
				"Checked the ConfigMap.",
				[]string{configMapBody},
				true,
			),
			wantBand:  scoring.BandIncorrect,
			wantScore: 0.0,
		},
		{
			name: "the reasoning trace never earns a band on its own",
			evidence: adapterEvidence(
				"I was unable to determine the cause.",
				"I was unable to determine the cause.",
				"The SMTP_PORT key is missing from the smtp-config ConfigMap.",
				[]string{configMapBody},
				true,
			),
			wantBand:  scoring.BandIncorrect,
			wantScore: 0.0,
		},
		{
			name: "the 2026-08-22 DA-1 answer no longer earns a band from its prose",
			evidence: adapterEvidence(
				"",
				da1AnswerVerbatim,
				"Read the pods, then the crashing pod's log.",
				[]string{`{"level":"ERROR","msg":"invalid configuration","error":"SMTP_PORT is required when SMTP_HOST is set"}`},
				true,
			),
			wantBand:  scoring.BandUnassessable,
			wantScore: 0.0,
		},
		{
			name: "a declared cause quoting a tool line still counts — exclusion does not run on the declaration",
			evidence: adapterEvidence(
				"SMTP_PORT is missing from configmap/smtp-config",
				"Here is the warning I found.",
				"Read the warning.",
				[]string{`{"warning":"SMTP_PORT is missing from configmap/smtp-config"}`},
				true,
			),
			wantBand:  scoring.BandRootCauseIdentified,
			wantScore: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			band := bound.Evaluate(tt.evidence)
			assert.Equal(t, tt.wantBand, band.Label)
			assert.InDelta(t, tt.wantScore, band.Score, 1e-9)
		})
	}
}

// sixteenBodies returns sixteen distinct tool response bodies, one per recorded
// invocation, so StepCount lands at sixteen against a budget of fifteen.
func sixteenBodies() []string {
	bodies := make([]string, 0, 16)
	for i := 0; i < 16; i++ {
		bodies = append(bodies, `{"probe":`+string(rune('0'+i%10))+`}`)
	}
	return bodies
}

// TestCDA001_EchoGamingIsClosedByTheSubject states what replaced echo exclusion
// on this template. The exclusion primitive no longer runs — a committed
// one-claim field cannot be padded with quotations the way a paragraph can — so
// the property is now carried by the subject itself: an agent that dumps a tool
// body into its ANSWER has declared nothing, and the band is unassessable rather
// than earned.
func TestCDA001_EchoGamingIsClosedByTheSubject(t *testing.T) {
	bound, err := scoring.Bind(da1Scenario())
	require.NoError(t, err)

	// A tool response that itself contains both the identifier and a synonym.
	echoed := `{"warning":"SMTP_PORT is missing from configmap/smtp-config"}`
	ev := adapterEvidenceUndeclared(echoed, "Quoted the tool.", []string{echoed}, true)

	band := bound.Evaluate(ev)
	assert.NotEqual(t, scoring.BandRootCauseIdentified, band.Label)
	assert.Less(t, band.Score, 1.0)
	assert.True(t, band.Unassessable)
}

// TestCDA001_AbsentDeclarationIsUnassessableNotZero is order part 4 at the band
// layer: the two absences are separated, neither scores, and neither is a wrong
// diagnosis. Row 1 still fires first for an agent that never concluded, because
// that IS an observed failure and a real zero.
func TestCDA001_AbsentDeclarationIsUnassessableNotZero(t *testing.T) {
	bound, err := scoring.Bind(da1Scenario())
	require.NoError(t, err)

	t.Run("no conclusion declared at all", func(t *testing.T) {
		ev := adapterEvidenceUndeclared(
			"The SMTP_PORT key is missing from the smtp-config ConfigMap.",
			"Checked the ConfigMap.",
			[]string{configMapBody},
			true,
		)
		band := bound.Evaluate(ev)
		assert.Equal(t, scoring.BandUnassessable, band.Label)
		assert.True(t, band.Unassessable)
		assert.Equal(t, evaluation.UnassessableNoDeclaredConclusion, band.UnassessableReason)
		assert.Equal(t, 0.0, band.Score, "the score is a float's zero, and the flag is what keeps it out of the average")
	})

	t.Run("conclusion declared, no cause committed", func(t *testing.T) {
		ev := adapterEvidence(
			"",
			"I could not determine the cause.",
			"Checked the ConfigMap.",
			[]string{configMapBody},
			true,
		)
		band := bound.Evaluate(ev)
		assert.Equal(t, scoring.BandUnassessable, band.Label)
		assert.True(t, band.Unassessable)
		assert.Equal(t, evaluation.UnassessableNoCommittedRootCause, band.UnassessableReason)
	})

	t.Run("whitespace-only cause is a non-commitment, not a cause", func(t *testing.T) {
		ev := adapterEvidence(
			"   ",
			"I could not determine the cause.",
			"Checked the ConfigMap.",
			[]string{configMapBody},
			true,
		)
		band := bound.Evaluate(ev)
		assert.True(t, band.Unassessable)
		assert.Equal(t, evaluation.UnassessableNoCommittedRootCause, band.UnassessableReason)
	})

	t.Run("budget exhaustion beats an absent declaration and stays a real zero", func(t *testing.T) {
		ev := adapterEvidenceUndeclared("", "Ran out of steps.", sixteenBodies(), false)
		band := bound.Evaluate(ev)
		assert.Equal(t, scoring.BandBudgetExhausted, band.Label)
		assert.False(t, band.Unassessable)
	})
}

// TestBind_Registry covers the registry contract: an unregistered template id is
// an error rather than a runtime skip, and every declared role is required.
func TestBind_Registry(t *testing.T) {
	t.Run("registered template binds", func(t *testing.T) {
		bound, err := scoring.Bind(da1Scenario())
		require.NoError(t, err)
		require.NotNil(t, bound)
	})

	t.Run("registered templates are listed deterministically", func(t *testing.T) {
		assert.Equal(t, []string{"C-DA-001"}, scoring.RegisteredTemplates())
		_, ok := scoring.Lookup("C-DA-001")
		assert.True(t, ok)
		_, ok = scoring.Lookup("C-DA-999")
		assert.False(t, ok)
	})

	t.Run("unregistered template is an error", func(t *testing.T) {
		s := da1Scenario()
		s.Scoring.ArchetypeTemplate = "C-DA-999"
		_, err := scoring.Bind(s)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a registered band template")
		assert.Contains(t, err.Error(), "C-DA-001")
	})

	t.Run("absent template is an error", func(t *testing.T) {
		s := da1Scenario()
		s.Scoring.ArchetypeTemplate = ""
		_, err := scoring.Bind(s)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "declares no archetype_template")
	})

	roleTests := []struct {
		name    string
		mutate  func(*evaluation.Scenario)
		wantErr string
	}{
		{
			name:    "step_budget is required",
			mutate:  func(s *evaluation.Scenario) { s.Scoring.StepBudget = 0 },
			wantErr: "step_budget",
		},
		{
			name:    "channels is required",
			mutate:  func(s *evaluation.Scenario) { s.Scoring.Channels = nil },
			wantErr: "channels is required",
		},
		{
			name:    "channels must not widen past agent_response",
			mutate:  func(s *evaluation.Scenario) { s.Scoring.Channels = []string{"agent_response", "reasoning_trace"} },
			wantErr: "must be [agent_response]",
		},
		{
			name:    "factor is required",
			mutate:  func(s *evaluation.Scenario) { s.Scoring.Factor = nil },
			wantErr: "factor is required",
		},
		{
			name:    "factor.ref is required",
			mutate:  func(s *evaluation.Scenario) { s.Scoring.Factor.Ref = "" },
			wantErr: "factor.ref is required",
		},
		{
			name:    "factor.required_identifiers must be non-empty",
			mutate:  func(s *evaluation.Scenario) { s.Scoring.Factor.RequiredIdentifiers = nil },
			wantErr: "required_identifiers",
		},
		{
			name:    "subsystem_set must be non-empty",
			mutate:  func(s *evaluation.Scenario) { s.Scoring.SubsystemSet = nil },
			wantErr: "subsystem_set",
		},
		{
			name:    "factor.ref must resolve to a declared deviation",
			mutate:  func(s *evaluation.Scenario) { s.Scoring.Factor.Ref = "f9" },
			wantErr: "does not resolve to an entry in injection.deviations",
		},
		{
			name:    "missing injection manifest cannot supply ground truth",
			mutate:  func(s *evaluation.Scenario) { s.Injection = nil },
			wantErr: "does not resolve to an entry in injection.deviations",
		},
		{
			name: "deviation type must have a registered synonym list",
			mutate: func(s *evaluation.Scenario) {
				s.Injection.Deviations[0].DeviationType = "wrong_value"
			},
			wantErr: "no registered synonym list",
		},
	}

	for _, tt := range roleTests {
		t.Run(tt.name, func(t *testing.T) {
			s := da1Scenario()
			tt.mutate(s)
			_, err := scoring.Bind(s)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Contains(t, err.Error(), s.ID, "the error must name the scenario")
		})
	}
}

// TestCDA001_BindingIsCopied confirms the bound table does not alias the
// scenario's slices, so mutating the scenario afterwards cannot change verdicts.
func TestCDA001_BindingIsCopied(t *testing.T) {
	s := da1Scenario()
	bound, err := scoring.Bind(s)
	require.NoError(t, err)

	s.Scoring.SubsystemSet[0] = "mutated"
	s.Scoring.Factor.RequiredIdentifiers[0] = "mutated"
	s.Scoring.Channels[0] = "mutated"

	ev := adapterEvidence("The SMTP_PORT key is missing from smtp-config.", "", "", []string{configMapBody}, true)
	band := bound.Evaluate(ev)
	assert.Equal(t, scoring.BandRootCauseIdentified, band.Label)
}
