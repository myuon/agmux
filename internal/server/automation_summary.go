package server

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/myuon/agmux/internal/automation"
	"github.com/myuon/agmux/internal/experimental"
)

// summaryRecentRuns is how many recent session-creating runs a summary lists.
const summaryRecentRuns = 5

// Loop states shown in the session list.
const (
	loopStateRunning = "running"
	loopStateWaiting = "waiting"
	loopStateStopped = "stopped"
)

// automationRunSummary is one recent run (one "turn" of the loop).
type automationRunSummary struct {
	RunID         int64  `json:"runId"`
	SessionID     string `json:"sessionId"`
	SessionName   string `json:"sessionName"`
	SessionStatus string `json:"sessionStatus"`
	// SessionUnread: the run's session finished a turn the user has not seen (#859).
	SessionUnread bool      `json:"sessionUnread"`
	FiredAt       time.Time `json:"firedAt"`
}

// automationSummary is the per-automation row of the session list
// (experimental "loops").
type automationSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Enabled     bool   `json:"enabled"`
	TriggerType string `json:"triggerType"`
	// State is running (latest run's session is working / waiting_input),
	// waiting, or stopped (disabled).
	State string `json:"state"`
	// StoppedReason is the latest "disabled:" run message (stopped only).
	StoppedReason string `json:"stoppedReason,omitempty"`
	PendingTasks  int    `json:"pendingTasks"`
	// NextFireAt is set for interval / cron (and queue once only the wait
	// window remains). Otherwise NextReason says what is awaited.
	NextFireAt *time.Time `json:"nextFireAt"`
	NextReason string     `json:"nextReason,omitempty"`
	// LastRunAt is when the latest session-creating run fired.
	LastRunAt  *time.Time             `json:"lastRunAt"`
	CreatedAt  time.Time              `json:"createdAt"`
	RecentRuns []automationRunSummary `json:"recentRuns"`
	Feeders    []automation.Ref       `json:"feeders"`
	Feeds      []automation.Ref       `json:"feeds"`
}

func isActiveSessionStatus(status string) bool {
	return status == "working" || status == "waiting_input"
}

// listAutomationSummaries returns what the session list needs to render one
// row per automation. 404 while the experimental "loops" feature is off.
func (s *Server) listAutomationSummaries(w http.ResponseWriter, r *http.Request) {
	if !experimental.Enabled(automation.FeatureLoops) {
		writeError(w, http.StatusNotFound, "experimental feature \""+automation.FeatureLoops+"\" is disabled")
		return
	}
	automations, err := s.automations.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	feeders, feeds, err := s.automations.Relations()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now()
	out := make([]automationSummary, 0, len(automations))
	for _, a := range automations {
		sum, err := s.buildAutomationSummary(a, now)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		sum.Feeders, sum.Feeds = feeders[a.ID], feeds[a.ID]
		if sum.Feeders == nil {
			sum.Feeders = []automation.Ref{}
		}
		if sum.Feeds == nil {
			sum.Feeds = []automation.Ref{}
		}
		out = append(out, sum)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) buildAutomationSummary(a automation.Automation, now time.Time) (automationSummary, error) {
	sum := automationSummary{
		ID: a.ID, Name: a.Name, Enabled: a.Enabled, TriggerType: string(a.TriggerType),
		CreatedAt: a.CreatedAt, RecentRuns: []automationRunSummary{},
	}
	pending, err := s.automations.CountPendingTasks(a.ID)
	if err != nil {
		return sum, err
	}
	sum.PendingTasks = pending

	rows, err := s.sqlDB.Query(
		`SELECT r.id, r.session_id, COALESCE(s.name, ''), COALESCE(s.status, ''), COALESCE(s.last_turn_completed_at > s.last_read_at, 0), r.fired_at, s.updated_at
		 FROM automation_runs r LEFT JOIN sessions s ON s.id = r.session_id
		 WHERE r.automation_id = ? AND r.session_id IS NOT NULL AND r.session_id != ''
		 ORDER BY r.fired_at DESC, r.id DESC LIMIT ?`, a.ID, summaryRecentRuns)
	if err != nil {
		return sum, err
	}
	defer rows.Close()
	var latestUpdated time.Time
	for rows.Next() {
		var rs automationRunSummary
		var updated sql.NullTime
		if err := rows.Scan(&rs.RunID, &rs.SessionID, &rs.SessionName, &rs.SessionStatus, &rs.SessionUnread, &rs.FiredAt, &updated); err != nil {
			return sum, err
		}
		if len(sum.RecentRuns) == 0 {
			t := rs.FiredAt
			sum.LastRunAt = &t
			latestUpdated = updated.Time
		}
		sum.RecentRuns = append(sum.RecentRuns, rs)
	}
	if err := rows.Err(); err != nil {
		return sum, err
	}
	active := len(sum.RecentRuns) > 0 && isActiveSessionStatus(sum.RecentRuns[0].SessionStatus)

	switch {
	case !a.Enabled:
		sum.State = loopStateStopped
		sum.StoppedReason = s.latestDisabledReason(a.ID)
	case active:
		sum.State = loopStateRunning
	default:
		sum.State = loopStateWaiting
	}

	if a.Enabled {
		s.fillNextFire(&sum, a, pending, active, latestUpdated, now)
	}
	return sum, nil
}

// fillNextFire sets NextFireAt / NextReason, mirroring the scheduler.
func (s *Server) fillNextFire(sum *automationSummary, a automation.Automation, pending int, active bool, latestUpdated, now time.Time) {
	if a.TriggerType == automation.TriggerQueue {
		switch {
		case pending == 0:
			sum.NextReason = "タスク待ち"
		case active:
			sum.NextReason = "前回終了待ち"
		default:
			base := latestUpdated
			if sum.LastRunAt != nil && sum.LastRunAt.After(base) {
				base = *sum.LastRunAt
			}
			if next, err := automation.NextFireTime(a, base); err == nil && next.After(now) {
				sum.NextFireAt = &next
			} else {
				sum.NextReason = "まもなく実行"
			}
		}
		return
	}
	base := a.CreatedAt
	if latest, err := s.automations.LatestRun(a.ID); err == nil && latest != nil {
		base = latest.FiredAt
	}
	if next, err := automation.NextFireTime(a, base); err == nil {
		sum.NextFireAt = &next
	}
}

// latestDisabledReason returns the newest "disabled:" run message, if any.
func (s *Server) latestDisabledReason(automationID string) string {
	var msg sql.NullString
	err := s.sqlDB.QueryRow(
		`SELECT message FROM automation_runs WHERE automation_id = ? AND message LIKE 'disabled:%'
		 ORDER BY fired_at DESC, id DESC LIMIT 1`, automationID).Scan(&msg)
	if err != nil {
		return ""
	}
	return msg.String
}
