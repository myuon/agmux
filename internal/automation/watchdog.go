package automation

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/myuon/agmux/internal/experimental"
)

// WatchdogWindow is N: how many recent runs the watchdog looks at.
const WatchdogWindow = 3

// Watchdog condition kinds (keys of the duplicate-suppression map).
const (
	watchFailures  = "failures"
	watchNoReport  = "no_report"
	watchPending   = "pending_growth"
	watchStagnated = "stagnation"
)

// WatchRun is a run as seen by the watchdog.
type WatchRun struct {
	Run
	// Pending is the pending task count at fire time (-1 = unknown).
	Pending   int
	HasReport bool
}

// WatchRuns returns up to limit most recent non-skipped runs (newest first)
// that came after the latest "disabled:" marker, so a re-enabled automation
// starts with a clean window.
func (s *Store) WatchRuns(automationID string, limit int) ([]WatchRun, error) {
	rows, err := s.db.Query(
		`SELECT r.id, r.automation_id, r.fired_at, r.session_id, r.status, r.message, r.created_at,
		        r.pending_at_fire, rep.run_id IS NOT NULL
		 FROM automation_runs r
		 LEFT JOIN automation_run_reports rep ON rep.run_id = r.id
		 WHERE r.automation_id = ? AND r.status != ?
		   AND r.id > (SELECT COALESCE(MAX(id), 0) FROM automation_runs
		               WHERE automation_id = ? AND status = ? AND message LIKE 'disabled:%')
		 ORDER BY r.id DESC LIMIT ?`,
		automationID, string(RunSkipped), automationID, string(RunSkipped), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query watch runs: %w", err)
	}
	defer rows.Close()
	var out []WatchRun
	for rows.Next() {
		var w WatchRun
		var sessionID, message *string
		var status string
		if err := rows.Scan(&w.ID, &w.AutomationID, &w.FiredAt, &sessionID, &status, &message, &w.CreatedAt, &w.Pending, &w.HasReport); err != nil {
			return nil, fmt.Errorf("scan watch run: %w", err)
		}
		if sessionID != nil {
			w.SessionID = *sessionID
		}
		if message != nil {
			w.Message = *message
		}
		w.Status = RunStatus(status)
		out = append(out, w)
	}
	return out, rows.Err()
}

// MetricPoint is one reported value of a metric.
type MetricPoint struct {
	RunID int64
	Value float64
}

// MetricHistory returns the reported values of a metric in chronological
// order, limited to runs after the latest "disabled:" marker.
func (s *Store) MetricHistory(automationID, key string) ([]MetricPoint, error) {
	rows, err := s.db.Query(
		`SELECT r.id, rep.metrics FROM automation_runs r
		 JOIN automation_run_reports rep ON rep.run_id = r.id
		 WHERE r.automation_id = ?
		   AND r.id > (SELECT COALESCE(MAX(id), 0) FROM automation_runs
		               WHERE automation_id = ? AND status = ? AND message LIKE 'disabled:%')
		 ORDER BY r.id ASC`,
		automationID, automationID, string(RunSkipped),
	)
	if err != nil {
		return nil, fmt.Errorf("query metric history: %w", err)
	}
	defer rows.Close()
	var out []MetricPoint
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, fmt.Errorf("scan metric history: %w", err)
		}
		m := map[string]float64{}
		if json.Unmarshal([]byte(raw), &m) != nil {
			continue
		}
		if v, ok := m[key]; ok {
			out = append(out, MetricPoint{RunID: id, Value: v})
		}
	}
	return out, rows.Err()
}

func (s *Store) watchdogNotified(automationID string) (map[string]int64, error) {
	var raw string
	if err := s.db.QueryRow(`SELECT watchdog_notified FROM automations WHERE id = ?`, automationID).Scan(&raw); err != nil {
		return nil, fmt.Errorf("query watchdog state: %w", err)
	}
	m := map[string]int64{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &m)
	}
	return m, nil
}

func (s *Store) setWatchdogNotified(automationID string, m map[string]int64) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE automations SET watchdog_notified = ? WHERE id = ?`, string(b), automationID); err != nil {
		return fmt.Errorf("save watchdog state: %w", err)
	}
	return nil
}

// sessionStopper is implemented by Runner.
type sessionStopper interface {
	SessionStopped(sessionID string) bool
}

// watchdog detects consecutive failures / missing reports / growing backlog /
// stagnation for a loops-enabled automation. It reports whether the
// automation was disabled (the caller must then not fire it). It never runs
// when the "loops" experimental feature is off.
func (s *Scheduler) watchdog(a Automation, now time.Time) bool {
	if !experimental.Enabled(FeatureLoops) {
		return false
	}
	n := WatchdogWindow
	runs, err := s.store.WatchRuns(a.ID, n+1) // newest first
	if err != nil {
		s.logger.Warn("automation watchdog: query runs failed", "automationId", a.ID, "error", err)
		return false
	}
	notified, err := s.store.watchdogNotified(a.ID)
	if err != nil {
		s.logger.Warn("automation watchdog: query state failed", "automationId", a.ID, "error", err)
		return false
	}
	changed := false
	// transition updates the state of a condition and reports whether it has
	// just entered it (and so must be notified). A condition is notified once
	// per state: it stays quiet while active, and is re-armed when it clears.
	// token identifies the state (e.g. the stagnant best value); a different
	// token while active counts as a new state. Presence in the map means active.
	transition := func(kind string, active bool, token int64) bool {
		cur, was := notified[kind]
		if !active {
			if was {
				delete(notified, kind)
				changed = true
			}
			return false
		}
		if was && cur == token {
			return false
		}
		notified[kind] = token
		changed = true
		return true
	}
	defer func() {
		if changed {
			if err := s.store.setWatchdogNotified(a.ID, notified); err != nil {
				s.logger.Warn("automation watchdog: save state failed", "automationId", a.ID, "error", err)
			}
		}
	}()

	disable := func(reason string) bool {
		if err := s.store.SetEnabled(a.ID, false); err != nil {
			s.logger.Warn("automation watchdog: disable failed", "automationId", a.ID, "error", err)
			return false
		}
		if _, err := s.store.InsertRun(Run{AutomationID: a.ID, FiredAt: now, Status: RunSkipped, Message: "disabled: " + reason}); err != nil {
			s.logger.Warn("automation watchdog: record disable reason failed", "automationId", a.ID, "error", err)
		}
		s.logger.Info("automation disabled by watchdog", "automationId", a.ID, "name", a.Name, "reason", reason)
		return true
	}
	lastSession := ""
	for _, r := range runs {
		if r.SessionID != "" {
			lastSession = r.SessionID
			break
		}
	}
	notify := func(msg string) {
		if s.Notify != nil {
			s.Notify(a, lastSession, msg)
		}
	}

	// 1. consecutive failures: disable + notify. Disabling is a one-shot
	// action; the window restarts after re-enabling.
	if len(runs) >= n {
		window := runs[:n]
		allErr := true
		for _, r := range window {
			if r.Status != RunError {
				allErr = false
			}
		}
		if transition(watchFailures, allErr, 1) {
			reason := fmt.Sprintf("%d consecutive errors (last: %s)", n, window[0].Message)
			if disable(reason) {
				notify(fmt.Sprintf("Automation「%s」を無効化しました: %d 回連続で失敗しました（%s）", a.Name, n, window[0].Message))
				return true
			}
		}
	} else {
		transition(watchFailures, false, 0)
	}

	// 2. no report: only for automations used as a loop (queue trigger, or
	// one that has recorded a loop_report at least once).
	noReport := false
	if stopper, ok := s.runner.(sessionStopper); ok && len(runs) >= n {
		isLoop := a.TriggerType == TriggerQueue
		if !isLoop {
			if v, err := s.store.HasReport(a.ID); err != nil {
				s.logger.Warn("automation watchdog: query reports failed", "automationId", a.ID, "error", err)
			} else {
				isLoop = v
			}
		}
		if isLoop {
			noReport = true
			for _, r := range runs[:n] {
				if r.Status != RunSuccess || r.SessionID == "" || r.HasReport || !stopper.SessionStopped(r.SessionID) {
					noReport = false
				}
			}
		}
	}
	if transition(watchNoReport, noReport, 1) {
		if a.TriggerType == TriggerQueue {
			if disable(fmt.Sprintf("%d consecutive runs ended without loop_report", n)) {
				notify(fmt.Sprintf("Automation「%s」を無効化しました: %d 回連続で loop_report が呼ばれずにセッションが終了しました", a.Name, n))
				return true
			}
		} else {
			notify(fmt.Sprintf("Automation「%s」: %d 回連続で loop_report が呼ばれずにセッションが終了しました", a.Name, n))
		}
	}

	// 3. backlog keeps growing: pending at fire strictly increased n times.
	grow := len(runs) == n+1
	if grow {
		for i := 0; i < n; i++ {
			newer, older := runs[i].Pending, runs[i+1].Pending
			if newer < 0 || older < 0 || newer <= older {
				grow = false
			}
		}
	}
	if transition(watchPending, grow, 1) {
		notify(fmt.Sprintf("Automation「%s」: 残タスクが %d 回連続で増え続けています（%d 件）", a.Name, n, runs[0].Pending))
	}

	// 4. stagnation of the watched metric: once per (metric, best value).
	stagnated, best, latest, worse := false, 0.0, 0.0, false
	if a.WatchMetric != "" {
		pts, err := s.store.MetricHistory(a.ID, a.WatchMetric)
		if err != nil {
			s.logger.Warn("automation watchdog: metric history failed", "automationId", a.ID, "error", err)
			return false
		}
		if len(pts) > n {
			better := func(x, than float64) bool {
				if a.WatchDirection == WatchLower {
					return x < than
				}
				return x > than
			}
			best = pts[0].Value
			for _, p := range pts[1 : len(pts)-n] {
				if better(p.Value, best) {
					best = p.Value
				}
			}
			improved := false
			for _, p := range pts[len(pts)-n:] {
				if better(p.Value, best) {
					improved = true
				}
			}
			stagnated = !improved
			latest = pts[len(pts)-1].Value
			worse = better(best, latest)
			// A latest value that has reached the target cannot improve
			// further, so it is not stagnation.
			if a.WatchTarget != nil && WatchTargetReached(a.WatchDirection, *a.WatchTarget, pts[len(pts)-1].Value) {
				stagnated = false
			}
		}
	}
	token := int64(math.Float64bits(best))
	if transition(watchStagnated, stagnated, token) {
		if worse {
			notify(fmt.Sprintf("Automation「%s」: 指標 %s が悪化しています（それまでの最良 %g → 最新 %g、直近 %d 回で最良を更新していません）", a.Name, a.WatchMetric, best, latest, n))
		} else {
			notify(fmt.Sprintf("Automation「%s」: 指標 %s が直近 %d 回で最良値 %g を更新していません（最新 %g）", a.Name, a.WatchMetric, n, best, latest))
		}
	}
	return false
}

// HasReport reports whether the automation has ever recorded a loop_report.
func (s *Store) HasReport(automationID string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM automation_runs r JOIN automation_run_reports rep ON rep.run_id = r.id
		 WHERE r.automation_id = ?`, automationID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("query reports: %w", err)
	}
	return n > 0, nil
}
