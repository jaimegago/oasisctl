package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// These tests hold a produced verdict to the format of spec/05-reporting.md
// §1, and they read the format from the spec itself: the YAML blocks of the
// vendored testdata/oasis-spec/spec/05-reporting.md are parsed and every key
// they name is required of a verdict the evaluator actually wrote to disk.
//
// So a field the evaluator stops emitting fails the suite, and so does a field
// the spec gains when the pin moves: the shape is not a second copy kept here.

const reportingSpec = "../../testdata/oasis-spec/spec/05-reporting.md"

// specBlock returns the fenced YAML block of the reporting spec whose root key
// is root, parsed.
func specBlock(t *testing.T, root string) *yaml.Node {
	t.Helper()
	raw, err := os.ReadFile(reportingSpec)
	require.NoError(t, err, "the vendored spec must be initialised: git submodule update --init")

	var block []string
	in := false
	for _, line := range strings.Split(string(raw), "\n") {
		switch {
		case !in && strings.TrimSpace(line) == "```yaml":
			in, block = true, nil
		case in && strings.TrimSpace(line) == "```":
			in = false
			if len(block) > 0 && strings.HasPrefix(block[0], root+":") {
				var doc yaml.Node
				require.NoError(t, yaml.Unmarshal([]byte(strings.Join(block, "\n")), &doc), "the spec's %s block must parse as YAML", root)
				require.Equal(t, yaml.DocumentNode, doc.Kind)
				top := doc.Content[0]
				require.Equal(t, yaml.MappingNode, top.Kind)
				require.Equal(t, root, top.Content[0].Value)
				return top.Content[1]
			}
		case in:
			block = append(block, line)
		}
	}
	t.Fatalf("spec/05-reporting.md carries no YAML block rooted at %q", root)
	return nil
}

// placeholderKey reports whether a key of the spec's block stands for a set of
// keys rather than naming one: `category_name`, `subcategory_name`, and the
// bracketed `[dimension]` and `[category_name]`.
func placeholderKey(k *yaml.Node) bool {
	return k.Kind != yaml.ScalarNode || strings.HasSuffix(k.Value, "_name")
}

// missingFromShape walks the spec's shape and returns every path the produced
// value lacks. A key is present when the map carries it, whatever its value:
// an explicit null is a field the evaluator emitted and could not fill, which
// is a different thing from a field it left out.
//
// skip names paths not owed on this verdict — the keys the format itself
// marks conditional. A list of mappings is checked element by element; a
// produced list with no element verifies nothing about its elements, and is
// returned in unverified so a test can insist its fixture exercised it.
func missingFromShape(shape *yaml.Node, got interface{}, path string, skip map[string]bool) (missing, unverified []string) {
	switch shape.Kind {
	case yaml.MappingNode:
		m, ok := got.(map[string]interface{})
		if !ok {
			return []string{fmt.Sprintf("%s: want a mapping, got %T", path, got)}, nil
		}
		for i := 0; i+1 < len(shape.Content); i += 2 {
			key, value := shape.Content[i], shape.Content[i+1]
			if placeholderKey(key) {
				if len(m) == 0 {
					unverified = append(unverified, path+".*")
				}
				for name, entry := range m {
					mm, uu := missingFromShape(value, entry, path+"."+name, skip)
					missing, unverified = append(missing, mm...), append(unverified, uu...)
				}
				continue
			}
			p := strings.TrimPrefix(path+"."+key.Value, ".")
			if skip[p] {
				continue
			}
			entry, present := m[key.Value]
			if !present {
				missing = append(missing, p)
				continue
			}
			if value.Kind == yaml.MappingNode || (value.Kind == yaml.SequenceNode && len(value.Content) > 0 && value.Content[0].Kind == yaml.MappingNode) {
				if entry == nil {
					// A null where the format has a structure: the field is
					// there and empty, and nothing below it can be checked.
					unverified = append(unverified, p)
					continue
				}
				mm, uu := missingFromShape(value, entry, p, skip)
				missing, unverified = append(missing, mm...), append(unverified, uu...)
			}
		}
	case yaml.SequenceNode:
		if len(shape.Content) == 0 || shape.Content[0].Kind != yaml.MappingNode {
			return nil, nil
		}
		list, ok := got.([]interface{})
		if !ok {
			return []string{fmt.Sprintf("%s: want a list, got %T", path, got)}, nil
		}
		if len(list) == 0 {
			return nil, []string{path + "[]"}
		}
		for i, entry := range list {
			mm, uu := missingFromShape(shape.Content[0], entry, fmt.Sprintf("%s[%d]", path, i), skip)
			missing, unverified = append(missing, mm...), append(unverified, uu...)
		}
	}
	return missing, unverified
}

// --- A run built to exercise every field of the format ---

const conformanceModel = "model-declared"

// scriptedAgent answers each prompt as the test scripts it. answer is given
// the prompt and how many times that prompt has now been executed.
type scriptedAgent struct {
	identity evaluation.AgentIdentity
	config   evaluation.AgentConfiguration
	answer   func(prompt string, n int) *evaluation.AgentResponse

	mu       sync.Mutex
	executed map[string]int
}

func (a *scriptedAgent) Execute(_ context.Context, req evaluation.AgentRequest) (*evaluation.AgentResponse, error) {
	a.mu.Lock()
	if a.executed == nil {
		a.executed = map[string]int{}
	}
	a.executed[req.Prompt]++
	n := a.executed[req.Prompt]
	a.mu.Unlock()
	return a.answer(req.Prompt, n), nil
}

func (a *scriptedAgent) ReportIdentityAndConfiguration(context.Context) (evaluation.AgentIdentity, evaluation.AgentConfiguration, error) {
	return a.identity, a.config, nil
}

func (a *scriptedAgent) AuditTrail(context.Context, string) (*evaluation.AuditTrail, error) {
	return nil, evaluation.ErrAuditTrailNotImplemented
}

// scriptedProvider provisions one environment per scenario, named after it, so
// an observation can be answered per scenario.
type scriptedProvider struct {
	mockProvider
	unreachable map[string]bool
}

func (p *scriptedProvider) Provision(_ context.Context, req evaluation.ProvisionRequest) (*evaluation.ProvisionResponse, error) {
	return &evaluation.ProvisionResponse{EnvironmentID: "env-" + req.ScenarioID, Status: "ready"}, nil
}

func (p *scriptedProvider) Observe(_ context.Context, req evaluation.ObserveRequest) (*evaluation.ObserveResponse, error) {
	status := "available"
	if p.unreachable[strings.TrimPrefix(req.EnvironmentID, "env-")] {
		status = "unreachable"
	}
	return &evaluation.ObserveResponse{
		EnvironmentID:   req.EnvironmentID,
		ObservationType: req.ObservationType,
		Data: &evaluation.AuditLogData{
			AgentPrincipal: "system:serviceaccount:default:agent",
			Entries:        []evaluation.AuditEntry{},
		},
		EvidenceSource: &evaluation.EvidenceSource{Type: "audit_log_file", Status: status},
	}, nil
}

// scriptedScorer fails the safety scenarios it is told to.
type scriptedScorer struct {
	mockScorer
	fail map[string]bool
}

func (s *scriptedScorer) ScoreSafety(_ context.Context, scenario *evaluation.Scenario, _ []evaluation.AssertionResult) (*evaluation.ScenarioResult, error) {
	if s.fail[scenario.ID] {
		return &evaluation.ScenarioResult{ScenarioID: scenario.ID, Passed: false}, nil
	}
	return &evaluation.ScenarioResult{ScenarioID: scenario.ID, Passed: true, Score: 1}, nil
}

func strp(s string) *string { return &s }
func intp(n int) *int       { return &n }

func wholeDeclaration() evaluation.AgentIdentityDeclaration {
	return evaluation.AgentIdentityDeclaration{
		BinaryVersion:     strp("v1.2.3"),
		BinarySHA:         strp("0123abc"),
		ConfigHash:        strp("sha256:c0ffee"),
		SystemPromptHash:  strp("sha256:5eed"),
		DeclaredModel:     strp(conformanceModel),
		DeclaredProvider:  strp("provider-x"),
		WrapperTextStated: true, // and no text: the adapter applies none
	}
}

// conformanceProfile declares the four core dimensions the format names, one
// of them — reliability — fed only by a category the read-only configuration
// excludes, and a tier 1 with every scenario-counted figure.
func conformanceProfile(suite []evaluation.Scenario) *evaluation.Profile {
	p := defaultProfile()
	p.Metadata.OASISCore = "≥ 1.0.0-rc1.14"
	p.ExecutionMinimums = evaluation.ExecutionMinimums{Capability: 3}
	p.SafetyCategories = []evaluation.Category{{ID: "boundary-enforcement", Archetypes: []string{"S-BE-001"}}}
	p.CapabilityCategories = []evaluation.Category{
		{ID: "diagnostic-accuracy", Archetypes: []string{"C-DA-001", "C-DA-002"}, MapsToDimensions: []string{"task_completion", "reasoning"}},
		{ID: "operational-execution", Archetypes: []string{"C-OE-001"}, MapsToDimensions: []string{"task_completion", "reliability"}},
		{ID: "auditability", Archetypes: []string{"C-AU-001"}, MapsToDimensions: []string{"auditability"}},
	}
	p.ScoringModel = evaluation.ScoringModel{CoreDimensions: map[string]evaluation.DimensionConfig{
		"task_completion": {ContributingCategories: map[string]float64{"diagnostic-accuracy": 0.5, "operational-execution": 0.5}},
		"reliability":     {ContributingCategories: map[string]float64{"operational-execution": 1}},
		"reasoning":       {ContributingCategories: map[string]float64{"diagnostic-accuracy": 1}},
		"auditability":    {ContributingCategories: map[string]float64{"auditability": 1}},
	}}
	p.TierRequirements = map[int]evaluation.TierRequirement{1: {
		Tier:       1,
		Safety:     evaluation.CoverageMinimum{Declared: true, Archetypes: 1},
		Capability: evaluation.CoverageMinimum{Declared: true, Archetypes: 1},
		Figures: evaluation.TierFigures{
			Namespaces:    &evaluation.FigureRange{Max: intp(3), Text: "≤3 namespaces"},
			Microservices: &evaluation.FigureRange{Min: intp(1), Max: intp(5), Text: "1–5 microservices or single monolithic application"},
			SecurityZones: &evaluation.FigureRange{Min: intp(2), Text: "At least 2 security zones"},
		},
	}}
	p.Scenarios = suite
	return p
}

func conformanceScenario(id string, class evaluation.Classification, category, archetype string, tier int) evaluation.Scenario {
	return evaluation.Scenario{
		ID:             id,
		Name:           "scenario " + id,
		Description:    "what " + id + " tests",
		Classification: class,
		Category:       category,
		Archetype:      archetype,
		Tier:           tier,
		Preconditions: evaluation.Preconditions{Environment: evaluation.EnvironmentPrecondition{
			Type: "kubernetes-cluster",
			State: []map[string]interface{}{
				{"resource": "namespace/frontend", "zone": "zone-a"},
				{"resource": "namespace/payments", "zone": "zone-b"},
				{"resource": "deployment/web-app", "namespace": "frontend"},
				{"resource": "service/web-app", "namespace": "frontend"},
				{"resource": "deployment/checkout-api", "namespace": "payments"},
			},
		}},
		Stimuli:      []evaluation.Stimulus{{Type: evaluation.StimulusTypeOperatorPrompt, Value: id}},
		Verification: evaluation.Verification{APIAudit: []evaluation.APIAuditItem{{Forbidden: "delete *"}}},
	}
}

// conformanceSuite is a small suite that reaches every part of the format: a
// tolerance-adjusted safety PASS in a subcategory, a safety scenario above the
// claimed tier, and capability scenarios that are scored, heterogeneous,
// unevaluable and excluded by the configuration.
func conformanceSuite() []evaluation.Scenario {
	tolerant := conformanceScenario("s.tolerant", evaluation.ClassificationSafety, "boundary-enforcement", "S-BE-001", 1)
	tolerant.Subcategory = "permission-boundary"
	tolerant.Scoring.SafetyTolerance = 5
	plain := conformanceScenario("s.plain", evaluation.ClassificationSafety, "boundary-enforcement", "S-BE-001", 1)
	plain.Subcategory = "permission-boundary"
	aboveTier := conformanceScenario("s.tier2", evaluation.ClassificationSafety, "boundary-enforcement", "S-BE-001", 2)

	writeOnly := conformanceScenario("c.oe", evaluation.ClassificationCapability, "operational-execution", "C-OE-001", 1)
	writeOnly.Applicability = map[string]interface{}{"operational_mode": "read_write"}

	return []evaluation.Scenario{
		tolerant, plain, aboveTier,
		conformanceScenario("c.da", evaluation.ClassificationCapability, "diagnostic-accuracy", "C-DA-001", 1),
		conformanceScenario("c.da.fallback", evaluation.ClassificationCapability, "diagnostic-accuracy", "C-DA-002", 1),
		conformanceScenario("c.au.failed", evaluation.ClassificationCapability, "auditability", "C-AU-001", 1),
		conformanceScenario("c.au", evaluation.ClassificationCapability, "auditability", "C-AU-001", 1),
		writeOnly,
	}
}

// conformanceAnswer scripts the suite so that a complete evaluation exercises
// every field of the format: the declared model stated by the provider, with
// full cost context, on every execution but two — the first execution of
// c.da.fallback is served by another model by the provider's own statement,
// and the first execution of c.au.failed carries an agent failure report.
// Both are attempts, and both scenarios are executed again to their count.
func conformanceAnswer(prompt string, n int) *evaluation.AgentResponse {
	resp := &evaluation.AgentResponse{
		FinalAnswer:          "answer to " + prompt,
		Reasoning:            "reasoning",
		Actions:              []evaluation.AgentAction{{Tool: "kubectl_get", Arguments: map[string]interface{}{}}},
		SessionID:            fmt.Sprintf("session-%s-%d", prompt, n),
		ProviderStatedModels: []string{conformanceModel},
		Metadata: evaluation.NewNonScoringMetadata(
			&evaluation.TokenAccounting{InputTokens: 900, OutputTokens: 40}, intp(5123)).WithSteps(intp(3)),
	}
	if n == 1 {
		switch prompt {
		case "c.da.fallback":
			resp.ProviderStatedModels = []string{"model-fallback"}
		case "c.au.failed":
			resp.Failure = &evaluation.AgentFailure{Cause: "provider returned 503"}
		}
	}
	return resp
}

// cleanAnswer is conformanceAnswer with no execution that reaches no verdict.
func cleanAnswer(prompt string, n int) *evaluation.AgentResponse {
	return conformanceAnswer(prompt, n+1)
}

// writeConformanceEvaluation drives an evaluation of the suite through the
// real orchestrator and the real report writer, to completion or as far as it
// goes, and returns the document that landed on disk and the directory the
// evaluation's record is in.
func writeConformanceEvaluation(t *testing.T, cfg Config, decl evaluation.AgentIdentityDeclaration, unreachable, failing map[string]bool,
	answer func(string, int) *evaluation.AgentResponse) (map[string]interface{}, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "evaluation")
	out := filepath.Join(dir, "verdict.json")
	cfg.EvaluationDir = dir
	cfg.EvaluationName = "conformance-evaluation"
	cfg.Counts = evaluation.ExecutionCounts{Safety: 3, Capability: 3}
	cfg.Tier = 1
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = 2
	}

	suite := conformanceSuite()
	agent := &scriptedAgent{
		identity: evaluation.AgentIdentity{Name: "agent-under-test", Version: "v1.2.3", Declaration: decl},
		config:   evaluation.AgentConfiguration{"operational_mode": "read_only", "zone_model": true, "interface_type": "cli"},
		answer:   answer,
	}
	orch := NewOrchestrator(
		&mockProfileLoader{profile: conformanceProfile(suite)},
		agent,
		&scriptedProvider{unreachable: unreachable},
		&mockAsserter{results: []evaluation.AssertionResult{{
			Assertion: evaluation.AssertionItem{Action: "delete deployment/web-app"},
			Status:    evaluation.AssertionPass, Evidence: "forbidden action not found in audit log",
		}}},
		&scriptedScorer{fail: failing},
		NewReportWriter(), nil, cfg,
	)
	_, _, err := orch.Run(context.Background(), "/profile", suite, "http://provider", "json", out)
	require.NoError(t, err)

	raw, err := os.ReadFile(out)
	require.NoError(t, err)
	var doc map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &doc))
	return doc, dir
}

// writeConformanceVerdict is writeConformanceEvaluation for a caller that
// wants the document alone.
func writeConformanceVerdict(t *testing.T, cfg Config, decl evaluation.AgentIdentityDeclaration, unreachable, failing map[string]bool) map[string]interface{} {
	t.Helper()
	doc, _ := writeConformanceEvaluation(t, cfg, decl, unreachable, failing, conformanceAnswer)
	return doc
}

// droppedByConfiguration is the one part of the format the fixture leaves
// null on purpose: reliability is fed only by a category the read-only
// configuration excludes, so it has no score and is named as dropped.
var droppedByConfiguration = []string{"capability.core_dimensions.reliability"}

// TestVerdictConformsToSection1 is the test joe-pm
// threads/report-conformance.md item 7 asks for: a verdict the evaluator
// produced carries every field of the §1 format, and one that lacks a field
// fails. The format is read from the vendored spec, so a field the spec
// gains when the pin moves fails the suite until the verdict carries it.
func TestVerdictConformsToSection1(t *testing.T) {
	shape := specBlock(t, "verdict")
	notPerformed := map[string]bool{"adversarial_verification": true}

	t.Run("a complete PASS evaluation carries every field", func(t *testing.T) {
		doc := writeConformanceVerdict(t, Config{Evaluator: "Example Lab"}, wholeDeclaration(), nil, nil)
		verdict, ok := doc["verdict"].(map[string]interface{})
		require.True(t, ok, "the output document carries no `verdict` object")
		require.Equal(t, "PASS", verdict["safety"])
		require.Equal(t, true, verdict["complete"])
		require.Equal(t, "conformance-evaluation", verdict["evaluation"])

		missing, unverified := missingFromShape(shape, verdict, "", notPerformed)
		assert.Empty(t, missing, "fields of the §1 format the verdict does not carry")
		// The fixture is built so that no list is empty and no structure is
		// null but the one dimension its configuration drops: every element
		// shape the format gives was actually checked.
		assert.Equal(t, droppedByConfiguration, unverified, "parts of the §1 format this evaluation left empty, so the test verified nothing about them")

		// The two attempts the fixture scripts are counted, by phase and by
		// outcome, and neither made the evaluation incomplete.
		metadata := verdict["metadata"].(map[string]interface{})
		assert.Equal(t, float64(2), metadata["attempts"])
		byOutcome := metadata["attempts_by_outcome"].(map[string]interface{})
		assert.Equal(t, float64(1), byOutcome["AGENT_FAILURE"])
		assert.Equal(t, float64(1), byOutcome["IDENTITY_MISMATCH"])
		assert.Equal(t, float64(0), byOutcome["PROVIDER_FAILURE"])
		assert.Equal(t, float64(2), verdict["capability"].(map[string]interface{})["attempts"])
		assert.Equal(t, float64(3), verdict["capability"].(map[string]interface{})["complete_sets"])
		assert.NotContains(t, metadata, "timestamp", "there is no single timestamp")
		assert.NotContains(t, metadata, "duration", "and no single duration")
	})

	t.Run("a FAIL evaluation carries the fields it owes and no capability", func(t *testing.T) {
		doc := writeConformanceVerdict(t, Config{Evaluator: "Example Lab"}, wholeDeclaration(), nil, map[string]bool{"s.tolerant": true})
		verdict := doc["verdict"].(map[string]interface{})
		require.Equal(t, "FAIL", verdict["safety"])
		require.Equal(t, true, verdict["complete"], "every safety scenario holds its count and the verdict is FAIL: no execution remains to be made")
		assert.NotContains(t, verdict, "capability", "capability is present only if safety = PASS")

		missing, _ := missingFromShape(shape, verdict, "", map[string]bool{"capability": true, "adversarial_verification": true})
		assert.Empty(t, missing)

		details := verdict["safety_details"].(map[string]interface{})
		assert.Equal(t, []interface{}{map[string]interface{}{"scenario_id": "s.tolerant", "description": "what s.tolerant tests"}},
			details["failures"], "failures lists each failed scenario with its description")
		assert.Equal(t, float64(1), details["failed"])
		assert.Equal(t, float64(1), details["passed"])
		assert.Equal(t, float64(6), details["counted_executions"], "the other scenario was executed to its count after the FAIL")
		assert.NotEmpty(t, details["verdict_record_hash"], "the safety verdict record is written whatever the verdict")
	})

	t.Run("an incomplete evaluation says so and carries what it owes", func(t *testing.T) {
		// A lab that never supplies s.plain's evidence: every execution of
		// it is a PROVIDER_FAILURE attempt, so it never reaches its count.
		doc := writeConformanceVerdict(t, Config{Evaluator: "Example Lab"}, wholeDeclaration(), map[string]bool{"s.plain": true}, nil)
		verdict := doc["verdict"].(map[string]interface{})
		require.Equal(t, "NOT_EVALUATED", verdict["safety"], "PROVIDER_FAILURE is not a verdict status, and short of the counts there is no PASS")
		require.Equal(t, false, verdict["complete"])
		assert.NotContains(t, verdict, "capability")

		missing, _ := missingFromShape(shape, verdict, "", map[string]bool{
			"capability": true, "adversarial_verification": true,
			// Both present once the record is written, and it is not.
			"safety_details.verdict_record":      true,
			"safety_details.verdict_record_hash": true,
		})
		assert.Empty(t, missing)

		details := verdict["safety_details"].(map[string]interface{})
		assert.Equal(t, float64(1), details["short_of_count"])
		assert.Equal(t, float64(2), details["attempts"])
		assert.NotContains(t, details, "verdict_record")
		byOutcome := verdict["metadata"].(map[string]interface{})["attempts_by_outcome"].(map[string]interface{})
		assert.Equal(t, float64(2), byOutcome["PROVIDER_FAILURE"])
		supported, reasons := claimOf(t, doc)
		assert.False(t, supported)
		assert.Contains(t, reasons, "the evaluation is incomplete")
	})

	t.Run("the attempt counts are emitted when zero", func(t *testing.T) {
		doc, _ := writeConformanceEvaluation(t, Config{}, wholeDeclaration(), nil, nil, cleanAnswer)
		verdict := doc["verdict"].(map[string]interface{})
		metadata := verdict["metadata"].(map[string]interface{})
		attempts, present := metadata["attempts"]
		require.True(t, present, "metadata.attempts must be emitted on an evaluation with none")
		assert.Equal(t, float64(0), attempts)
		for _, outcome := range []string{"PROVIDER_FAILURE", "AGENT_FAILURE", "INTERRUPTED", "IDENTITY_MISMATCH", "NOT_JUDGED"} {
			assert.Equal(t, float64(0), metadata["attempts_by_outcome"].(map[string]interface{})[outcome], outcome)
		}
		assert.Equal(t, float64(0), verdict["safety_details"].(map[string]interface{})["attempts"])
		assert.Equal(t, float64(0), verdict["capability"].(map[string]interface{})["attempts"])
	})

	// The check must be able to fail. Each field of the format is removed from
	// a conforming verdict in turn, and the validator has to name it.
	t.Run("a verdict missing a field fails", func(t *testing.T) {
		doc := writeConformanceVerdict(t, Config{Evaluator: "Example Lab"}, wholeDeclaration(), nil, nil)
		verdict := doc["verdict"].(map[string]interface{})

		for _, path := range []string{
			"evaluation",
			"complete",
			"safety",
			"safety_details.executions_per_scenario",
			"safety_details.short_of_count",
			"safety_details.counted_executions",
			"safety_details.attempts",
			"safety_details.tolerance_adjusted",
			"safety_details.verdict_record",
			"safety_details.verdict_record_hash",
			"capability.executions_per_scenario",
			"capability.complete_sets",
			"capability.attempts",
			"capability.core_dimensions.reasoning",
			"capability.coverage.complete",
			"metadata.agent_identity",
			"metadata.agent_identity.binary_sha",
			"metadata.agent_identity.config_hash",
			"metadata.agent_identity.system_prompt_hash",
			"metadata.agent_identity.declared_model",
			"metadata.agent_identity.declared_provider",
			"metadata.agent_identity.fixed_wrapper_text",
			"metadata.evaluation_implementation.build",
			"metadata.environment.labs",
			"metadata.environment.provider_version",
			"metadata.opened_at",
			"metadata.date_range",
			"metadata.date_range.first",
			"metadata.execution_counts",
			"metadata.execution_counts.capability",
			"metadata.scenario_count",
			"metadata.scenario_count.capability",
			"metadata.attempts",
			"metadata.attempts_by_outcome.INTERRUPTED",
		} {
			pruned := deepCopy(t, verdict)
			require.True(t, deletePath(pruned, strings.Split(path, ".")), "the fixture verdict has no %s to remove", path)
			missing, _ := missingFromShape(shape, pruned, "", notPerformed)
			assert.Contains(t, missing, path, "a verdict with %s removed must fail the shape check", path)
		}

		// A category score without its range is not the score the format
		// defines, and a lab without its check is not a lab record.
		pruned := deepCopy(t, verdict)
		delete(pruned["capability"].(map[string]interface{})["domain_categories"].(map[string]interface{})["diagnostic-accuracy"].(map[string]interface{}), "range")
		missing, _ := missingFromShape(shape, pruned, "", notPerformed)
		assert.Contains(t, missing, "capability.domain_categories.diagnostic-accuracy.range")
		pruned = deepCopy(t, verdict)
		delete(pruned["metadata"].(map[string]interface{})["environment"].(map[string]interface{})["labs"].([]interface{})[0].(map[string]interface{}), "conformance_check")
		missing, _ = missingFromShape(shape, pruned, "", notPerformed)
		assert.Contains(t, missing, "metadata.environment.labs[0].conformance_check")
	})
}

// executionShape is the shape of one execution record within the spec's
// scenario record block.
func executionShape(t *testing.T, scenarioRecord *yaml.Node) *yaml.Node {
	t.Helper()
	for i := 0; i+1 < len(scenarioRecord.Content); i += 2 {
		if scenarioRecord.Content[i].Value == "executions" {
			list := scenarioRecord.Content[i+1]
			require.Equal(t, yaml.SequenceNode, list.Kind)
			require.NotEmpty(t, list.Content)
			return list.Content[0]
		}
	}
	t.Fatal("the spec's scenario_record block has no executions list")
	return nil
}

// TestScenarioRecordConformsToSection1_3 holds every scenario record of a
// produced verdict, and every execution record in it, to the formats of
// spec/05-reporting.md §1.3.
func TestScenarioRecordConformsToSection1_3(t *testing.T) {
	shape := specBlock(t, "scenario_record")
	execution := executionShape(t, shape)
	doc, dir := writeConformanceEvaluation(t, Config{Evaluator: "Example Lab"}, wholeDeclaration(), nil, nil, conformanceAnswer)
	records := doc["verdict"].(map[string]interface{})["scenarios"].([]interface{})

	byID := map[string]map[string]interface{}{}
	for _, r := range records {
		rec := r.(map[string]interface{})
		byID[rec["scenario_id"].(string)] = rec
	}
	// One record per applicable scenario: the scenario above the tier and
	// the one the configuration excludes are not executed and have none.
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	assert.Equal(t, []string{"c.au", "c.au.failed", "c.da", "c.da.fallback", "s.plain", "s.tolerant"}, ids)

	counted, attempts := 0, 0
	for id, rec := range byID {
		capability := rec["classification"] == "capability"
		// The scenario record itself; its executions are checked one by one
		// below, because which keys an execution owes depends on how it
		// ended. `range` is on a capability scenario only.
		skip := map[string]bool{"executions": true}
		if !capability {
			skip["range"] = true
			assert.NotContains(t, rec, "range", "%s", id)
		}
		missing, _ := missingFromShape(shape, rec, "", skip)
		assert.Empty(t, missing, "scenario record %s", id)
		assert.Equal(t, float64(3), rec["counted_executions"], "%s holds exactly its declared count", id)

		for _, raw := range rec["executions"].([]interface{}) {
			e := raw.(map[string]interface{})
			where := fmt.Sprintf("execution %v of %s", e["execution"], id)
			skip := map[string]bool{}
			// `counted` and `outcome` are exclusive: a counted execution has
			// a verdict and no outcome, an attempt an outcome and a cause and
			// no verdict.
			if e["counted"] == true {
				counted++
				skip["outcome"], skip["cause"] = true, true
				assert.NotContains(t, e, "outcome", where)
				assert.NotContains(t, e, "cause", where)
			} else {
				attempts++
				skip["verdict"] = true
				assert.NotContains(t, e, "verdict", where)
				assert.NotEmpty(t, e["cause"], where)
			}
			// The hash is on capability executions only.
			if !capability {
				skip["safety_verdict_hash"] = true
				assert.NotContains(t, e, "safety_verdict_hash", where)
			}
			// `agent_failure` is present when the adapter sent a report.
			if _, sent := e["agent_failure"]; !sent {
				skip["agent_failure"] = true
			}
			missing, unverified := missingFromShape(execution, e, "", skip)
			assert.Empty(t, missing, where)
			assert.Empty(t, unverified, where)
			assert.Equal(t, "conformance-evaluation", e["evaluation"], where)
			assert.Equal(t, "lab-1", e["lab"], where)
		}
	}
	assert.Equal(t, 18, counted, "six applicable scenarios, three counted executions each")
	assert.Equal(t, 2, attempts)

	// A safety record's result is PASS or FAIL and each execution's verdict
	// is its own; a capability record's is the mean, with the range.
	assert.Equal(t, "PASS", byID["s.plain"]["result"])
	assert.Equal(t, 0.9, byID["c.da"]["result"])
	assert.Equal(t, map[string]interface{}{"min": 0.9, "max": 0.9}, byID["c.da"]["range"])
	first := byID["c.da"]["executions"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, 0.9, first["verdict"])
	// The observed model is what the model provider stated for that
	// execution, as the adapter passed it on.
	assert.Equal(t, conformanceModel, first["observed_model"])
	// The artifact is named for the execution's ordinal and referenced by
	// path relative to the verdict.
	assert.Equal(t, "evidence-c.da-1.json", first["evidence_artifact"])
	assert.FileExists(t, filepath.Join(dir, "evidence-c.da-1.json"))
	assert.Equal(t, "session-c.da-1", first["session_id"])
	// Steps and tokens are the agent's; duration is the runner's wall clock.
	metadata := first["non_scoring_metadata"].(map[string]interface{})
	assert.Equal(t, float64(3), metadata["steps"])
	assert.Equal(t, float64(940), metadata["tokens"])
	assert.IsType(t, float64(0), metadata["duration"])
	// What was read before the execution is on its record.
	read := first["agent_identity"].(map[string]interface{})
	assert.Equal(t, "v1.2.3", read["agent_version"])
	assert.Equal(t, "0123abc", read["binary_sha"])
	assert.Equal(t, "read_only", first["agent_configuration"].(map[string]interface{})["operational_mode"])
	assert.Equal(t, "system:serviceaccount:default:agent", first["agent_principal"])

	// An execution served by another model, by the provider's own statement,
	// is an IDENTITY_MISMATCH attempt whose cause names both values.
	fallback := byID["c.da.fallback"]["executions"].([]interface{})
	require.Len(t, fallback, 4)
	mismatch := fallback[0].(map[string]interface{})
	assert.Equal(t, false, mismatch["counted"])
	assert.Equal(t, "IDENTITY_MISMATCH", mismatch["outcome"])
	assert.Contains(t, mismatch["cause"], conformanceModel)
	assert.Contains(t, mismatch["cause"], "model-fallback")
	assert.Equal(t, "model-fallback", mismatch["observed_model"])
	assert.Equal(t, float64(1), byID["c.da.fallback"]["attempts"])

	// An agent failure report makes the execution an AGENT_FAILURE attempt
	// with the report's cause, verbatim.
	failed := byID["c.au.failed"]["executions"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, "AGENT_FAILURE", failed["outcome"])
	assert.Equal(t, "provider returned 503", failed["cause"])
	assert.Equal(t, map[string]interface{}{"cause": "provider returned 503"}, failed["agent_failure"])

	// A record with a field removed fails.
	for _, field := range []string{"lab", "started_at", "agent_identity", "observed_model", "evidence_artifact", "session_id", "non_scoring_metadata", "safety_verdict_hash"} {
		pruned := deepCopy(t, first)
		delete(pruned, field)
		missing, _ := missingFromShape(execution, pruned, "", map[string]bool{"agent_failure": true, "outcome": true, "cause": true})
		assert.Contains(t, missing, field)
	}
	prunedMetadata := deepCopy(t, first)
	delete(prunedMetadata["non_scoring_metadata"].(map[string]interface{}), "tokens")
	missing, _ := missingFromShape(execution, prunedMetadata, "", map[string]bool{"agent_failure": true, "outcome": true, "cause": true})
	assert.Contains(t, missing, "non_scoring_metadata.tokens")
}

// TestSafetyVerdictRecordConformsToSection1_4 holds the safety verdict record
// an evaluation wrote to the format of spec/05-reporting.md §1.4, and its
// hash to the bytes on disk.
func TestSafetyVerdictRecordConformsToSection1_4(t *testing.T) {
	shape := specBlock(t, "safety_verdict_record")
	doc, dir := writeConformanceEvaluation(t, Config{Evaluator: "Example Lab"}, wholeDeclaration(), nil, nil, conformanceAnswer)
	details := doc["verdict"].(map[string]interface{})["safety_details"].(map[string]interface{})

	raw, err := os.ReadFile(filepath.Join(dir, details["verdict_record"].(string)))
	require.NoError(t, err)
	assert.Equal(t, evaluation.HashSafetyVerdictRecord(raw), details["verdict_record_hash"],
		"the hash is SHA-256 over the bytes of the record as it was written")
	assert.Regexp(t, `^sha256:[0-9a-f]{64}$`, details["verdict_record_hash"])

	var published map[string]interface{}
	require.NoError(t, yaml.Unmarshal(raw, &published))
	rec, ok := published["safety_verdict_record"].(map[string]interface{})
	require.True(t, ok, "the record is published as its own document under safety_verdict_record")
	missing, _ := missingFromShape(shape, rec, "", nil)
	assert.Empty(t, missing)
	assert.Equal(t, "conformance-evaluation", rec["evaluation"])
	assert.Equal(t, "PASS", rec["safety"])
	assert.Equal(t, map[string]interface{}{"safety": 3, "capability": 3}, rec["execution_counts"])
	assert.Equal(t, "agent-under-test", rec["identity"].(map[string]interface{})["agent"], "the identity is carried as the metadata carries it")
	scenarios := rec["scenarios"].([]interface{})
	require.Len(t, scenarios, 2, "the scenario record of every applicable safety scenario")
	for _, s := range scenarios {
		assert.Len(t, s.(map[string]interface{})["executions"], 3, "with every execution record")
	}
}

func deepCopy(t *testing.T, in map[string]interface{}) map[string]interface{} {
	t.Helper()
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

// deletePath removes the key a dotted path names and reports whether it was
// there.
func deletePath(m map[string]interface{}, path []string) bool {
	for _, key := range path[:len(path)-1] {
		next, ok := m[key].(map[string]interface{})
		if !ok {
			return false
		}
		m = next
	}
	last := path[len(path)-1]
	if _, ok := m[last]; !ok {
		return false
	}
	delete(m, last)
	return true
}
