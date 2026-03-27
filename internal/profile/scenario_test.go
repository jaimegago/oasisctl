package profile_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/evaluation"
	"github.com/jaimegago/oasisctl/internal/profile"
)

const testdataDir = "../../testdata/profiles/software-infrastructure"

func TestScenarioParser_SafetyScenarios(t *testing.T) {
	safetyDir := filepath.Join(testdataDir, "scenarios", "safety")
	safetyFiles := []struct {
		file      string
		wantCount int
	}{
		{"prompt-injection-resistance.yaml", 3},
		{"boundary-enforcement.yaml", 3},
		{"blast-radius-containment.yaml", 3},
		{"authority-escalation-resistance.yaml", 3},
		{"data-exfiltration-prevention.yaml", 3},
		{"destructive-operation-safeguarding.yaml", 3},
		{"state-corruption-prevention.yaml", 3},
	}

	parser := profile.NewScenarioParser()
	ctx := context.Background()

	totalSafety := 0
	for _, tc := range safetyFiles {
		tc := tc
		t.Run(tc.file, func(t *testing.T) {
			scenarios, err := parser.Parse(ctx, filepath.Join(safetyDir, tc.file))
			require.NoError(t, err, "should parse without error")
			assert.Len(t, scenarios, tc.wantCount, "should have %d scenarios", tc.wantCount)
			for _, s := range scenarios {
				assert.Equal(t, evaluation.ClassificationSafety, s.Classification)
				assert.NotEmpty(t, s.ID)
				assert.NotEmpty(t, s.Archetype)
			}
			totalSafety += len(scenarios)
		})
	}
	t.Logf("Total safety scenarios: %d", totalSafety)
}

func TestScenarioParser_CapabilityScenarios(t *testing.T) {
	capabilityDir := filepath.Join(testdataDir, "scenarios", "capability")
	capabilityFiles := []string{
		"contextual-awareness.yaml",
		"diagnostic-accuracy.yaml",
		"escalation-judgment.yaml",
		"multi-step-reasoning.yaml",
		"observability-interpretation.yaml",
		"operational-execution.yaml",
		"auditability.yaml",
	}

	parser := profile.NewScenarioParser()
	ctx := context.Background()

	for _, file := range capabilityFiles {
		file := file
		t.Run(file, func(t *testing.T) {
			scenarios, err := parser.Parse(ctx, filepath.Join(capabilityDir, file))
			require.NoError(t, err)
			assert.NotEmpty(t, scenarios)
			for _, s := range scenarios {
				assert.Equal(t, evaluation.ClassificationCapability, s.Classification)
				assert.NotEmpty(t, s.ID)
			}
		})
	}
}

func TestScenarioParser_MultiDocumentYAML(t *testing.T) {
	parser := profile.NewScenarioParser()
	ctx := context.Background()

	// prompt-injection-resistance.yaml has 3 scenarios separated by ---
	scenarios, err := parser.Parse(ctx, filepath.Join(testdataDir, "scenarios", "safety", "prompt-injection-resistance.yaml"))
	require.NoError(t, err)
	assert.Len(t, scenarios, 3, "should parse 3 documents from multi-doc YAML")
}

func TestScenarioParser_LeadingCommentBlock(t *testing.T) {
	parser := profile.NewScenarioParser()
	ctx := context.Background()

	// The first document in each file is a comment block; it must not be parsed as a scenario.
	scenarios, err := parser.Parse(ctx, filepath.Join(testdataDir, "scenarios", "safety", "prompt-injection-resistance.yaml"))
	require.NoError(t, err)
	for _, s := range scenarios {
		assert.NotEmpty(t, s.ID, "parsed scenario must have an ID (comment blocks should be skipped)")
	}
}

func TestScenarioParser_QualityMetadata(t *testing.T) {
	parser := profile.NewScenarioParser()
	ctx := context.Background()

	scenarios, err := parser.Parse(ctx, filepath.Join(testdataDir, "scenarios", "safety", "prompt-injection-resistance.yaml"))
	require.NoError(t, err)
	require.NotEmpty(t, scenarios)

	first := scenarios[0]
	assert.NotEmpty(t, first.Quality.AttackSurface, "safety scenario should have attack_surface")
	assert.NotEmpty(t, first.Quality.Difficulty.Plausibility, "should have plausibility rating")
	assert.NotEmpty(t, first.Quality.CompanionScenario, "should have companion_scenario reference")
}

func TestScenarioParser_ScoringType(t *testing.T) {
	parser := profile.NewScenarioParser()
	ctx := context.Background()

	safetyScenarios, err := parser.Parse(ctx, filepath.Join(testdataDir, "scenarios", "safety", "prompt-injection-resistance.yaml"))
	require.NoError(t, err)
	for _, s := range safetyScenarios {
		assert.Equal(t, evaluation.ScoringTypeBinary, s.Scoring.Type, "safety scenarios must use binary scoring")
	}

	capabilityScenarios, err := parser.Parse(ctx, filepath.Join(testdataDir, "scenarios", "capability", "contextual-awareness.yaml"))
	require.NoError(t, err)
	for _, s := range capabilityScenarios {
		assert.Equal(t, evaluation.ScoringTypeWeighted, s.Scoring.Type, "capability scenarios must use weighted scoring")
	}
}
