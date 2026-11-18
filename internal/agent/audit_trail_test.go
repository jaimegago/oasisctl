package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// The audit trail operation has three answers and they are kept apart
// (oasis-spec spec/04-execution.md §1.3): the trail, a statement that the
// adapter does not implement the operation, and a failure. These tests pin
// where each one lands on the wire.

func auditTrailServer(t *testing.T, status int, body string) (*HTTPClient, *http.Request) {
	t.Helper()
	seen := &http.Request{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = *r
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return NewHTTPClient(server.URL+"/", "test-token"), seen
}

func TestExecute_CarriesTheSessionID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"final_answer":"done","session_id":"sess-42"}`))
	}))
	defer server.Close()

	resp, err := NewHTTPClient(server.URL, "").Execute(context.Background(), evaluation.AgentRequest{Prompt: "p"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.SessionID != "sess-42" {
		t.Errorf("session_id = %q, want sess-42", resp.SessionID)
	}
	if resp.AuditTrail != nil {
		t.Error("Execute must never set the audit trail: the runner reads it through the operation")
	}
}

func TestExecute_AnAdapterThatReturnsNoSessionIDLeavesItEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"final_answer":"done"}`))
	}))
	defer server.Close()

	resp, err := NewHTTPClient(server.URL, "").Execute(context.Background(), evaluation.AgentRequest{Prompt: "p"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.SessionID != "" {
		t.Errorf("session_id = %q; the runner records what it received and never mints one", resp.SessionID)
	}
}

func TestAuditTrail_ReturnsTheTrailInOrder(t *testing.T) {
	client, seen := auditTrailServer(t, http.StatusOK, `{"entries":[
		{"action":{"operation":"get","target":"deployment/api"},"reasoning":"checking","trigger":{"type":"operator_request","ref":"p"}},
		{"action":{"operation":"list","target":"pod"},"reasoning":"looking","trigger":{"type":"alert","ref":"alert/high-memory"},"seq":2}
	]}`)

	trail, err := client.AuditTrail(context.Background(), "sess 42/a&b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seen.Method != http.MethodGet {
		t.Errorf("method = %s; the operation is a read", seen.Method)
	}
	if seen.URL.Path != "/audit-trail" {
		t.Errorf("path = %s, want /audit-trail", seen.URL.Path)
	}
	if got := seen.URL.Query().Get("session_id"); got != "sess 42/a&b" {
		t.Errorf("session_id = %q; the identifier is opaque and must arrive intact", got)
	}
	if got := seen.Header.Get("Authorization"); got != "Bearer test-token" {
		t.Errorf("Authorization = %q", got)
	}
	if len(trail.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(trail.Entries))
	}
	if trail.Entries[0].Action.Operation != "get" || trail.Entries[1].Action.Target != "pod" {
		t.Errorf("entries out of order or misread: %+v", trail.Entries)
	}
	if trail.Entries[1].Trigger == nil || trail.Entries[1].Trigger.Ref != "alert/high-memory" {
		t.Errorf("trigger misread: %+v", trail.Entries[1].Trigger)
	}
}

func TestAuditTrail_AnEmptyTrailIsATrail(t *testing.T) {
	for _, body := range []string{`{"entries":[]}`, `{"entries":null}`} {
		client, _ := auditTrailServer(t, http.StatusOK, body)
		trail, err := client.AuditTrail(context.Background(), "s")
		if err != nil {
			t.Fatalf("%s: an empty trail returned without error is a trail, got %v", body, err)
		}
		if trail == nil || trail.Entries == nil || len(trail.Entries) != 0 {
			t.Errorf("%s: want an empty, non-nil trail, got %+v", body, trail)
		}
	}
}

func TestAuditTrail_NotImplemented(t *testing.T) {
	// 404 from an adapter that routes by path, 405 from one whose only handler
	// takes POST on every path, 501 from one that says so outright.
	for _, status := range []int{http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented} {
		client, _ := auditTrailServer(t, status, "not here")
		trail, err := client.AuditTrail(context.Background(), "s")
		if !errors.Is(err, evaluation.ErrAuditTrailNotImplemented) {
			t.Errorf("status %d: want ErrAuditTrailNotImplemented, got %v", status, err)
		}
		if trail != nil {
			t.Errorf("status %d: a statement of non-implementation carries no trail", status)
		}
	}
}

func TestAuditTrail_AFailureIsNotNonImplementation(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"server error", http.StatusInternalServerError, "boom"},
		{"bad gateway", http.StatusBadGateway, ""},
		{"undecodable body", http.StatusOK, "<html>"},
		{"no entries array", http.StatusOK, `{"trail":[]}`},
		{"entries not a list", http.StatusOK, `{"entries":"none"}`},
	}
	for _, tc := range cases {
		client, _ := auditTrailServer(t, tc.status, tc.body)
		trail, err := client.AuditTrail(context.Background(), "s")
		if err == nil {
			t.Errorf("%s: want a failure, got trail %+v", tc.name, trail)
			continue
		}
		if errors.Is(err, evaluation.ErrAuditTrailNotImplemented) {
			t.Errorf("%s: a failure must not read as non-implementation", tc.name)
		}
	}
}

func TestAuditTrail_AFailureReportCarriesItsCause(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		client, _ := auditTrailServer(t, status, `{"agent_failure":{"cause":"audit store unreachable"}}`)
		_, err := client.AuditTrail(context.Background(), "s")
		var failure *evaluation.AuditTrailFailure
		if !errors.As(err, &failure) {
			t.Fatalf("status %d: want an AuditTrailFailure, got %v", status, err)
		}
		if failure.Cause != "audit store unreachable" {
			t.Errorf("status %d: cause = %q; it is recorded verbatim", status, failure.Cause)
		}
	}
}

func TestAuditTrail_AnUnreachableAdapterIsAFailure(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()

	_, err := NewHTTPClient(url, "").AuditTrail(context.Background(), "s")
	if err == nil || errors.Is(err, evaluation.ErrAuditTrailNotImplemented) {
		t.Errorf("an adapter that does not answer has failed, not declined: %v", err)
	}
}
