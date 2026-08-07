package scoring

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// Band is the outcome of evaluating an archetype band template: the selected
// band's label and the score the template assigns it.
type Band struct {
	Label string
	Score float64
}

// BandTemplate is one archetype's percentage bands expressed as a decision table
// over registry primitives. Percentages and table shape live at the archetype,
// never at the scenario.
//
// Adding the next archetype is a data-plus-function addition: write its
// Bind/Evaluate pair and register it in templates. No orchestrator change.
type BandTemplate struct {
	// ID is the archetype_template identifier a scenario binding references.
	ID string
	// Bind validates a scenario's Form B binding against the roles this
	// template declares and returns the bound, ready-to-evaluate table. A
	// binding that does not supply the template's required roles is a
	// load-time error.
	Bind func(s *evaluation.Scenario) (BoundTemplate, error)
}

// BoundTemplate is a band template with a scenario's roles bound to it. Evaluate
// is a pure function of the evidence.
type BoundTemplate interface {
	Evaluate(ev Evidence) Band
}

// templates is the archetype band template registry, keyed by archetype_template
// identifier.
var templates = map[string]BandTemplate{
	TemplateCDA001: {ID: TemplateCDA001, Bind: bindCDA001},
}

// Lookup returns the band template registered under id.
func Lookup(id string) (BandTemplate, bool) {
	t, ok := templates[id]
	return t, ok
}

// RegisteredTemplates returns the registered template identifiers in sorted
// order, for deterministic error messages.
func RegisteredTemplates() []string {
	ids := make([]string, 0, len(templates))
	for id := range templates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Bind resolves a scenario's Form B binding to an evaluatable decision table.
// A binding referencing an unregistered template is an error, never a runtime
// skip: an unrecognized template would otherwise silently score every agent the
// same.
func Bind(s *evaluation.Scenario) (BoundTemplate, error) {
	id := s.Scoring.ArchetypeTemplate
	if id == "" {
		return nil, fmt.Errorf("scenario %s: scoring block declares no archetype_template", s.ID)
	}
	tmpl, ok := Lookup(id)
	if !ok {
		return nil, fmt.Errorf("scenario %s: scoring.archetype_template %q is not a registered band template (registered: %s)",
			s.ID, id, strings.Join(RegisteredTemplates(), ", "))
	}
	bound, err := tmpl.Bind(s)
	if err != nil {
		return nil, fmt.Errorf("scenario %s: bind archetype_template %s: %w", s.ID, id, err)
	}
	return bound, nil
}

// TemplateCDA001 is the Single-Signal Diagnosis archetype template.
const TemplateCDA001 = "C-DA-001"

// Band labels for C-DA-001, one per row of the §5.1 decision table.
const (
	// BandBudgetExhausted — no final answer within budget.
	BandBudgetExhausted = "budget_exhausted"
	// BandRootCauseIdentified — correct root cause.
	BandRootCauseIdentified = "root_cause_identified"
	// BandSubsystemIdentified — correct subsystem, wrong component.
	BandSubsystemIdentified = "subsystem_identified"
	// BandIncorrect — neither.
	BandIncorrect = "incorrect"
)

// cda001 is the bound C-DA-001 decision table.
type cda001 struct {
	stepBudget      int
	channels        []string
	excludeToolEcho bool
	factor          FactorParams
	subsystemSet    []string
}

// bindCDA001 validates and binds the C-DA-001 roles declared in
// scoring-decomposition.md §5.1: factor (ref plus required_identifiers),
// subsystem_set, channels, step_budget, exclude_tool_echo.
func bindCDA001(s *evaluation.Scenario) (BoundTemplate, error) {
	sc := s.Scoring

	if sc.StepBudget <= 0 {
		return nil, fmt.Errorf("role step_budget must be a positive integer, got %d", sc.StepBudget)
	}
	if len(sc.Channels) == 0 {
		return nil, fmt.Errorf("role channels is required")
	}
	// §2.5: diagnosis bands match agent_response only, and scenario bindings
	// MUST NOT widen it. Matching the reasoning trace would credit an agent that
	// considered the correct answer and discarded it.
	for _, ch := range sc.Channels {
		if ch != ChannelAgentResponse {
			return nil, fmt.Errorf("role channels must be [%s] for a diagnosis template, got %q", ChannelAgentResponse, ch)
		}
	}
	if sc.Factor == nil {
		return nil, fmt.Errorf("role factor is required")
	}
	if sc.Factor.Ref == "" {
		return nil, fmt.Errorf("role factor.ref is required")
	}
	if len(sc.Factor.RequiredIdentifiers) == 0 {
		return nil, fmt.Errorf("role factor.required_identifiers must be non-empty")
	}
	if len(sc.SubsystemSet) == 0 {
		return nil, fmt.Errorf("role subsystem_set must be non-empty")
	}

	// Ground truth is the injection manifest, not prose authorship of "the
	// answer": factor.ref binds to a declared deviation and the deviation type
	// is read from there.
	deviation := s.Injection.FindDeviation(sc.Factor.Ref)
	if deviation == nil {
		return nil, fmt.Errorf("role factor.ref %q does not resolve to an entry in injection.deviations", sc.Factor.Ref)
	}
	if _, ok := SynonymsFor(deviation.DeviationType); !ok {
		return nil, fmt.Errorf("deviation %q declares deviation_type %q, which has no registered synonym list",
			deviation.ID, deviation.DeviationType)
	}

	return &cda001{
		stepBudget:      sc.StepBudget,
		channels:        append([]string(nil), sc.Channels...),
		excludeToolEcho: sc.ExcludeToolEcho,
		factor: FactorParams{
			RequiredIdentifiers: append([]string(nil), sc.Factor.RequiredIdentifiers...),
			DeviationType:       deviation.DeviationType,
			Channels:            append([]string(nil), sc.Channels...),
			ExcludeToolEcho:     sc.ExcludeToolEcho,
		},
		subsystemSet: append([]string(nil), sc.SubsystemSet...),
	}, nil
}

// Evaluate runs the C-DA-001 decision table from scoring-decomposition.md §5.1,
// first match wins:
//
//	1  ¬ within_step_budget(step_budget)                              0.0
//	2  factor_identified(factor, channels)                            1.0
//	3  named_in_output(subsystem_set, channels, exclude_tool_echo)    0.5
//	4  else                                                           0.0
//
// Row 1 precedes row 2 deliberately: an agent that never concluded has not
// diagnosed anything, regardless of what its intermediate output contains.
func (t *cda001) Evaluate(ev Evidence) Band {
	if !WithinStepBudget(ev, t.stepBudget) {
		return Band{Label: BandBudgetExhausted, Score: 0.0}
	}
	if FactorIdentified(ev, t.factor) {
		return Band{Label: BandRootCauseIdentified, Score: 1.0}
	}
	named := NamedInOutput(ev, NamedInOutputParams{
		IdentifierSet:   t.subsystemSet,
		Channels:        t.channels,
		ExcludeToolEcho: t.excludeToolEcho,
	})
	if named {
		return Band{Label: BandSubsystemIdentified, Score: 0.5}
	}
	return Band{Label: BandIncorrect, Score: 0.0}
}
