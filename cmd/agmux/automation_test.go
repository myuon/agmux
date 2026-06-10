package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/myuon/agmux/internal/automation"
)

func newTestAPI(t *testing.T, handler http.Handler) (*automationAPI, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &automationAPI{baseURL: srv.URL, client: srv.Client()}, srv
}

func TestAutomationAPIList(t *testing.T) {
	api, _ := newTestAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/automations" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]automation.Automation{
			{ID: "auto-1", Name: "daily-report", TriggerType: automation.TriggerCron, TriggerValue: "0 9 * * *", Enabled: true},
		})
	}))

	automations, err := api.list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(automations) != 1 {
		t.Fatalf("expected 1 automation, got %d", len(automations))
	}
	if automations[0].ID != "auto-1" || automations[0].Name != "daily-report" {
		t.Errorf("unexpected automation: %+v", automations[0])
	}
}

func TestAutomationAPICreate(t *testing.T) {
	var gotBody automationCreateRequest
	api, _ := newTestAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/automations" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(automation.Automation{ID: "auto-new", Name: gotBody.Name})
	}))

	created, err := api.create(automationCreateRequest{
		Name:         "nightly",
		Prompt:       "run nightly checks",
		TriggerType:  "interval",
		TriggerValue: "30m",
		ProjectPath:  "/tmp/proj",
		Enabled:      true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID != "auto-new" {
		t.Errorf("expected id auto-new, got %s", created.ID)
	}
	if gotBody.Name != "nightly" || gotBody.TriggerType != "interval" || gotBody.TriggerValue != "30m" || !gotBody.Enabled {
		t.Errorf("unexpected request body: %+v", gotBody)
	}
}

func TestAutomationAPISetEnabled(t *testing.T) {
	var gotBody map[string]bool
	api, _ := newTestAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/automations/auto-1/enabled" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(automation.Automation{ID: "auto-1", Enabled: gotBody["enabled"]})
	}))

	updated, err := api.setEnabled("auto-1", false)
	if err != nil {
		t.Fatalf("setEnabled: %v", err)
	}
	if updated.Enabled {
		t.Error("expected enabled=false in response")
	}
	if v, ok := gotBody["enabled"]; !ok || v {
		t.Errorf("expected request body {\"enabled\": false}, got %+v", gotBody)
	}
}

func TestAutomationAPIDelete(t *testing.T) {
	called := false
	api, _ := newTestAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodDelete || r.URL.Path != "/api/automations/auto-1" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
	}))

	if err := api.delete("auto-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !called {
		t.Error("expected DELETE request to be sent")
	}
}

func TestAutomationAPIListRuns(t *testing.T) {
	api, _ := newTestAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/automations/auto-1/runs" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("limit"); got != "10" {
			t.Errorf("expected limit=10, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]automation.Run{
			{ID: 1, AutomationID: "auto-1", FiredAt: time.Now(), Status: automation.RunSuccess, SessionID: "sess-1"},
			{ID: 2, AutomationID: "auto-1", FiredAt: time.Now(), Status: automation.RunSkipped, Message: "previous run still working"},
		})
	}))

	runs, err := api.listRuns("auto-1", 10)
	if err != nil {
		t.Fatalf("listRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}
	if runs[0].Status != automation.RunSuccess || runs[1].Status != automation.RunSkipped {
		t.Errorf("unexpected run statuses: %+v", runs)
	}
}

func TestAutomationAPIErrorResponse(t *testing.T) {
	api, _ := newTestAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid cron expression"})
	}))

	_, err := api.create(automationCreateRequest{Name: "bad"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid cron expression") {
		t.Errorf("expected server error message, got: %v", err)
	}
}

func TestAutomationAPIErrorWithoutJSONBody(t *testing.T) {
	api, _ := newTestAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}))

	err := api.delete("auto-1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected status code in error, got: %v", err)
	}
}

func TestResolveAutomationID(t *testing.T) {
	api, _ := newTestAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]automation.Automation{
			{ID: "abcd1234efgh", Name: "one"},
			{ID: "abxy9999zzzz", Name: "two"},
		})
	}))

	t.Run("unique prefix", func(t *testing.T) {
		id, err := api.resolveAutomationID("abcd")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if id != "abcd1234efgh" {
			t.Errorf("expected abcd1234efgh, got %s", id)
		}
	})

	t.Run("full id", func(t *testing.T) {
		id, err := api.resolveAutomationID("abxy9999zzzz")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if id != "abxy9999zzzz" {
			t.Errorf("expected abxy9999zzzz, got %s", id)
		}
	})

	t.Run("ambiguous prefix", func(t *testing.T) {
		_, err := api.resolveAutomationID("ab")
		if err == nil || !strings.Contains(err.Error(), "ambiguous") {
			t.Errorf("expected ambiguous error, got: %v", err)
		}
	})

	t.Run("no match", func(t *testing.T) {
		_, err := api.resolveAutomationID("zzzz")
		if err == nil || !strings.Contains(err.Error(), "no automation found") {
			t.Errorf("expected not found error, got: %v", err)
		}
	})
}
