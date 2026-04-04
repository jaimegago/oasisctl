package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReportIdentityAndConfiguration_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/identity-and-configuration" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}

		resp := map[string]interface{}{
			"identity": map[string]interface{}{
				"name":        "joe",
				"version":     "0.4.2",
				"description": "AI infrastructure copilot",
			},
			"configuration": map[string]interface{}{
				"operational_mode": "read_write",
				"zone_model":       true,
				"interface_type":   "cli",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewHTTPClient(server.URL, "test-token")
	identity, config, err := client.ReportIdentityAndConfiguration(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if identity.Name != "joe" {
		t.Errorf("expected name joe, got %s", identity.Name)
	}
	if identity.Version != "0.4.2" {
		t.Errorf("expected version 0.4.2, got %s", identity.Version)
	}
	if identity.Description != "AI infrastructure copilot" {
		t.Errorf("expected description, got %s", identity.Description)
	}
	if config["operational_mode"] != "read_write" {
		t.Errorf("expected operational_mode=read_write, got %v", config["operational_mode"])
	}
	if config["zone_model"] != true {
		t.Errorf("expected zone_model=true, got %v", config["zone_model"])
	}
}

func TestReportIdentityAndConfiguration_404(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewHTTPClient(server.URL, "")
	_, _, err := client.ReportIdentityAndConfiguration(context.Background())
	if err == nil {
		t.Error("expected error for 404 response")
	}
}

func TestReportIdentityAndConfiguration_BearerToken(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		resp := map[string]interface{}{
			"identity":      map[string]interface{}{"name": "test", "version": "1.0"},
			"configuration": map[string]interface{}{},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewHTTPClient(server.URL, "my-secret")
	_, _, _ = client.ReportIdentityAndConfiguration(context.Background())

	if gotAuth != "Bearer my-secret" {
		t.Errorf("expected Bearer my-secret, got %s", gotAuth)
	}
}
