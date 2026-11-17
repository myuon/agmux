package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/myuon/agmux/internal/automation"
	"github.com/myuon/agmux/internal/experimental"
)

func TestLoopTasksAPIDisabled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newAutomationTestServer(t)
	a, err := s.automations.Create(automation.CreateParams{
		Name: "l", Prompt: "p", TriggerType: automation.TriggerInterval, TriggerValue: "1h",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/automations/" + a.ID + "/tasks"},
		{http.MethodPost, "/api/automations/" + a.ID + "/tasks"},
		{http.MethodPut, "/api/automations/" + a.ID + "/tasks/1"},
		{http.MethodDelete, "/api/automations/" + a.ID + "/tasks/1"},
		{http.MethodPost, "/api/sessions/s1/loop-tasks/next"},
	} {
		rec := doJSON(t, s, c.method, c.path, `{"title":"x"}`)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404 while loops is off", c.method, c.path, rec.Code)
		}
	}
}

func TestLoopTasksAPIFlow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := experimental.Set("loops", true); err != nil {
		t.Fatal(err)
	}
	s := newAutomationTestServer(t)
	a, err := s.automations.Create(automation.CreateParams{
		Name: "l", Prompt: "p", TriggerType: automation.TriggerInterval, TriggerValue: "1h",
	})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/automations/" + a.ID + "/tasks"

	rec := doJSON(t, s, http.MethodPost, base, `{"title":"user task"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, s, http.MethodPost, base, `{"title":""}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty title = %d, want 400", rec.Code)
	}

	// A session that is not from an automation is rejected.
	if _, err := s.sqlDB.Exec(`INSERT INTO sessions (id, name, project_path) VALUES ('manual', 'm', '/tmp')`); err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, s, http.MethodPost, "/api/sessions/manual/loop-tasks/next", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("manual session next = %d, want 400", rec.Code)
	}

	// An automation session takes the task and links it to its run.
	if _, err := s.sqlDB.Exec(`INSERT INTO sessions (id, name, project_path, automation_id) VALUES ('auto1', 'a', '/tmp', ?)`, a.ID); err != nil {
		t.Fatal(err)
	}
	run, err := s.automations.InsertRun(automation.Run{AutomationID: a.ID, SessionID: "auto1", Status: automation.RunSuccess})
	if err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, s, http.MethodPost, "/api/sessions/auto1/loop-tasks/next", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("next = %d: %s", rec.Code, rec.Body.String())
	}
	var next struct {
		Task *automation.Task `json:"task"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &next); err != nil || next.Task == nil {
		t.Fatalf("next body: %s", rec.Body.String())
	}
	if next.Task.RunID == nil || *next.Task.RunID != run.ID || next.Task.Status != automation.TaskInProgress {
		t.Fatalf("task not linked to run: %+v", next.Task)
	}

	// Nothing left.
	rec = doJSON(t, s, http.MethodPost, "/api/sessions/auto1/loop-tasks/next", `{}`)
	if rec.Code != http.StatusOK || rec.Body.String() == "" {
		t.Fatalf("second next = %d", rec.Code)
	}
	var empty struct {
		Task *automation.Task `json:"task"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &empty)
	if empty.Task != nil {
		t.Fatalf("expected empty, got %+v", empty.Task)
	}

	rec = doJSON(t, s, http.MethodPost, "/api/sessions/auto1/loop-tasks/add", `{"title":"found"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("agent add = %d", rec.Code)
	}
	var added automation.Task
	_ = json.Unmarshal(rec.Body.Bytes(), &added)
	if added.CreatedBy != automation.CreatedByAgent {
		t.Fatalf("createdBy = %q", added.CreatedBy)
	}

	rec = doJSON(t, s, http.MethodPost, "/api/sessions/auto1/loop-tasks/done",
		`{"taskId":`+strconv.FormatInt(next.Task.ID, 10)+`,"status":"done","result":"fine"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("done = %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, base, "")
	var list []automation.Task
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 2 || list[0].Status != automation.TaskDone || list[1].Status != automation.TaskPending {
		t.Fatalf("unexpected list: %+v", list)
	}

	if rec := doJSON(t, s, http.MethodDelete, base+"/"+strconv.FormatInt(list[1].ID, 10), ""); rec.Code != http.StatusOK {
		t.Fatalf("delete = %d", rec.Code)
	}
}
