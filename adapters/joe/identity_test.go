package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeJoeIdentity serves the two joe routes the identity declaration is read
// from. An empty body for a route makes it answer 404, as a joe without the
// route does.
func fakeJoeIdentity(t *testing.T, version, agentIdentity string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	serve := func(path, body string) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if body == "" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
		})
	}
	serve("/api/v1/version", version)
	serve("/api/v1/agent-identity", agentIdentity)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// identityOf asks the adapter for its identity report and returns the raw
// identity object, so a test can tell an absent key from an empty one.
func identityOf(t *testing.T, joeURL string) map[string]json.RawMessage {
	t.Helper()
	// The adapter's real mux, not the test double other tests in this package
	// use: the identity handler is what is under test.
	adapter := httptest.NewServer(newServeMux(adapterConfig{
		joeURL: joeURL, operationalMode: "read_only", zoneModel: true, agentVersion: "v0.4.2",
	}))
	t.Cleanup(adapter.Close)

	resp, err := http.Get(adapter.URL + "/identity-and-configuration")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Identity map[string]json.RawMessage `json:"identity"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	return body.Identity
}

func wantString(t *testing.T, identity map[string]json.RawMessage, key, want string) {
	t.Helper()
	raw, ok := identity[key]
	if !ok {
		t.Errorf("%s is absent, want %q", key, want)
		return
	}
	var got string
	if err := json.Unmarshal(raw, &got); err != nil || got != want {
		t.Errorf("%s = %s, want %q", key, raw, want)
	}
}

// Every fact joe establishes reaches the identity report under the name the
// declaration gives it (oasis-spec spec/05-reporting.md §1.2).
func TestIdentity_DeclaresWhatJoeReports(t *testing.T) {
	joe := fakeJoeIdentity(t,
		`{"version":"v0.4.2","commit":"81e52ba","build_time":"2026-11-19T10:00:00Z","ui_digest":"abc"}`,
		`{"config_hash":"sha256:c0ffee","system_prompt_hash":"sha256:5eed","model":"gemini-2.5-pro","provider":"gemini"}`)

	identity := identityOf(t, joe.URL)

	wantString(t, identity, "binary_version", "v0.4.2")
	wantString(t, identity, "binary_sha", "81e52ba")
	wantString(t, identity, "config_hash", "sha256:c0ffee")
	wantString(t, identity, "system_prompt_hash", "sha256:5eed")
	wantString(t, identity, "declared_model", "gemini-2.5-pro")
	wantString(t, identity, "declared_provider", "gemini")
}

// The adapter adds no text to a stimulus, and says so: the key is present and
// null. An absent key would read as the adapter not having answered.
func TestIdentity_StatesThatItAppliesNoWrapperText(t *testing.T) {
	identity := identityOf(t, fakeJoeIdentity(t, "", "").URL)

	raw, ok := identity["fixed_wrapper_text"]
	if !ok {
		t.Fatal("fixed_wrapper_text is absent; it must be present and null")
	}
	if string(raw) != "null" {
		t.Errorf("fixed_wrapper_text = %s, want null", raw)
	}
}

// A fact joe does not establish is absent from the report, never an empty
// string and never a value made up for it.
func TestIdentity_AFactJoeDoesNotEstablishIsAbsent(t *testing.T) {
	facts := []string{"binary_version", "binary_sha", "config_hash", "system_prompt_hash", "declared_model", "declared_provider"}

	tests := []struct {
		name          string
		version       string
		agentIdentity string
		absent        []string
		present       []string
	}{
		{
			name:    "a joe that predates the agent-identity route",
			version: `{"version":"v0.4.2","commit":"81e52ba"}`,
			absent:  []string{"config_hash", "system_prompt_hash", "declared_model", "declared_provider"},
			present: []string{"binary_version", "binary_sha"},
		},
		{
			name:          "a build with no injected version or commit",
			version:       `{"version":"dev","commit":"none"}`,
			agentIdentity: `{"config_hash":"sha256:c0ffee","system_prompt_hash":"sha256:5eed","model":"m","provider":"p"}`,
			absent:        []string{"binary_version", "binary_sha"},
			present:       []string{"config_hash", "system_prompt_hash", "declared_model", "declared_provider"},
		},
		{
			name:          "a joe with no model configured",
			version:       `{"version":"v0.4.2","commit":"81e52ba"}`,
			agentIdentity: `{"config_hash":"sha256:c0ffee","system_prompt_hash":"sha256:5eed"}`,
			absent:        []string{"declared_model", "declared_provider"},
			present:       []string{"binary_version", "binary_sha", "config_hash", "system_prompt_hash"},
		},
		{
			name:   "joe answers neither route",
			absent: facts,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity := identityOf(t, fakeJoeIdentity(t, tt.version, tt.agentIdentity).URL)
			for _, key := range tt.absent {
				if raw, ok := identity[key]; ok {
					t.Errorf("%s = %s, want the key absent", key, raw)
				}
			}
			for _, key := range tt.present {
				if _, ok := identity[key]; !ok {
					t.Errorf("%s is absent, want it reported", key)
				}
			}
		})
	}
}

// An unreachable joe leaves every fact absent and the report still answers.
func TestIdentity_UnreachableJoeLeavesTheDeclarationEmpty(t *testing.T) {
	joe := fakeJoeIdentity(t, `{"version":"v0.4.2","commit":"81e52ba"}`, "")
	url := joe.URL
	joe.Close()

	identity := identityOf(t, url)
	wantString(t, identity, "name", "joe")
	for _, key := range []string{"binary_version", "binary_sha", "config_hash"} {
		if raw, ok := identity[key]; ok {
			t.Errorf("%s = %s with joe unreachable, want the key absent", key, raw)
		}
	}
}

// The step count is the length of joe's steps array, and is absent — not zero
// — when joe's response carried no array.
func TestTranslateResponse_StepCount(t *testing.T) {
	decode := func(body string) *AgentResponse {
		t.Helper()
		var jr JoeResponse
		if err := json.Unmarshal([]byte(body), &jr); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return translateResponse(&jr)
	}

	two := decode(`{"steps":[{"step_number":1},{"step_number":2}],"final_answer":"a"}`)
	if two.StepCount == nil || *two.StepCount != 2 {
		t.Errorf("step_count = %v, want 2", two.StepCount)
	}
	none := decode(`{"steps":[],"final_answer":"a"}`)
	if none.StepCount == nil || *none.StepCount != 0 {
		t.Errorf("step_count for an empty steps array = %v, want 0", none.StepCount)
	}
	absent := decode(`{"final_answer":"a"}`)
	if absent.StepCount != nil {
		t.Errorf("step_count = %d with no steps array, want it absent", *absent.StepCount)
	}
	raw, _ := json.Marshal(absent)
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(raw, &keys)
	if _, ok := keys["step_count"]; ok {
		t.Error("step_count is on the wire for a response with no steps array")
	}
}
