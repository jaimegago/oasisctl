# oasisctl — Intent and Subcategory Support

## Context

The OASIS spec and Software Infrastructure profile have been updated with two new scenario fields and supporting profile-level structures:

1. `intent` — a natural-language statement describing the adversarial goal or trust property a scenario tests. Recommended by default, but profiles can promote it to required for specific categories. The SI profile requires it for safety scenarios, recommends it for capability scenarios.

2. `subcategory` — an optional field referencing a profile-defined subcategory within the parent category. Subcategories group scenarios by the safety property they test rather than by category taxonomy. A subcategory can span multiple parent categories.

The updated oasis-spec is in the testdata directory (update the test fixtures from the latest spec). All 21 safety scenarios now have both fields. Capability scenarios have neither.

## Type changes

### Scenario (internal/evaluation/types.go)

Add two fields to the Scenario struct, after Tier and before Description:

- Subcategory string with tags `yaml:"subcategory,omitempty"` and `json:"subcategory,omitempty"`
- Intent string with tags `yaml:"intent,omitempty"` and `json:"intent,omitempty"`

### Profile (internal/evaluation/types.go)

Add to the Profile struct:

- Subcategories: a slice of SubcategoryDefinition
- IntentPromotion: an IntentPromotionConfig

Define new types:

SubcategoryDefinition:
- Identifier (string) — the string used in scenario subcategory fields
- ParentCategories ([]string) — one or more parent category IDs this subcategory belongs to
- Description (string) — prose description

IntentPromotionConfig:
- RequiredFor ([]string) — list of classification values where intent is required (e.g., ["safety"])
- RecommendedFor ([]string) — list of classification values where intent is recommended (e.g., ["capability"])

### Category (internal/evaluation/types.go)

Add to the Category struct:

- Subcategories ([]string) — list of subcategory identifiers that belong to this category

### SafetySummary (internal/evaluation/types.go)

Add to the SafetySummary struct:

- SubcategoryResults (map[string]SubcategoryResult) with json/yaml tags `subcategory_results,omitempty`

Define:

SubcategoryResult:
- Total (int)
- Passed (int)
- Failed (int)

### ScenarioResult (internal/evaluation/types.go)

Add fields to carry scenario metadata through to reporting:

- Category (string) — the scenario's category
- Subcategory (string) — the scenario's subcategory (empty if none)
- Archetype (string) — the scenario's archetype

These are needed because the report writer currently derives category from the scenario ID using a heuristic (categoryFromID). With actual category and subcategory fields on the result, the report can group accurately.

## Profile loader changes

### Parse subcategories from safety-categories.md (internal/profile/loader.go or a new subcategory parser)

The safety-categories.md file now has a "Safety subcategories" section at the top with a markdown table. Parse it into []SubcategoryDefinition. The table has columns: Subcategory, Parent category(ies), Description. The Parent category column may contain comma-separated values (a subcategory can span multiple categories).

### Parse intent promotion from profile.md (internal/profile/profile_md.go)

The profile.md now has a section "5.2 Intent field promotion" with a YAML code block containing:

```yaml
profile_validation:
  intent:
    required_for:
      - safety
    recommended_for:
      - capability
```

Parse this YAML block and populate the IntentPromotionConfig on the Profile.

### Map subcategories to categories

After parsing both subcategories and categories, populate each Category's Subcategories field based on the parent category mapping in the subcategory definitions.

## Validation changes (internal/validation/)

### Profile validation (profile.go)

Add these checks to ValidateProfile:

- Subcategory reference validation: every scenario's subcategory field (when non-empty) must reference a subcategory defined in the profile's Subcategories list
- Subcategory parent validation: the scenario's category must be listed in the subcategory's ParentCategories (a scenario in category "boundary-enforcement" cannot use a subcategory whose parent is only "data-exfiltration-prevention")

### Scenario validation (scenario.go)

Add these checks to ValidateScenario:

- Intent presence: if the profile promotes intent to required for the scenario's classification, the intent field must be non-empty. This requires passing the profile's IntentPromotionConfig to the scenario validator (add it as a parameter or make it available through a context/config object).
- Intent minimum length: if intent is present, it must be at least 20 characters
- Intent uniqueness: across all scenarios in the profile, no two scenarios should have identical intent values. This is a profile-level check (in ValidateProfile), not per-scenario.

### Quality analysis (quality.go)

Add two new analysis functions:

ComputeIntentCoverage(scenarios []Scenario, config IntentPromotionConfig) — returns:
- Total scenarios
- Scenarios with intent
- Scenarios missing intent where required (by classification)
- Scenarios missing intent where recommended
- Duplicate intent values (list of duplicated strings with scenario IDs)

ComputeSubcategoryDistribution(scenarios []Scenario, subcategories []SubcategoryDefinition) — returns:
- Per subcategory: count of scenarios assigned
- Subcategories defined but unused (zero scenarios)
- Scenarios without a subcategory assignment (when a subcategory exists for their category)

## Report writer changes (internal/execution/report.go)

### Safety summary

Update buildSafetySummary to compute subcategory results. The ScenarioResult now carries Category and Subcategory fields, so group by subcategory and compute Total/Passed/Failed for each. Populate the SafetySummary.SubcategoryResults map.

### Category grouping

Replace the categoryFromID heuristic with the actual Category field from ScenarioResult. The current code parses dots in the scenario ID to guess the category — that's fragile. Now that ScenarioResult carries .Category, use it directly.

## Orchestrator changes (internal/execution/orchestrator.go)

### Populate ScenarioResult metadata

In runScenario, after scoring, copy the scenario's Category, Subcategory, and Archetype into the ScenarioResult before returning it. Currently the result only has ScenarioID — the report writer needs the metadata.

## Validate command changes (internal/cli/validate.go)

### Profile validation with intent and subcategories

The validate profile command currently loads the profile and checks basic integrity. Update it to also:

- Load scenarios (it already does this for behavior reference checks)
- Run intent coverage analysis
- Run subcategory distribution analysis
- When --report flag is set, print the intent coverage and subcategory distribution alongside the existing output

## Test fixture updates

Copy the updated scenario files from the latest oasis-spec into testdata/profiles/software-infrastructure/. The safety scenarios now have intent and subcategory fields. Update any existing tests that assert on scenario field counts or validation results.

## New tests

### Type parsing tests (internal/profile/)

- Test that scenarios with intent and subcategory fields parse correctly
- Test that scenarios without these fields still parse correctly (backward compatibility)
- Test that the subcategory section in safety-categories.md parses into SubcategoryDefinition structs
- Test that the intent promotion YAML block in profile.md parses into IntentPromotionConfig

### Validation tests (internal/validation/)

- Test: safety scenario without intent → validation error (profile requires it)
- Test: capability scenario without intent → validation warning (profile recommends it) — not an error
- Test: intent present but under 20 characters → validation error
- Test: two scenarios with identical intent → validation error
- Test: scenario with invalid subcategory reference → validation error
- Test: scenario with subcategory whose parent doesn't match scenario's category → validation error
- Test: scenario with valid subcategory → passes validation

### Quality analysis tests (internal/validation/)

- Test ComputeIntentCoverage with mix of present/missing intent values
- Test ComputeSubcategoryDistribution with used and unused subcategories

### Report tests (internal/execution/)

- Test that safety summary includes subcategory_results when subcategories are present
- Test that category grouping uses ScenarioResult.Category instead of ID parsing

## Summary of changes

Changed files:
- internal/evaluation/types.go — add Intent, Subcategory to Scenario; add SubcategoryDefinition, IntentPromotionConfig, SubcategoryResult types; add Subcategories and IntentPromotion to Profile; add Subcategories to Category; add SubcategoryResults to SafetySummary; add Category, Subcategory, Archetype to ScenarioResult
- internal/profile/loader.go — parse subcategories from safety-categories.md, parse intent promotion from profile.md, map subcategories to categories
- internal/profile/profile_md.go — parse intent promotion YAML block
- internal/validation/profile.go — subcategory reference validation, intent uniqueness check
- internal/validation/scenario.go — intent presence/length validation (needs profile config)
- internal/validation/quality.go — add ComputeIntentCoverage and ComputeSubcategoryDistribution
- internal/execution/orchestrator.go — populate Category/Subcategory/Archetype on ScenarioResult
- internal/execution/report.go — subcategory results in safety summary, use ScenarioResult.Category directly
- internal/cli/validate.go — display intent coverage and subcategory distribution in --report output
- testdata/profiles/software-infrastructure/ — update scenario fixtures from latest spec

New files:
- internal/profile/subcategory.go — subcategory parser (or add to loader.go)
- internal/profile/subcategory_test.go
- internal/validation/quality_test.go — add new test cases (file exists, extend it)
