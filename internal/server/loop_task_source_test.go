package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/myuon/agmux/internal/automation"
	"github.com/myuon/agmux/internal/experimental"
)

func TestLoopTaskSourceAndRelations(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newAutomationTestServer(t)
	mk := func(name string) *automation.Automation {
		a, err := s.automations.Create(automation.CreateParams{Name: name, Prompt: "p", TriggerType: automation.TriggerInterval, TriggerValue: "1h"})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	scanner, fixer := mk("scanner"), mk("fixer")
	for _, q := range []string{
		`INSERT INTO sessions (id, name, project_path, automation_id) VALUES ('scan1', 's', '/tmp', '` + scanner.ID + `')`,
		`INSERT INTO sessions (id, name, project_path, automation_id) VALUES ('fix1', 'f', '/tmp', '` + fixer.ID + `')`,
		`INSERT INTO sessions (id, name, project_path) VALUES ('manual', 'm', '/tmp')`,
	} {
		if _, err := s.sqlDB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	// loops off: no relations in the response.
	rec := doJSON(t, s, http.MethodGet, "/api/automations/"+fixer.ID, "")
	if strings.Contains(rec.Body.String(), "feeders") {
		t.Fatalf("feeders must not appear while loops is off: %s", rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/automations", "")
	if strings.Contains(rec.Body.String(), "feeders") {
		t.Fatalf("feeders must not appear in list while loops is off: %s", rec.Body.String())
	}

	if err := experimental.Set("loops", true); err != nil {
		t.Fatal(err)
	}
	base := "/api/automations/" + fixer.ID + "/tasks"
	add := func(body string) automation.Task {
		rec := doJSON(t, s, http.MethodPost, base, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("add %s = %d: %s", body, rec.Code, rec.Body.String())
		}
		var task automation.Task
		_ = json.Unmarshal(rec.Body.Bytes(), &task)
		return task
	}
	if tk := add(`{"title":"plain"}`); tk.CreatedBy != "user" || tk.SourceAutomationID != "" {
		t.Fatalf("plain: %+v", tk)
	}
	if tk := add(`{"title":"manual","sourceSessionId":"manual"}`); tk.CreatedBy != "user" || tk.SourceAutomationID != "" {
		t.Fatalf("manual session: %+v", tk)
	}
	if tk := add(`{"title":"unknown","sourceSessionId":"nope"}`); tk.CreatedBy != "user" {
		t.Fatalf("unknown session: %+v", tk)
	}
	if tk := add(`{"title":"from scanner","sourceSessionId":"scan1"}`); tk.CreatedBy != "agent" || tk.SourceAutomationID != scanner.ID {
		t.Fatalf("scanner: %+v", tk)
	}
	// Own session: agent but no source (self is not a feeder).
	if tk := add(`{"title":"self","sourceSessionId":"fix1"}`); tk.CreatedBy != "agent" || tk.SourceAutomationID != "" {
		t.Fatalf("self: %+v", tk)
	}
	// MCP path records the session's own automation.
	rec = doJSON(t, s, http.MethodPost, "/api/sessions/scan1/loop-tasks/add", `{"title":"mcp"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("mcp add = %d", rec.Code)
	}
	var mcp automation.Task
	_ = json.Unmarshal(rec.Body.Bytes(), &mcp)
	if mcp.CreatedBy != "agent" || mcp.SourceAutomationID != scanner.ID || mcp.AutomationID != scanner.ID {
		t.Fatalf("mcp: %+v", mcp)
	}

	type rel struct {
		ID      string           `json:"id"`
		Feeders []automation.Ref `json:"feeders"`
		Feeds   []automation.Ref `json:"feeds"`
	}
	rec = doJSON(t, s, http.MethodGet, "/api/automations/"+fixer.ID, "")
	var got rel
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Feeders) != 1 || got.Feeders[0] != (automation.Ref{ID: scanner.ID, Name: "scanner"}) || len(got.Feeds) != 0 {
		t.Fatalf("fixer relations: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"feeds":[]`) {
		t.Fatalf("feeds should be an empty array: %s", rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/automations", "")
	var list []rel
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	seen := 0
	for _, a := range list {
		switch a.ID {
		case scanner.ID:
			seen++
			if len(a.Feeds) != 1 || a.Feeds[0].ID != fixer.ID || len(a.Feeders) != 0 {
				t.Fatalf("scanner relations: %+v", a)
			}
		case fixer.ID:
			seen++
			if len(a.Feeders) != 1 {
				t.Fatalf("fixer relations in list: %+v", a)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("expected both automations in list, saw %d", seen)
	}
}
