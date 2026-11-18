package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/myuon/agmux/internal/automation"
	"github.com/myuon/agmux/internal/experimental"
)

func newProjectsTestServer(t *testing.T, loops bool) *Server {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	s := newAutomationTestServer(t)
	s.escalations = NewEscalationStore()
	if err := experimental.Set("projects", true); err != nil {
		t.Fatal(err)
	}
	if loops {
		if err := experimental.Set("loops", true); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// addSess inserts a session; ageMin orders creation (larger = older).
func addSess(t *testing.T, s *Server, id, path string, ageMin int, extra map[string]string) {
	t.Helper()
	typ, status, auto, by := "worker", "idle", "", ""
	if v, ok := extra["type"]; ok {
		typ = v
	}
	if v, ok := extra["status"]; ok {
		status = v
	}
	if v, ok := extra["automation"]; ok {
		auto = v
	}
	if v, ok := extra["by"]; ok {
		by = v
	}
	ts := time.Now().Add(-time.Duration(ageMin) * time.Minute)
	_, err := s.sqlDB.Exec(
		`INSERT INTO sessions (id, name, project_path, type, status, automation_id, created_by_session_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "name-"+id, path, typ, status, auto, by, ts, ts)
	if err != nil {
		t.Fatal(err)
	}
}

// touchSess sets a session's updated_at to ageMin minutes ago (recency drives
// the automatic coordinator choice).
func touchSess(t *testing.T, s *Server, id string, ageMin int) {
	t.Helper()
	ts := time.Now().Add(-time.Duration(ageMin) * time.Minute)
	if _, err := s.sqlDB.Exec(`UPDATE sessions SET updated_at = ? WHERE id = ?`, ts, id); err != nil {
		t.Fatal(err)
	}
}

func projURL(path string) string { return "/api/projects/" + url.PathEscape(path) }

func getProjectDetail(t *testing.T, s *Server, path string) projectDetail {
	t.Helper()
	rec := doJSON(t, s, http.MethodGet, projURL(path), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, rec.Code, rec.Body.String())
	}
	var d projectDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func coordID(d projectDetail) string {
	if d.Coordinator == nil {
		return ""
	}
	return d.Coordinator.ID
}

func TestProjects_DisabledReturns404(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newAutomationTestServer(t)
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/projects", ""},
		{http.MethodGet, projURL("/tmp/a"), ""},
		{http.MethodPut, projURL("/tmp/a") + "/coordinator", `{"sessionId":"x"}`},
	} {
		if rec := doJSON(t, s, c.method, c.path, c.body); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", c.method, c.path, rec.Code)
		}
	}
}

func TestProjects_CoordinatorAutoDecision(t *testing.T) {
	s := newProjectsTestServer(t, false)
	p := "/work/repo"
	addSess(t, s, "ctl", p, 1, map[string]string{"type": "controller"})
	addSess(t, s, "run1", p, 1, map[string]string{"automation": "auto-x"})
	addSess(t, s, "eph", p, 1, map[string]string{"type": "ephemeral"})
	addSess(t, s, "main", p, 70, nil)
	addSess(t, s, "second", p, 60, nil)
	addSess(t, s, "third", p, 50, nil)

	// The most recently active regular session wins (not the oldest).
	d := getProjectDetail(t, s, p)
	if coordID(d) != "third" {
		t.Fatalf("coordinator = %q, want third", coordID(d))
	}
	if d.Name != "repo" || d.Path != p {
		t.Errorf("name/path = %q %q", d.Name, d.Path)
	}
	// Coordinator and controller are not in the sessions list.
	ids := map[string]bool{}
	for _, x := range d.Sessions {
		ids[x.ID] = true
	}
	if ids["third"] || ids["ctl"] || !ids["second"] || !ids["main"] || !ids["eph"] {
		t.Errorf("sessions = %v", ids)
	}

	// Activity moves the coordinator (recomputed, not sticky).
	touchSess(t, s, "main", 0)
	if got := coordID(getProjectDetail(t, s, p)); got != "main" {
		t.Fatalf("after activity coordinator = %q, want main", got)
	}
	// Archiving it moves on to the next most recent.
	if _, err := s.sqlDB.Exec(`UPDATE sessions SET status='archived' WHERE id='main'`); err != nil {
		t.Fatal(err)
	}
	if got := coordID(getProjectDetail(t, s, p)); got != "third" {
		t.Fatalf("after archive coordinator = %q, want third", got)
	}
}

// An exited session is only chosen when nothing else is eligible, even if it
// is the most recently updated one; a long-dead exited session never wins.
func TestProjects_CoordinatorPrefersLiveOverExited(t *testing.T) {
	s := newProjectsTestServer(t, false)
	p := "/work/repo"
	addSess(t, s, "c3coo", p, 128*24*60, map[string]string{"status": "exited"})
	addSess(t, s, "live", p, 30, nil)
	addSess(t, s, "recent-exited", p, 1, map[string]string{"status": "exited"})
	if got := coordID(getProjectDetail(t, s, p)); got != "live" {
		t.Fatalf("coordinator = %q, want live", got)
	}
	if _, err := s.sqlDB.Exec(`UPDATE sessions SET status='archived' WHERE id='live'`); err != nil {
		t.Fatal(err)
	}
	if got := coordID(getProjectDetail(t, s, p)); got != "recent-exited" {
		t.Fatalf("only exited left: coordinator = %q, want recent-exited", got)
	}
}

// Sub-sessions (created by another session, or learning apply sessions) are
// not coordinator candidates unless nothing else exists.
func TestProjects_CoordinatorSkipsSubSessions(t *testing.T) {
	s := newProjectsTestServer(t, false)
	p := "/work/repo"
	addSess(t, s, "human", p, 60, nil)
	addSess(t, s, "kid", p, 1, map[string]string{"by": "human"})
	addSess(t, s, "apply", p, 1, nil)
	if _, err := s.sqlDB.Exec(`UPDATE sessions SET name = '学びの反映: 何か' WHERE id = 'apply'`); err != nil {
		t.Fatal(err)
	}
	if got := coordID(getProjectDetail(t, s, p)); got != "human" {
		t.Fatalf("coordinator = %q, want human", got)
	}
	// Only sub-sessions left: the most recent one is used as a last resort.
	if _, err := s.sqlDB.Exec(`UPDATE sessions SET status='archived' WHERE id='human'`); err != nil {
		t.Fatal(err)
	}
	if got := coordID(getProjectDetail(t, s, p)); got == "" {
		t.Fatal("expected a last-resort coordinator")
	}
}

// Archived members (e.g. ephemeral sessions) are grouped under the project
// instead of being dropped, and do not count as active or bump the time.
func TestProjects_ArchivedMembersGrouped(t *testing.T) {
	s := newProjectsTestServer(t, false)
	p := "/work/repo"
	addSess(t, s, "main", p, 10, nil)
	addSess(t, s, "eph1", p, 5, map[string]string{"type": "ephemeral", "status": "archived"})
	addSess(t, s, "eph2", p, 4, map[string]string{"type": "ephemeral", "status": "archived"})
	d := getProjectDetail(t, s, p)
	if len(d.ArchivedSessions) != 2 || len(d.Sessions) != 0 {
		t.Fatalf("archived=%d sessions=%d", len(d.ArchivedSessions), len(d.Sessions))
	}
	// A project made only of archived sessions does not appear.
	addSess(t, s, "lonely", "/work/other", 3, map[string]string{"status": "archived"})
	if rec := doJSON(t, s, http.MethodGet, projURL("/work/other"), ""); rec.Code != http.StatusNotFound {
		t.Errorf("archived-only project = %d, want 404", rec.Code)
	}
}

// The list is ordered by the project-wide last update, not the coordinator's.
func TestProjects_ListOrderUsesProjectWideUpdate(t *testing.T) {
	s := newProjectsTestServer(t, false)
	// "old" has a stale coordinator but a recently active sub-session.
	addSess(t, s, "oldc", "/work/old", 1000, nil)
	addSess(t, s, "oldsub", "/work/old", 1000, map[string]string{"type": "ephemeral"})
	touchSess(t, s, "oldsub", 1)
	addSess(t, s, "midc", "/work/mid", 60, nil)
	rec := doJSON(t, s, http.MethodGet, "/api/projects", "")
	var list []projectSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Path != "/work/old" {
		t.Fatalf("order = %+v", list)
	}
}

func TestProjects_SetCoordinator(t *testing.T) {
	s := newProjectsTestServer(t, false)
	p := "/work/repo"
	addSess(t, s, "main", p, 70, nil)
	addSess(t, s, "other", p, 60, nil)
	addSess(t, s, "run1", p, 50, map[string]string{"automation": "a"})
	addSess(t, s, "foreign", "/work/else", 40, nil)

	put := func(id string) int {
		return doJSON(t, s, http.MethodPut, projURL(p)+"/coordinator", `{"sessionId":"`+id+`"}`).Code
	}
	if c := put("other"); c != http.StatusOK {
		t.Fatalf("put other = %d", c)
	}
	if got := coordID(getProjectDetail(t, s, p)); got != "other" {
		t.Fatalf("coordinator = %q, want other", got)
	}
	if c := put("run1"); c != http.StatusBadRequest {
		t.Errorf("automation session = %d, want 400", c)
	}
	if c := put("foreign"); c != http.StatusBadRequest {
		t.Errorf("other project's session = %d, want 400", c)
	}
	if c := put("nope"); c != http.StatusNotFound {
		t.Errorf("missing session = %d, want 404", c)
	}
	if rec := doJSON(t, s, http.MethodPut, projURL("/work/none")+"/coordinator", `{"sessionId":"main"}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project = %d, want 404", rec.Code)
	}
	// The explicit choice beats a more recently active session.
	touchSess(t, s, "main", 0)
	if got := coordID(getProjectDetail(t, s, p)); got != "other" {
		t.Fatalf("explicit coordinator = %q, want other", got)
	}
	// Archiving the manually chosen coordinator falls back to the automatic pick.
	if _, err := s.sqlDB.Exec(`UPDATE sessions SET status='archived' WHERE id='other'`); err != nil {
		t.Fatal(err)
	}
	if got := coordID(getProjectDetail(t, s, p)); got != "main" {
		t.Fatalf("coordinator after archive = %q, want main", got)
	}
}

func TestProjects_CreatedByPlacesItemsUnderCoordinator(t *testing.T) {
	s := newProjectsTestServer(t, true)
	home, lab := "/work/income-harness", "/work/crypto-lab"
	addSess(t, s, "coordA", home, 100, nil)
	addSess(t, s, "coordB", lab, 99, nil)
	// A session coordA created in another repository.
	addSess(t, s, "child", lab, 50, map[string]string{"by": "coordA"})
	// A session in lab created by a non-coordinator stays in lab.
	addSess(t, s, "sub", home, 40, nil)
	addSess(t, s, "labchild", lab, 30, map[string]string{"by": "sub"})
	// coordA / coordB are the most recently active in their projects.
	touchSess(t, s, "coordA", 0)
	touchSess(t, s, "coordB", 0)

	// A loop coordA created in lab, and its run session.
	loop, err := s.automations.Create(automation.CreateParams{
		Name: "lab-loop", Prompt: "p", TriggerType: automation.TriggerInterval, TriggerValue: "1h",
		ProjectPath: lab, Enabled: true, CreatedBySessionID: "coordA"})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := s.automations.Create(automation.CreateParams{
		Name: "lab-plain", Prompt: "p", TriggerType: automation.TriggerInterval, TriggerValue: "1h",
		ProjectPath: lab, Enabled: true, CreatedBySessionID: "sub"})
	if err != nil {
		t.Fatal(err)
	}
	addSess(t, s, "run1", lab, 20, map[string]string{"automation": loop.ID, "status": "working"})
	if _, err := s.sqlDB.Exec(`INSERT INTO automation_runs (automation_id, fired_at, session_id, status) VALUES (?, datetime('now','-1 minutes'), 'run1', 'success')`, loop.ID); err != nil {
		t.Fatal(err)
	}

	a := getProjectDetail(t, s, home)
	if coordID(a) != "coordA" {
		t.Fatalf("home coordinator = %q", coordID(a))
	}
	var sessIDs []string
	for _, x := range a.Sessions {
		sessIDs = append(sessIDs, x.ID)
	}
	if !strings.Contains(strings.Join(sessIDs, ","), "child") {
		t.Errorf("home sessions %v should include child", sessIDs)
	}
	if len(a.AutomationList) != 1 || a.AutomationList[0].ID != loop.ID {
		t.Fatalf("home automations = %+v", a.AutomationList)
	}
	if a.Automations.Running != 1 {
		t.Errorf("home automation counts = %+v, want running 1", a.Automations)
	}
	// The loop's run session (working) is under home too.
	if a.ActiveSessions != 1 {
		t.Errorf("home activeSessions = %d, want 1", a.ActiveSessions)
	}

	b := getProjectDetail(t, s, lab)
	if coordID(b) != "coordB" {
		t.Fatalf("lab coordinator = %q", coordID(b))
	}
	if len(b.AutomationList) != 1 || b.AutomationList[0].ID != plain.ID {
		t.Fatalf("lab automations = %+v", b.AutomationList)
	}
	for _, x := range b.Sessions {
		if x.ID == "child" {
			t.Error("child should not be in lab")
		}
	}
}

func TestProjects_ListSummaryAndEscalations(t *testing.T) {
	s := newProjectsTestServer(t, false)
	p := "/work/repo"
	addSess(t, s, "main", p, 70, nil)
	addSess(t, s, "w1", p, 60, map[string]string{"status": "working"})
	addSess(t, s, "w2", p, 50, map[string]string{"status": "waiting_input"})
	addSess(t, s, "w3", p, 40, nil)
	touchSess(t, s, "main", 0)
	s.escalations.Create("e1", "w1", "need input", 60)
	s.escalations.Create("e2", "main", "question", 60)
	s.escalations.Respond("e2", "ok")
	s.escalations.Create("e3", "elsewhere", "x", 60)

	rec := doJSON(t, s, http.MethodGet, "/api/projects", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d: %s", rec.Code, rec.Body.String())
	}
	var list []projectSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("list = %+v", list)
	}
	got := list[0]
	if got.Coordinator == nil || got.Coordinator.ID != "main" || got.ActiveSessions != 2 || got.PendingEscalations != 1 || got.LastUpdatedAt == nil {
		t.Errorf("summary = %+v", got)
	}
	d := getProjectDetail(t, s, p)
	if len(d.Escalations) != 1 || d.Escalations[0].ID != "e1" {
		t.Errorf("escalations = %+v", d.Escalations)
	}
}

func TestProjects_LoopFieldsOnlyWithLoops(t *testing.T) {
	for _, loops := range []bool{false, true} {
		s := newProjectsTestServer(t, loops)
		p := "/work/repo"
		addSess(t, s, "main", p, 70, nil)
		a, err := s.automations.Create(automation.CreateParams{
			Name: "daily", Prompt: "p", TriggerType: automation.TriggerInterval, TriggerValue: "1h", ProjectPath: p, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		addSess(t, s, "r1", p, 10, map[string]string{"automation": a.ID})
		res, err := s.sqlDB.Exec(`INSERT INTO automation_runs (automation_id, fired_at, session_id, status) VALUES (?, datetime('now','-1 minutes'), 'r1', 'success')`, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		runID, _ := res.LastInsertId()
		if _, err := s.automations.SaveReport(runID, map[string]float64{"score": 1}, "done", nil); err != nil {
			t.Fatal(err)
		}
		d := getProjectDetail(t, s, p)
		if len(d.AutomationList) != 1 {
			t.Fatalf("automations = %+v", d.AutomationList)
		}
		x := d.AutomationList[0]
		if x.State != "waiting" || x.LastRunAt == nil {
			t.Errorf("loops=%v state = %+v", loops, x)
		}
		if loops {
			if x.PendingTasks == nil || x.LatestReport == nil || x.LatestReport.Summary != "done" {
				t.Errorf("loops on: %+v", x)
			}
		} else if x.PendingTasks != nil || x.LatestReport != nil {
			t.Errorf("loops off must omit loop fields: %+v", x)
		}
	}
}

func TestAutomationCreate_RecordsCreatedBySessionID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newAutomationTestServer(t)
	rec := doJSON(t, s, http.MethodPost, "/api/automations",
		`{"name":"n","prompt":"p","triggerType":"interval","triggerValue":"1h","enabled":true,"createdBySessionId":"coord-1"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("code %d: %s", rec.Code, rec.Body.String())
	}
	var a automation.Automation
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	got, err := s.automations.Get(a.ID)
	if err != nil || got.CreatedBySessionID != "coord-1" {
		t.Fatalf("stored = %+v, %v", got, err)
	}
}

func TestProjects_ListPerformance(t *testing.T) {
	s := newProjectsTestServer(t, true)
	for i := 0; i < 30; i++ {
		p := fmt.Sprintf("/work/repo%d", i%5)
		if _, err := s.automations.Create(automation.CreateParams{
			Name: "a", Prompt: "p", TriggerType: automation.TriggerInterval, TriggerValue: "1h", ProjectPath: p, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := s.sqlDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 500; i++ {
		if _, err := tx.Exec(`INSERT INTO sessions (id, name, project_path, type, status, created_at, updated_at) VALUES (?, 'n', ?, 'worker', 'idle', datetime('now', ?), datetime('now'))`,
			fmt.Sprintf("s%d", i), fmt.Sprintf("/work/repo%d", i%5), fmt.Sprintf("-%d minutes", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	rec := doJSON(t, s, http.MethodGet, "/api/projects", "")
	elapsed := time.Since(start)
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	t.Logf("GET /api/projects with 500 sessions / 30 automations: %v", elapsed)
	if elapsed > 500*time.Millisecond {
		t.Errorf("too slow: %v", elapsed)
	}
}
