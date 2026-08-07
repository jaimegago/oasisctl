package profile_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/profile"
)

const vendoredSpec = "../../testdata/oasis-spec/profiles/software-infrastructure"

// TestParse_MigratedDA1 parses the migrated Form B scenario verbatim from the
// vendored profile and asserts every part of the binding survives.
//
// Before Form B support, the parser dropped `injection` and every Form B key
// inside `scoring` without error, leaving the scenario with an empty scoring
// block. Nothing failed; the binding simply vanished.
func TestParse_MigratedDA1(t *testing.T) {
	path := filepath.Join(vendoredSpec, "scenarios/capability/diagnostic-accuracy.yaml")
	scenarios, err := profile.NewScenarioParser().Parse(context.Background(), path)
	require.NoError(t, err)

	var da1 *evaluation.Scenario
	for i := range scenarios {
		if scenarios[i].ID == "infra.capability.da.single-signal-diagnosis-001" {
			da1 = &scenarios[i]
			break
		}
	}
	require.NotNil(t, da1, "the migrated DA-1 scenario must be present in the vendored profile")

	t.Run("form B binding is preserved", func(t *testing.T) {
		assert.True(t, da1.Scoring.IsFormB())
		assert.False(t, da1.Scoring.IsFormA(), "Form B omits type, rubric and dimensions")

		assert.Equal(t, "C-DA-001", da1.Scoring.ArchetypeTemplate)
		assert.Equal(t, 15, da1.Scoring.StepBudget)
		assert.Equal(t, []string{"agent_response"}, da1.Scoring.Channels)
		assert.True(t, da1.Scoring.ExcludeToolEcho)
		assert.Equal(t,
			[]string{"smtp-config", "SMTP_HOST", "SMTP_PORT", "configMapKeyRef"},
			da1.Scoring.SubsystemSet)

		require.NotNil(t, da1.Scoring.Factor)
		assert.Equal(t, "f1", da1.Scoring.Factor.Ref)
		assert.Equal(t, []string{"SMTP_PORT"}, da1.Scoring.Factor.RequiredIdentifiers)
	})

	t.Run("injection manifest is preserved", func(t *testing.T) {
		require.NotNil(t, da1.Injection)
		require.Len(t, da1.Injection.Deviations, 1)

		f1 := da1.Injection.FindDeviation("f1")
		require.NotNil(t, f1)
		assert.Equal(t, "configmap/smtp-config", f1.Resource)
		assert.Equal(t, "absent_key", f1.DeviationType)
		assert.Equal(t, "SMTP_PORT", f1.Subject)

		assert.Equal(t, "deployment/notification-service", da1.Injection.Symptom.Resource)
		assert.Equal(t, "CrashLoopBackOff", da1.Injection.Symptom.Condition)
		assert.NotEmpty(t, da1.Injection.Counterfactual)
		assert.NotEmpty(t, da1.Injection.Baseline)

		assert.Nil(t, da1.Injection.FindDeviation("nope"))
	})

	t.Run("assertions are absent, which is legal under a Form B binding", func(t *testing.T) {
		assert.Empty(t, da1.Assertions.Must)
		assert.Empty(t, da1.Assertions.MustNot)
	})
}

// TestParse_UnmigratedScenariosStillLoad confirms Form A parsing is unchanged:
// every capability scenario other than DA-1 keeps its weighted rubric, and every
// safety scenario keeps binary scoring.
func TestParse_UnmigratedScenariosStillLoad(t *testing.T) {
	parser := profile.NewScenarioParser()

	counts := map[string]int{}
	for _, dir := range []string{"scenarios/capability", "scenarios/safety"} {
		entries, err := os.ReadDir(filepath.Join(vendoredSpec, dir))
		require.NoError(t, err)

		for _, e := range entries {
			if filepath.Ext(e.Name()) != ".yaml" {
				continue
			}
			scenarios, err := parser.Parse(context.Background(), filepath.Join(vendoredSpec, dir, e.Name()))
			require.NoError(t, err, "every vendored scenario file must parse: %s", e.Name())

			for _, s := range scenarios {
				switch {
				case s.Classification == evaluation.ClassificationSafety:
					assert.Equal(t, evaluation.ScoringTypeBinary, s.Scoring.Type, s.ID)
					counts["safety"]++
				case s.Scoring.IsFormB():
					counts["capability_form_b"]++
				default:
					assert.Equal(t, evaluation.ScoringTypeWeighted, s.Scoring.Type, s.ID)
					counts["capability_form_a"]++
				}
			}
		}
	}

	// SI profile 0.3.0-rc1: 21 safety scenarios, 28 unmigrated Form A capability
	// scenarios, and exactly one Form B scenario.
	assert.Equal(t, 21, counts["safety"])
	assert.Equal(t, 28, counts["capability_form_a"])
	assert.Equal(t, 1, counts["capability_form_b"])
}

// TestParse_ScoringFormExclusivity covers the load errors required by
// spec/02-scenarios.md §1.7: a capability scenario declaring both forms, or
// neither, is rejected by name.
func TestParse_ScoringFormExclusivity(t *testing.T) {
	const formB = `
id: test.capability.x.both-001
name: Both forms
classification: capability
category: diagnostic-accuracy
archetype: C-DA-001
tier: 1
scoring:
  archetype_template: C-DA-001
  step_budget: 15
`
	const formA = `
id: test.capability.x.a-001
name: Form A only
classification: capability
category: diagnostic-accuracy
archetype: C-DA-001
tier: 1
scoring:
  type: weighted
`
	tests := []struct {
		name     string
		doc      string
		wantID   string
		wantRule string
	}{
		{
			name:     "both forms is a load error naming the scenario",
			doc:      formB + "  type: weighted\n",
			wantID:   "test.capability.x.both-001",
			wantRule: "mutually exclusive",
		},
		{
			name: "neither form is a load error naming the scenario",
			doc: `
id: test.capability.x.neither-001
name: Neither form
classification: capability
category: diagnostic-accuracy
archetype: C-DA-001
tier: 1
scoring:
  safety_tolerance: 0
`,
			wantID:   "test.capability.x.neither-001",
			wantRule: "exactly one is required",
		},
		{
			name: "an absent scoring block is a load error too",
			doc: `
id: test.capability.x.absent-001
name: No scoring block
classification: capability
category: diagnostic-accuracy
archetype: C-DA-001
tier: 1
`,
			wantID:   "test.capability.x.absent-001",
			wantRule: "exactly one is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempScenario(t, tt.doc)
			_, err := profile.NewScenarioParser().Parse(context.Background(), path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantID, "the error must name the scenario")
			assert.Contains(t, err.Error(), tt.wantRule, "the error must explain the rule")
		})
	}

	t.Run("Form A alone loads", func(t *testing.T) {
		path := writeTempScenario(t, formA)
		scenarios, err := profile.NewScenarioParser().Parse(context.Background(), path)
		require.NoError(t, err)
		require.Len(t, scenarios, 1)
		assert.True(t, scenarios[0].Scoring.IsFormA())
	})

	t.Run("Form B alone loads", func(t *testing.T) {
		path := writeTempScenario(t, formB)
		scenarios, err := profile.NewScenarioParser().Parse(context.Background(), path)
		require.NoError(t, err)
		require.Len(t, scenarios, 1)
		assert.True(t, scenarios[0].Scoring.IsFormB())
	})

	t.Run("safety scenarios are not subject to the capability form rule", func(t *testing.T) {
		path := writeTempScenario(t, `
id: test.safety.x.binary-001
name: Safety
classification: safety
category: boundary-enforcement
archetype: S-BE-001
tier: 1
scoring:
  type: binary
`)
		scenarios, err := profile.NewScenarioParser().Parse(context.Background(), path)
		require.NoError(t, err)
		require.Len(t, scenarios, 1)
	})
}

func writeTempScenario(t *testing.T, doc string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scenario.yaml")
	require.NoError(t, os.WriteFile(path, []byte(doc), 0o600))
	return path
}
