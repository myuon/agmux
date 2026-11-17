package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/myuon/agmux/internal/automation"
	"github.com/myuon/agmux/internal/experimental"
)

func TestAutomationSummaries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newAutomationTestServer(t)

	if rec := doJSON(t, s, http.MethodGet, "/api/automations/summary", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("loops off: code = %d, want 404", rec.Code)
	}
	if err := experimental.Set("loops", true); err != nil {
		t.Fatal(err)
	}

	mk := func(name string, tt automation.TriggerType, tv string) *automation.Automation {
		a, err := s.automations.Create(automation.CreateParams{Name: name, Prompt: "p", TriggerType: tt, TriggerValue: tv, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	scanner := mk("scanner", automation.TriggerInterval, "1h")
	fixer := mk("fixer", automation.TriggerQueue, "")
	stopped := mk("stopped", automation.TriggerInterval, "1h")
	for _, q := range []string{
		`INSERT INTO sessions (id, name, project_path, automation_id, status) VALUES ('scan1', 'scan #1', '/tmp', '` + scanner.ID + `', 'idle')`,
		`INSERT INTO sessions (id, name, project_path, automation_id, status) VALUES ('fix1', 'fix #1', '/tmp', '` + fixer.ID + `', 'working')`,
		`INSERT INTO automation_runs (automation_id, fired_at, session_id, status) VALUES ('` + scanner.ID + `', datetime('now','-10 minutes'), 'scan1', 'success')`,
		`INSERT INTO automation_runs (automation_id, fired_at, session_id, status) VALUES ('` + fixer.ID + `', datetime('now','-5 minutes'), 'fix1', 'success')`,
		`INSERT INTO automation_runs (automation_id, fired_at, status, message) VALUES ('` + stopped.ID + `', datetime('now','-1 minutes'), 'skipped', 'disabled: reached max runs (3)')`,
	} {
		if _, err := s.sqlDB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.automations.SetEnabled(stopped.ID, false); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"a", "b"} {
		if _, err := s.automations.AddTaskFrom(fixer.ID, title, "", "agent", scanner.ID); err != nil {
			t.Fatal(err)
		}
	}

	rec := doJSON(t, s, http.MethodGet, "/api/automations/summary", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d: %s", rec.Code, rec.Body.String())
	}
	var list []automationSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	by := map[string]automationSummary{}
	for _, a := range list {
		by[a.ID] = a
	}
	sc := by[scanner.ID]
	if sc.State != "waiting" || sc.NextFireAt == nil || sc.LastRunAt == nil || len(sc.RecentRuns) != 1 || sc.RecentRuns[0].SessionID != "scan1" {
		t.Fatalf("scanner: %+v", sc)
	}
	if len(sc.Feeds) != 1 || sc.Feeds[0].ID != fixer.ID {
		t.Fatalf("scanner feeds: %+v", sc.Feeds)
	}
	fx := by[fixer.ID]
	if fx.State != "running" || fx.PendingTasks != 2 || fx.NextFireAt != nil || fx.NextReason != "前回終了待ち" {
		t.Fatalf("fixer: %+v", fx)
	}
	if len(fx.Feeders) != 1 || fx.Feeders[0].ID != scanner.ID {
		t.Fatalf("fixer feeders: %+v", fx.Feeders)
	}
	st := by[stopped.ID]
	if st.State != "stopped" || st.StoppedReason != "disabled: reached max runs (3)" || st.NextFireAt != nil || st.RecentRuns == nil {
		t.Fatalf("stopped: %+v", st)
	}
}
