package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// The identity report's declaration reaches the runner fact by fact, and a
// fact the adapter did not send — or sent empty — stays nil: it is never an
// empty string that would read as a value (spec/05-reporting.md §1.2).
func TestReportIdentity_Declaration(t *testing.T) {
	serve := func(body string) *HTTPClient {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(server.Close)
		return NewHTTPClient(server.URL, "")
	}

	t.Run("a whole declaration", func(t *testing.T) {
		identity, _, err := serve(`{"identity":{"name":"joe","version":"v1",
			"binary_version":"v1","binary_sha":"abc1234","config_hash":"sha256:c","system_prompt_hash":"sha256:p",
			"declared_model":"gemini-2.5-pro","declared_provider":"gemini","fixed_wrapper_text":null},
			"configuration":{}}`).ReportIdentityAndConfiguration(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		d := identity.Declaration
		for name, got := range map[string]*string{
			"binary_version": d.BinaryVersion, "binary_sha": d.BinarySHA, "config_hash": d.ConfigHash,
			"system_prompt_hash": d.SystemPromptHash, "declared_model": d.DeclaredModel, "declared_provider": d.DeclaredProvider,
		} {
			if got == nil {
				t.Errorf("%s is nil, want the reported value", name)
			}
		}
		if *d.BinarySHA != "abc1234" || *d.DeclaredModel != "gemini-2.5-pro" {
			t.Errorf("binary_sha, declared_model = %q, %q", *d.BinarySHA, *d.DeclaredModel)
		}
		if !d.WrapperTextStated || d.FixedWrapperText != nil {
			t.Errorf("a null fixed_wrapper_text must read as stated, with no text: stated=%t text=%v", d.WrapperTextStated, d.FixedWrapperText)
		}
	})

	t.Run("an adapter that reports none of it", func(t *testing.T) {
		identity, _, err := serve(`{"identity":{"name":"agent","version":"1.0.0","config_hash":""},"configuration":{}}`).
			ReportIdentityAndConfiguration(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		d := identity.Declaration
		for name, got := range map[string]*string{
			"binary_version": d.BinaryVersion, "binary_sha": d.BinarySHA, "config_hash": d.ConfigHash,
			"system_prompt_hash": d.SystemPromptHash, "declared_model": d.DeclaredModel, "declared_provider": d.DeclaredProvider,
		} {
			if got != nil {
				t.Errorf("%s = %q, want nil", name, *got)
			}
		}
		if d.WrapperTextStated {
			t.Error("an absent fixed_wrapper_text must not read as stated")
		}
	})

	t.Run("a disclosed wrapper", func(t *testing.T) {
		identity, _, err := serve(`{"identity":{"name":"agent","version":"1","fixed_wrapper_text":"You are an SRE."},"configuration":{}}`).
			ReportIdentityAndConfiguration(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		d := identity.Declaration
		if !d.WrapperTextStated || d.FixedWrapperText == nil || *d.FixedWrapperText != "You are an SRE." {
			t.Errorf("wrapper text = %v (stated %t)", d.FixedWrapperText, d.WrapperTextStated)
		}
	})
}

// The step count is non-scoring metadata and is absent, not zero, when the
// adapter reports none.
func TestExecute_StepCount(t *testing.T) {
	serve := func(body string) string {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(server.Close)
		resp, err := NewHTTPClient(server.URL, "").Execute(context.Background(), evaluation.AgentRequest{Prompt: "p"})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Metadata == nil {
			return "nil"
		}
		raw, _ := resp.Metadata.MarshalJSON()
		return string(raw)
	}
	if got := serve(`{"actions":[],"final_answer":"f","step_count":4}`); got != `{"tokens":null,"agent_loop_duration_ms":null,"steps":4}` {
		t.Errorf("metadata = %s", got)
	}
	if got := serve(`{"actions":[],"final_answer":"f"}`); got != "nil" {
		t.Errorf("metadata = %s, want none for an adapter that reports nothing", got)
	}
}
