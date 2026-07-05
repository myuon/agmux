package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/myuon/agmux/internal/automation"
	"github.com/myuon/agmux/internal/db"
	"github.com/myuon/agmux/internal/mcp"
	"github.com/myuon/agmux/internal/session"
)

// newAutomationTestServer builds a Server with a real SQLite-backed automation
// store and the full route table, without requiring a session manager.
func newAutomationTestServer(t *testing.T) *Server {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	s := &Server{
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		automations: automation.NewStore(sqlDB),
		startTime:   time.Now(),
	}
	s.setupRoutes()
	return s
}

func doJSON(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestAutomationCRUD(t *testing.T) {
	s := newAutomationTestServer(t)

	// Initially the list is an empty (non-null) array.
	rec := doJSON(t, s, http.MethodGet, "/api/automations", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Errorf("empty list body = %q, want []", got)
	}

	// Create
	rec = doJSON(t, s, http.MethodPost, "/api/automations",
		`{"name":"daily report","prompt":"summarize","triggerType":"interval","triggerValue":"30m","enabled":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var created automation.Automation
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created automation: %v", err)
	}
	if created.ID == "" || created.Name != "daily report" || !created.Enabled {
		t.Errorf("unexpected created automation: %+v", created)
	}

	// Get
	rec = doJSON(t, s, http.MethodGet, "/api/automations/"+created.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d", rec.Code, http.StatusOK)
	}

	// List contains the new automation
	rec = doJSON(t, s, http.MethodGet, "/api/automations", "")
	var list []automation.Automation
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode automation list: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Errorf("list = %+v, want 1 automation with id %s", list, created.ID)
	}

	// Update
	rec = doJSON(t, s, http.MethodPut, "/api/automations/"+created.ID,
		`{"name":"daily report v2","prompt":"summarize all","triggerType":"cron","triggerValue":"0 9 * * 1-5","projectPath":"/tmp/proj","enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var updated automation.Automation
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated automation: %v", err)
	}
	if updated.Name != "daily report v2" || updated.TriggerType != automation.TriggerCron ||
		updated.ProjectPath != "/tmp/proj" || updated.Enabled {
		t.Errorf("unexpected updated automation: %+v", updated)
	}

	// Toggle enabled
	rec = doJSON(t, s, http.MethodPut, "/api/automations/"+created.ID+"/enabled", `{"enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set enabled status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var toggled automation.Automation
	if err := json.Unmarshal(rec.Body.Bytes(), &toggled); err != nil {
		t.Fatalf("decode toggled automation: %v", err)
	}
	if !toggled.Enabled {
		t.Errorf("automation should be enabled after toggle: %+v", toggled)
	}

	// Runs are empty (non-null) for a fresh automation
	rec = doJSON(t, s, http.MethodGet, "/api/automations/"+created.ID+"/runs", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list runs status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Errorf("empty runs body = %q, want []", got)
	}

	// Delete
	rec = doJSON(t, s, http.MethodDelete, "/api/automations/"+created.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want %d", rec.Code, http.StatusOK)
	}
	rec = doJSON(t, s, http.MethodGet, "/api/automations/"+created.ID, "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("get after delete status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestAutomationRunsReturned(t *testing.T) {
	s := newAutomationTestServer(t)

	a, err := s.automations.Create(automation.CreateParams{
		Name: "a", Prompt: "p", TriggerType: automation.TriggerInterval, TriggerValue: "30m", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.automations.InsertRun(automation.Run{
		AutomationID: a.ID, FiredAt: time.Now(), SessionID: "sess1", Status: automation.RunSuccess,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.automations.InsertRun(automation.Run{
		AutomationID: a.ID, FiredAt: time.Now().Add(time.Minute), Status: automation.RunSkipped,
		Message: "previous run session sess1 is still active",
	}); err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, s, http.MethodGet, "/api/automations/"+a.ID+"/runs", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list runs status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var runs []automation.Run
	if err := json.Unmarshal(rec.Body.Bytes(), &runs); err != nil {
		t.Fatalf("decode runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("len(runs) = %d, want 2", len(runs))
	}
	// Newest first
	if runs[0].Status != automation.RunSkipped || runs[1].SessionID != "sess1" {
		t.Errorf("unexpected runs order/content: %+v", runs)
	}
}

func TestCreateAutomationValidation(t *testing.T) {
	s := newAutomationTestServer(t)

	tests := []struct {
		name string
		body string
	}{
		{"invalid cron", `{"name":"a","prompt":"p","triggerType":"cron","triggerValue":"not a cron","enabled":true}`},
		{"invalid interval", `{"name":"a","prompt":"p","triggerType":"interval","triggerValue":"abc","enabled":true}`},
		{"unknown trigger type", `{"name":"a","prompt":"p","triggerType":"weird","triggerValue":"30m","enabled":true}`},
		{"missing name", `{"name":"","prompt":"p","triggerType":"interval","triggerValue":"30m","enabled":true}`},
		{"missing prompt", `{"name":"a","prompt":"","triggerType":"interval","triggerValue":"30m","enabled":true}`},
		{"invalid json", `{`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doJSON(t, s, http.MethodPost, "/api/automations", tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			var resp map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("error response is not JSON: %v", err)
			}
			if resp["error"] == "" {
				t.Errorf("error response missing error message: %s", rec.Body.String())
			}
		})
	}
}

func TestAutomationNotFound(t *testing.T) {
	s := newAutomationTestServer(t)

	paths := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/automations/missing", ""},
		{http.MethodPut, "/api/automations/missing", `{"name":"a","prompt":"p","triggerType":"interval","triggerValue":"30m"}`},
		{http.MethodDelete, "/api/automations/missing", ""},
		{http.MethodPut, "/api/automations/missing/enabled", `{"enabled":true}`},
		{http.MethodGet, "/api/automations/missing/runs", ""},
	}
	for _, p := range paths {
		rec := doJSON(t, s, p.method, p.path, p.body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s status = %d, want %d", p.method, p.path, rec.Code, http.StatusNotFound)
		}
	}
}

func TestFilterAutomationSessions(t *testing.T) {
	sessions := []session.Session{
		{ID: "manual-null"},                              // automation_id was NULL in DB
		{ID: "manual-empty", AutomationID: ""},           // automation_id stored as ''
		{ID: "automated", AutomationID: "auto1"},         // created by an automation
		{ID: "controller", Type: session.TypeController}, // controller stays
	}
	got := filterAutomationSessions(sessions)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (%+v)", len(got), got)
	}
	for _, s := range got {
		if s.ID == "automated" {
			t.Errorf("automation session should be filtered out: %+v", got)
		}
	}

	// nil input yields a non-nil empty slice (serializes as []).
	if got := filterAutomationSessions(nil); got == nil || len(got) != 0 {
		t.Errorf("filterAutomationSessions(nil) = %+v, want empty non-nil slice", got)
	}
}

// TestMCPCreateAutomationPersists exercises the full path used by the
// create_automation MCP tool: MCP server -> HTTP API -> SQLite-backed store.
// It guarantees that an automation created from within a session is persisted
// and therefore visible on the settings screen (which reads the same store).
func TestMCPCreateAutomationPersists(t *testing.T) {
	s := newAutomationTestServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	mcpServer := mcp.NewServerForHTTP("sess-1", ts.URL)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_automation","arguments":{"name":"nightly cleanup","prompt":"clean up stale branches","triggerType":"cron","triggerValue":"0 3 * * *","projectPath":"/tmp/proj"}}}`
	out := mcpServer.HandleJSONRPC([]byte(req))

	var resp struct {
		Result *struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal MCP response: %v (body: %s)", err, string(out))
	}
	if resp.Error != nil {
		t.Fatalf("MCP error: %+v", resp.Error)
	}
	if resp.Result == nil || resp.Result.IsError {
		t.Fatalf("unexpected MCP result: %s", string(out))
	}

	// The automation is persisted in the store.
	list, err := s.automations.List()
	if err != nil {
		t.Fatalf("list automations: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(automations) = %d, want 1", len(list))
	}
	a := list[0]
	if a.Name != "nightly cleanup" || a.Prompt != "clean up stale branches" ||
		a.TriggerType != automation.TriggerCron || a.TriggerValue != "0 3 * * *" ||
		a.ProjectPath != "/tmp/proj" || !a.Enabled {
		t.Errorf("unexpected persisted automation: %+v", a)
	}
	if len(resp.Result.Content) == 0 || !strings.Contains(resp.Result.Content[0].Text, a.ID) {
		t.Errorf("MCP result should contain the created automation id %q: %s", a.ID, string(out))
	}

	// The automation is visible via the same API the settings screen uses.
	rec := doJSON(t, s, http.MethodGet, "/api/automations/"+a.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// TestMCPCreateAutomationInvalidTrigger verifies that server-side validation
// errors surface as tool errors and nothing is persisted.
func TestMCPCreateAutomationInvalidTrigger(t *testing.T) {
	s := newAutomationTestServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	mcpServer := mcp.NewServerForHTTP("sess-1", ts.URL)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_automation","arguments":{"name":"bad","prompt":"p","triggerType":"cron","triggerValue":"not a cron"}}}`
	out := mcpServer.HandleJSONRPC([]byte(req))

	var resp struct {
		Result *struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal MCP response: %v (body: %s)", err, string(out))
	}
	if resp.Result == nil || !resp.Result.IsError {
		t.Fatalf("expected tool error result, got: %s", string(out))
	}

	list, err := s.automations.List()
	if err != nil {
		t.Fatalf("list automations: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("len(automations) = %d, want 0 (nothing should be persisted)", len(list))
	}
}
