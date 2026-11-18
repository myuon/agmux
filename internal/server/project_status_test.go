package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/myuon/agmux/internal/automation"
	"github.com/myuon/agmux/internal/experimental"
)

type sentNotice struct{ id, text string }

func captureNotices(s *Server) *[]sentNotice {
	var mu sync.Mutex
	got := &[]sentNotice{}
	s.noticeSend = func(id, text string) error {
		mu.Lock()
		defer mu.Unlock()
		*got = append(*got, sentNotice{id, text})
		return nil
	}
	return got
}

func setStatus(t *testing.T, s *Server, id, status string) {
	t.Helper()
	if _, err := s.sqlDB.Exec(`UPDATE sessions SET status = ? WHERE id = ?`, status, id); err != nil {
		t.Fatal(err)
	}
}

func TestProjectStatus_Text(t *testing.T) {
	s := newProjectsTestServer(t, true)
	p := "/work/repo"
	addSess(t, s, "main", p, 70, nil)
	addSess(t, s, "w1", p, 60, map[string]string{"status": "working"})
	touchSess(t, s, "main", 0)
	if _, err := s.sqlDB.Exec(`UPDATE sessions SET goal = 'テストを直す' WHERE id = 'w1'`); err != nil {
		t.Fatal(err)
	}
	a, err := s.automations.Create(automation.CreateParams{
		Name: "nightly", Prompt: "p", TriggerType: automation.TriggerInterval, TriggerValue: "1h",
		ProjectPath: p, Enabled: true, CreatedBySessionID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	addSess(t, s, "run1", p, 20, map[string]string{"automation": a.ID, "status": "idle"})
	res, err := s.sqlDB.Exec(`INSERT INTO automation_runs (automation_id, fired_at, session_id, status) VALUES (?, datetime('now','-1 minutes'), 'run1', 'success')`, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := res.LastInsertId()
	if _, err := s.sqlDB.Exec(`INSERT INTO automation_run_reports (run_id, metrics, summary, links, created_at) VALUES (?, '{"pass_rate":0.82}', 'ok した', '[]', datetime('now'))`, runID); err != nil {
		t.Fatal(err)
	}
	s.escalations.Create("e1", "w1", "どちらにしますか", 60)

	// Callable from a non-coordinator session of the same project.
	rec := doJSON(t, s, http.MethodGet, "/api/sessions/w1/project-status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d: %s", rec.Code, rec.Body.String())
	}
	var out struct{ Text string }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"プロジェクト: repo (/work/repo)",
		"コーディネーター: name-main (id: main) [待機中]",
		"「nightly」(id: " + a.ID + ") 状態: 待機中 / トリガー: interval 1h / 残タスク: 0 件",
		"指標: pass_rate=0.82",
		"ok した",
		"name-w1 (id: w1) [作業中] ゴール: テストを直す",
		"## 返事待ちの escalate (1 件)",
		"セッション w1: どちらにしますか",
	} {
		if !strings.Contains(out.Text, want) {
			t.Errorf("missing %q in:\n%s", want, out.Text)
		}
	}
	// The coordinator sees itself marked.
	rec = doJSON(t, s, http.MethodGet, "/api/sessions/main/project-status", "")
	if !strings.Contains(rec.Body.String(), "あなた") {
		t.Errorf("coordinator not marked: %s", rec.Body.String())
	}
}

func TestProjectStatus_DisabledAndUnknown(t *testing.T) {
	s := newProjectsTestServer(t, false)
	if rec := doJSON(t, s, http.MethodGet, "/api/sessions/nope/project-status", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown session = %d", rec.Code)
	}
	if err := experimental.Set("projects", false); err != nil {
		t.Fatal(err)
	}
	addSess(t, s, "main", "/work/repo", 10, nil)
	if rec := doJSON(t, s, http.MethodGet, "/api/sessions/main/project-status", ""); rec.Code != http.StatusNotFound {
		t.Errorf("projects off = %d", rec.Code)
	}
}

func TestProjectNotices_AutomationStopGoesToCoordinator(t *testing.T) {
	s := newProjectsTestServer(t, true)
	got := captureNotices(s)
	p := "/work/repo"
	addSess(t, s, "main", p, 70, nil)
	a, err := s.automations.Create(automation.CreateParams{
		Name: "nightly", Prompt: "p", TriggerType: automation.TriggerInterval, TriggerValue: "1h",
		ProjectPath: p, Enabled: true, CreatedBySessionID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	s.NoticeAutomation(*a, "Automation「nightly」を停止しました")
	if len(*got) != 1 || (*got)[0].id != "main" || (*got)[0].text != noticePrefix+"Automation「nightly」を停止しました" {
		t.Fatalf("got %+v", *got)
	}
}

func TestProjectNotices_BufferedWhileBusyThenOneMessage(t *testing.T) {
	s := newProjectsTestServer(t, true)
	got := captureNotices(s)
	p := "/work/repo"
	addSess(t, s, "main", p, 70, map[string]string{"status": "working"})
	addSess(t, s, "w1", p, 60, nil)
	touchSess(t, s, "main", 0)
	a, err := s.automations.Create(automation.CreateParams{
		Name: "nightly", Prompt: "p", TriggerType: automation.TriggerInterval, TriggerValue: "1h",
		ProjectPath: p, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	s.NoticeAutomation(*a, "停滞を検知しました")
	s.noticeEscalation("w1", "確認してください")
	setStatus(t, s, "main", "waiting_input")
	s.NoticeAutomation(*a, "停止しました")
	if len(*got) != 0 {
		t.Fatalf("sent while busy: %+v", *got)
	}
	setStatus(t, s, "main", "idle")
	s.flushNotices("main")
	if len(*got) != 1 {
		t.Fatalf("want 1 combined message, got %+v", *got)
	}
	text := (*got)[0].text
	for _, want := range []string{noticePrefix, "3 件", "停滞を検知しました", "停止しました", "返事は不要", "確認してください"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %q", want, text)
		}
	}
	s.flushNotices("main")
	if len(*got) != 1 {
		t.Errorf("sent twice: %+v", *got)
	}
}

func TestProjectNotices_EscalationFromSubAndNotFromCoordinator(t *testing.T) {
	s := newProjectsTestServer(t, false)
	got := captureNotices(s)
	p := "/work/repo"
	addSess(t, s, "main", p, 70, nil)
	addSess(t, s, "w1", p, 60, map[string]string{"status": "working"})
	touchSess(t, s, "main", 0)
	addSess(t, s, "other", "/work/other", 50, nil)

	s.noticeEscalation("main", "自分の escalate")
	if len(*got) != 0 {
		t.Fatalf("coordinator's own escalate was copied: %+v", *got)
	}
	s.noticeEscalation("w1", "どうしますか")
	if len(*got) != 1 || (*got)[0].id != "main" ||
		!strings.Contains((*got)[0].text, "返事は不要") || !strings.Contains((*got)[0].text, "どうしますか") {
		t.Fatalf("got %+v", *got)
	}
	// The other project's own coordinator is itself, so nothing is sent for it.
	s.noticeEscalation("other", "x")
	if len(*got) != 1 {
		t.Errorf("leaked: %+v", *got)
	}
}

func TestProjectNotices_OffSendsNothing(t *testing.T) {
	s := newProjectsTestServer(t, true)
	got := captureNotices(s)
	p := "/work/repo"
	addSess(t, s, "main", p, 70, nil)
	addSess(t, s, "w1", p, 60, nil)
	touchSess(t, s, "main", 0)
	a, err := s.automations.Create(automation.CreateParams{
		Name: "n", Prompt: "p", TriggerType: automation.TriggerInterval, TriggerValue: "1h", ProjectPath: p, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := experimental.Set("projects", false); err != nil {
		t.Fatal(err)
	}
	s.NoticeAutomation(*a, "停止")
	s.noticeEscalation("w1", "x")
	s.flushNotices("main")
	if len(*got) != 0 {
		t.Fatalf("sent while off: %+v", *got)
	}
}
