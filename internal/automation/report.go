package automation

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrNoRunForSession is returned when a session is not linked to any
// automation run.
var ErrNoRunForSession = errors.New("session is not associated with an automation run")

// ReportLink is a titled link attached to a run report.
type ReportLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Report is the per-run result recorded by the loop_report tool.
type Report struct {
	RunID     int64              `json:"runId"`
	Metrics   map[string]float64 `json:"metrics"`
	Summary   string             `json:"summary"`
	Links     []ReportLink       `json:"links"`
	CreatedAt time.Time          `json:"createdAt"`
}

// RunReport is a run together with its report (if any) and values derived by
// agmux (duration, cost, tokens).
type RunReport struct {
	Run
	Report *Report `json:"report"`
	// DurationSeconds is fired_at until the session stopped, or until now
	// when the session has not stopped yet.
	DurationSeconds float64 `json:"durationSeconds"`
	// Stopped reports whether the session had stopped (DurationSeconds is final).
	Stopped bool `json:"stopped"`
	// Cost / tokens come from OTel data; zero when none was recorded.
	Cost         float64 `json:"cost"`
	InputTokens  float64 `json:"inputTokens"`
	OutputTokens float64 `json:"outputTokens"`
}

// FindRunBySession returns the latest run that created the given session.
func (s *Store) FindRunBySession(sessionID string) (*Run, error) {
	if sessionID == "" {
		return nil, ErrNoRunForSession
	}
	row := s.db.QueryRow(
		`SELECT id, automation_id, fired_at, session_id, status, message, created_at
		 FROM automation_runs WHERE session_id = ? ORDER BY fired_at DESC, id DESC LIMIT 1`,
		sessionID,
	)
	r, err := scanRun(row)
	if err == sql.ErrNoRows {
		return nil, ErrNoRunForSession
	}
	if err != nil {
		return nil, fmt.Errorf("find run by session: %w", err)
	}
	return r, nil
}

// SaveReport stores the report for a run, overwriting any previous one.
func (s *Store) SaveReport(runID int64, metrics map[string]float64, summary string, links []ReportLink) (*Report, error) {
	if metrics == nil {
		metrics = map[string]float64{}
	}
	if links == nil {
		links = []ReportLink{}
	}
	mj, err := json.Marshal(metrics)
	if err != nil {
		return nil, err
	}
	lj, err := json.Marshal(links)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if _, err := s.db.Exec(
		`INSERT INTO automation_run_reports (run_id, metrics, summary, links, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(run_id) DO UPDATE SET metrics = excluded.metrics, summary = excluded.summary,
		   links = excluded.links, created_at = excluded.created_at`,
		runID, string(mj), summary, string(lj), now,
	); err != nil {
		return nil, fmt.Errorf("save run report: %w", err)
	}
	return &Report{RunID: runID, Metrics: metrics, Summary: summary, Links: links, CreatedAt: now}, nil
}

// ListRunReports returns the runs of an automation in chronological order
// (oldest first) with their reports, duration and cost. limit <= 0 means 50;
// when truncated the most recent runs are kept.
func (s *Store) ListRunReports(automationID string, limit int) ([]RunReport, error) {
	runs, err := s.ListRuns(automationID, limit) // newest first
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := make([]RunReport, 0, len(runs))
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		rr := RunReport{Run: r}
		rep, err := s.getReport(r.ID)
		if err != nil {
			return nil, err
		}
		rr.Report = rep
		if r.SessionID != "" {
			end := now
			var status sql.NullString
			var updated sql.NullTime
			err := s.db.QueryRow(`SELECT status, updated_at FROM sessions WHERE id = ?`, r.SessionID).Scan(&status, &updated)
			if err == nil && updated.Valid && (status.String == "paused" || status.String == "exited" || status.String == "archived") {
				end = updated.Time
				rr.Stopped = true
			}
			if d := end.Sub(r.FiredAt).Seconds(); d > 0 {
				rr.DurationSeconds = d
			}
			s.fillUsage(&rr)
		}
		out = append(out, rr)
	}
	return out, nil
}

// fillUsage sums cost / tokens recorded via OTel for the run's session.
// Missing data leaves the values at zero.
func (s *Store) fillUsage(rr *RunReport) {
	var cost sql.NullFloat64
	if err := s.db.QueryRow(
		`SELECT SUM(value) FROM otel_metrics WHERE name = 'claude_code.cost.usage' AND session_id = ?`,
		rr.SessionID).Scan(&cost); err == nil && cost.Valid {
		rr.Cost = cost.Float64
	}
	rows, err := s.db.Query(
		`SELECT json_extract(attributes, '$.type'), SUM(value) FROM otel_metrics
		 WHERE name = 'claude_code.token.usage' AND session_id = ? GROUP BY 1`, rr.SessionID)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var typ sql.NullString
		var v float64
		if rows.Scan(&typ, &v) != nil {
			continue
		}
		switch typ.String {
		case "input":
			rr.InputTokens = v
		case "output":
			rr.OutputTokens = v
		}
	}
}

func (s *Store) getReport(runID int64) (*Report, error) {
	var mj, lj, summary string
	rep := &Report{RunID: runID}
	err := s.db.QueryRow(
		`SELECT metrics, summary, links, created_at FROM automation_run_reports WHERE run_id = ?`, runID,
	).Scan(&mj, &summary, &lj, &rep.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get run report: %w", err)
	}
	rep.Summary = summary
	_ = json.Unmarshal([]byte(mj), &rep.Metrics)
	_ = json.Unmarshal([]byte(lj), &rep.Links)
	if rep.Metrics == nil {
		rep.Metrics = map[string]float64{}
	}
	if rep.Links == nil {
		rep.Links = []ReportLink{}
	}
	return rep, nil
}
