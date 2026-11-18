package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/myuon/agmux/internal/automation"
	"github.com/myuon/agmux/internal/session"
)

// noticePrefix marks messages that agmux (not the user) sent to a session.
const noticePrefix = "【agmux 通知】"

// maxPendingNotices caps the per-coordinator buffer; the oldest are dropped.
const maxPendingNotices = 50

func sessionStatusLabel(s string) string {
	switch s {
	case "idle":
		return "待機中"
	case "working":
		return "作業中"
	case "waiting_input":
		return "入力待ち"
	case "paused":
		return "一時停止"
	case "exited":
		return "終了"
	}
	return s
}

func automationStateLabel(s string) string {
	switch s {
	case loopStateRunning:
		return "実行中"
	case loopStateWaiting:
		return "待機中"
	}
	return "停止"
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// formatProjectStatus renders a project's members as agent-readable Japanese
// text. self is the caller's session id (marked in the output).
func formatProjectStatus(d *projectData, pd *projectDetail, self string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "プロジェクト: %s (%s)\n", pd.Name, pd.Path)
	if c := pd.Coordinator; c != nil {
		mark := ""
		if c.ID == self {
			mark = "（あなた）"
		}
		fmt.Fprintf(&b, "コーディネーター: %s (id: %s) [%s]%s\n", c.Name, c.ID, sessionStatusLabel(c.Status), mark)
	} else {
		b.WriteString("コーディネーター: なし\n")
	}

	fmt.Fprintf(&b, "\n## ループ / Automation (%d 件)\n", len(pd.AutomationList))
	if len(pd.AutomationList) == 0 {
		b.WriteString("なし\n")
	}
	for _, a := range pd.AutomationList {
		fmt.Fprintf(&b, "- 「%s」(id: %s) 状態: %s / トリガー: %s", a.Name, a.ID, automationStateLabel(a.State), a.TriggerType)
		if a.TriggerValue != "" {
			fmt.Fprintf(&b, " %s", a.TriggerValue)
		}
		if a.PendingTasks != nil {
			fmt.Fprintf(&b, " / 残タスク: %d 件", *a.PendingTasks)
		}
		b.WriteString("\n")
		if a.State == loopStateStopped && a.StoppedReason != "" {
			fmt.Fprintf(&b, "  停止理由: %s\n", a.StoppedReason)
		}
		if a.LastRunAt != nil {
			fmt.Fprintf(&b, "  最終実行: %s\n", a.LastRunAt.Local().Format("2006-01-02 15:04"))
		}
		if r := a.LatestReport; r != nil {
			fmt.Fprintf(&b, "  直近の loop_report (%s): %s\n", r.CreatedAt.Local().Format("2006-01-02 15:04"), oneLine(r.Summary, 200))
			if len(r.Metrics) > 0 {
				keys := make([]string, 0, len(r.Metrics))
				for k := range r.Metrics {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				parts := make([]string, 0, len(keys))
				for _, k := range keys {
					parts = append(parts, fmt.Sprintf("%s=%g", k, r.Metrics[k]))
				}
				fmt.Fprintf(&b, "  指標: %s\n", strings.Join(parts, ", "))
			}
		}
	}

	var active []projSession
	for _, p := range d.subs {
		if p.AutomationID == "" && isActiveSessionStatus(p.Status) {
			active = append(active, p)
		}
	}
	fmt.Fprintf(&b, "\n## 動いているセッション (%d 件)\n", len(active))
	if len(active) == 0 {
		b.WriteString("なし\n")
	}
	for _, p := range active {
		fmt.Fprintf(&b, "- %s (id: %s) [%s]", p.Name, p.ID, sessionStatusLabel(p.Status))
		if p.Goal != "" {
			fmt.Fprintf(&b, " ゴール: %s", oneLine(p.Goal, 200))
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "\n## 返事待ちの escalate (%d 件)\n", len(pd.Escalations))
	if len(pd.Escalations) == 0 {
		b.WriteString("なし\n")
	}
	for _, e := range pd.Escalations {
		fmt.Fprintf(&b, "- セッション %s: %s\n", e.SessionID, oneLine(e.Message, 200))
	}
	return b.String()
}

// getSessionProjectStatus returns the project status of the session's
// project as text. 404 while the experimental "projects" feature is off.
func (s *Server) getSessionProjectStatus(w http.ResponseWriter, r *http.Request) {
	if projectsDisabled(w) {
		return
	}
	id := chi.URLParam(r, "id")
	m, err := s.loadProjectModel()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sess, ok := m.byID[id]
	if !ok {
		writeError(w, http.StatusNotFound, "session is not in a project: "+id)
		return
	}
	path := m.sessionPath(sess)
	projects := m.projects()
	d, found := projects[path]
	if path == "" || !found {
		writeError(w, http.StatusNotFound, "session is not in a project: "+id)
		return
	}
	s.persistCoordinators(m, projects)
	pd, err := s.buildProject(m, d, true, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"projectPath": path,
		"text":        formatProjectStatus(d, pd, id),
	})
}

// --- notices to the coordinator ---

// sendNoticeToSession delivers text to a session like `agmux session send`.
func (s *Server) sendNoticeToSession(id, text string) error {
	if s.noticeSend != nil {
		return s.noticeSend(id, text)
	}
	if err := s.sessions.SendKeysWithImages(id, text, nil); err != nil {
		return err
	}
	_ = s.sessions.UpdateStatus(id, session.StatusWorking)
	s.recordSessionAction(id, "session_send_keys", text)
	return nil
}

// NoticeAutomation tells the coordinator of the automation's project about an
// automation event (stopped by a safety valve / watchdog, stagnation). It does
// nothing while the experimental "projects" feature is off.
func (s *Server) NoticeAutomation(a automation.Automation, message string) {
	if !projectsEnabled() {
		return
	}
	m, err := s.loadProjectModel()
	if err != nil {
		s.logger.Warn("projects: load model for notice failed", "error", err)
		return
	}
	path := m.automationPath(a)
	if path == "" {
		return
	}
	if coord := m.coordinator[path]; coord != "" {
		s.queueNotice(coord, message)
	}
}

// noticeEscalation sends the coordinator a copy of an escalate raised by a
// session of its project. The coordinator's own escalate is not copied.
func (s *Server) noticeEscalation(sessionID, message string) {
	if !projectsEnabled() {
		return
	}
	m, err := s.loadProjectModel()
	if err != nil {
		s.logger.Warn("projects: load model for notice failed", "error", err)
		return
	}
	sess, ok := m.byID[sessionID]
	if !ok {
		return
	}
	path := m.sessionPath(sess)
	coord := m.coordinator[path]
	if path == "" || coord == "" || coord == sessionID {
		return
	}
	s.queueNotice(coord, fmt.Sprintf(
		"セッション「%s」(id: %s) が escalate しました（写しです。返事は不要です。ユーザーに直接届いています）: %s",
		sess.Name, sess.ID, oneLine(message, 300)))
}

// queueNotice buffers text for the coordinator and sends it right away when
// the coordinator is idle. Otherwise it waits for the idle transition.
// The buffer lives in memory only: it is lost on a server restart.
func (s *Server) queueNotice(coordinatorID, text string) {
	s.noticeMu.Lock()
	if s.notices == nil {
		s.notices = map[string][]string{}
	}
	q := append(s.notices[coordinatorID], text)
	if len(q) > maxPendingNotices {
		q = q[len(q)-maxPendingNotices:]
	}
	s.notices[coordinatorID] = q
	s.noticeMu.Unlock()
	s.flushNotices(coordinatorID)
}

// flushNotices sends everything buffered for the session as one message when
// the session is idle.
func (s *Server) flushNotices(sessionID string) {
	s.noticeMu.Lock()
	has := len(s.notices[sessionID]) > 0
	s.noticeMu.Unlock()
	if !has {
		return
	}
	if !projectsEnabled() {
		s.noticeMu.Lock()
		delete(s.notices, sessionID)
		s.noticeMu.Unlock()
		return
	}
	var status string
	if err := s.sqlDB.QueryRow(`SELECT status FROM sessions WHERE id = ?`, sessionID).Scan(&status); err != nil || status != string(session.StatusIdle) {
		return
	}
	s.noticeMu.Lock()
	items := s.notices[sessionID]
	delete(s.notices, sessionID)
	s.noticeMu.Unlock()
	if len(items) == 0 {
		return
	}
	var msg string
	if len(items) == 1 {
		msg = noticePrefix + items[0]
	} else {
		var b strings.Builder
		fmt.Fprintf(&b, "%s%d 件のお知らせがあります。\n", noticePrefix, len(items))
		for _, it := range items {
			fmt.Fprintf(&b, "- %s\n", it)
		}
		msg = strings.TrimRight(b.String(), "\n")
	}
	if err := s.sendNoticeToSession(sessionID, msg); err != nil {
		s.logger.Warn("projects: send notice to coordinator failed", "sessionId", sessionID, "error", err)
		s.noticeMu.Lock()
		s.notices[sessionID] = append(items, s.notices[sessionID]...)
		s.noticeMu.Unlock()
	}
}
