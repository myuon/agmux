package otel

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// MetricRow represents a stored metric data point.
type MetricRow struct {
	ID         int64             `json:"id"`
	Name       string            `json:"name"`
	Value      float64           `json:"value"`
	Attributes map[string]string `json:"attributes"`
	SessionID  string            `json:"sessionId"`
	Timestamp  time.Time         `json:"timestamp"`
}

// EventRow represents a stored event.
type EventRow struct {
	ID         int64             `json:"id"`
	Name       string            `json:"name"`
	Body       string            `json:"body"`
	Attributes map[string]string `json:"attributes"`
	SessionID  string            `json:"sessionId"`
	Timestamp  time.Time         `json:"timestamp"`
}

// MetricSummary holds aggregated metric data.
type MetricSummary struct {
	TotalCost         float64            `json:"totalCost"`
	TotalTokens       map[string]float64 `json:"totalTokens"`
	SessionCount      float64            `json:"sessionCount"`
	LinesOfCode       float64            `json:"linesOfCode"`
	ActiveTime        float64            `json:"activeTime"`
	CommitCount       float64            `json:"commitCount"`
	PullRequestCount  float64            `json:"pullRequestCount"`
	CodeEditDecisions float64            `json:"codeEditDecisions"`
	CostBySession     []SessionCost      `json:"costBySession"`
	TokensBySession   []SessionTokens    `json:"tokensBySession"`
}

type SessionCost struct {
	SessionID string  `json:"sessionId"`
	Cost      float64 `json:"cost"`
}

type SessionTokens struct {
	SessionID string  `json:"sessionId"`
	Input     float64 `json:"input"`
	Output    float64 `json:"output"`
}

// Store provides query methods over the otel tables.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// QueryMetrics returns metrics filtered by name, session, and time range.
func (s *Store) QueryMetrics(name, sessionID string, since time.Time) ([]MetricRow, error) {
	query := `SELECT id, name, value, attributes, session_id, timestamp FROM otel_metrics WHERE 1=1`
	var args []interface{}

	if name != "" {
		query += " AND name = ?"
		args = append(args, name)
	}
	if sessionID != "" {
		query += " AND session_id = ?"
		args = append(args, sessionID)
	}
	if !since.IsZero() {
		query += " AND timestamp >= ?"
		args = append(args, since)
	}
	query += " ORDER BY timestamp ASC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query metrics: %w", err)
	}
	defer rows.Close()

	var results []MetricRow
	for rows.Next() {
		var r MetricRow
		var attrsJSON string
		var sid sql.NullString
		if err := rows.Scan(&r.ID, &r.Name, &r.Value, &attrsJSON, &sid, &r.Timestamp); err != nil {
			return nil, fmt.Errorf("scan metric: %w", err)
		}
		json.Unmarshal([]byte(attrsJSON), &r.Attributes)
		if sid.Valid {
			r.SessionID = sid.String
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// QueryEvents returns events filtered by name, session, and time range.
func (s *Store) QueryEvents(name, sessionID string, since time.Time) ([]EventRow, error) {
	query := `SELECT id, name, body, attributes, session_id, timestamp FROM otel_events WHERE 1=1`
	var args []interface{}

	if name != "" {
		query += " AND name = ?"
		args = append(args, name)
	}
	if sessionID != "" {
		query += " AND session_id = ?"
		args = append(args, sessionID)
	}
	if !since.IsZero() {
		query += " AND timestamp >= ?"
		args = append(args, since)
	}
	query += " ORDER BY timestamp ASC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var results []EventRow
	for rows.Next() {
		var r EventRow
		var attrsJSON string
		var sid sql.NullString
		if err := rows.Scan(&r.ID, &r.Name, &r.Body, &attrsJSON, &sid, &r.Timestamp); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		json.Unmarshal([]byte(attrsJSON), &r.Attributes)
		if sid.Valid {
			r.SessionID = sid.String
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// GetSummary returns aggregated metrics summary.
func (s *Store) GetSummary(since time.Time) (*MetricSummary, error) {
	summary := &MetricSummary{
		TotalTokens: make(map[string]float64),
	}

	// Total cost
	var cost sql.NullFloat64
	err := s.db.QueryRow(
		`SELECT SUM(value) FROM otel_metrics WHERE name = 'claude_code.cost.usage' AND (? = '' OR timestamp >= ?)`,
		since.Format(time.RFC3339), since,
	).Scan(&cost)
	if err == nil && cost.Valid {
		summary.TotalCost = cost.Float64
	}

	// Tokens by type
	rows, err := s.db.Query(
		`SELECT json_extract(attributes, '$.type') as token_type, SUM(value)
		 FROM otel_metrics WHERE name = 'claude_code.token.usage' AND (? = '' OR timestamp >= ?)
		 GROUP BY token_type`,
		since.Format(time.RFC3339), since,
	)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var tokenType sql.NullString
			var val float64
			if rows.Scan(&tokenType, &val) == nil && tokenType.Valid {
				summary.TotalTokens[tokenType.String] = val
			}
		}
	}

	// Session count
	var sc sql.NullFloat64
	s.db.QueryRow(
		`SELECT SUM(value) FROM otel_metrics WHERE name = 'claude_code.session.count' AND (? = '' OR timestamp >= ?)`,
		since.Format(time.RFC3339), since,
	).Scan(&sc)
	if sc.Valid {
		summary.SessionCount = sc.Float64
	}

	// Lines of code
	var loc sql.NullFloat64
	s.db.QueryRow(
		`SELECT SUM(value) FROM otel_metrics WHERE name = 'claude_code.lines_of_code.count' AND (? = '' OR timestamp >= ?)`,
		since.Format(time.RFC3339), since,
	).Scan(&loc)
	if loc.Valid {
		summary.LinesOfCode = loc.Float64
	}

	// Active time
	var at sql.NullFloat64
	s.db.QueryRow(
		`SELECT SUM(value) FROM otel_metrics WHERE name = 'claude_code.active_time.total' AND (? = '' OR timestamp >= ?)`,
		since.Format(time.RFC3339), since,
	).Scan(&at)
	if at.Valid {
		summary.ActiveTime = at.Float64
	}

	// Commit count
	var cc sql.NullFloat64
	s.db.QueryRow(
		`SELECT SUM(value) FROM otel_metrics WHERE name = 'claude_code.commit.count' AND (? = '' OR timestamp >= ?)`,
		since.Format(time.RFC3339), since,
	).Scan(&cc)
	if cc.Valid {
		summary.CommitCount = cc.Float64
	}

	// Pull request count
	var prc sql.NullFloat64
	s.db.QueryRow(
		`SELECT SUM(value) FROM otel_metrics WHERE name = 'claude_code.pull_request.count' AND (? = '' OR timestamp >= ?)`,
		since.Format(time.RFC3339), since,
	).Scan(&prc)
	if prc.Valid {
		summary.PullRequestCount = prc.Float64
	}

	// Code edit decisions
	var ced sql.NullFloat64
	s.db.QueryRow(
		`SELECT SUM(value) FROM otel_metrics WHERE name = 'claude_code.code_edit_tool.decision' AND (? = '' OR timestamp >= ?)`,
		since.Format(time.RFC3339), since,
	).Scan(&ced)
	if ced.Valid {
		summary.CodeEditDecisions = ced.Float64
	}

	// Cost by session
	costRows, err := s.db.Query(
		`SELECT session_id, SUM(value) FROM otel_metrics WHERE name = 'claude_code.cost.usage' AND session_id != '' AND (? = '' OR timestamp >= ?)
		 GROUP BY session_id ORDER BY SUM(value) DESC`,
		since.Format(time.RFC3339), since,
	)
	if err == nil {
		defer costRows.Close()
		for costRows.Next() {
			var sc SessionCost
			if costRows.Scan(&sc.SessionID, &sc.Cost) == nil {
				summary.CostBySession = append(summary.CostBySession, sc)
			}
		}
	}

	// Tokens by session
	tokenRows, err := s.db.Query(
		`SELECT session_id,
			SUM(CASE WHEN json_extract(attributes, '$.type') = 'input' THEN value ELSE 0 END) as input_tokens,
			SUM(CASE WHEN json_extract(attributes, '$.type') = 'output' THEN value ELSE 0 END) as output_tokens
		 FROM otel_metrics WHERE name = 'claude_code.token.usage' AND session_id != '' AND (? = '' OR timestamp >= ?)
		 GROUP BY session_id`,
		since.Format(time.RFC3339), since,
	)
	if err == nil {
		defer tokenRows.Close()
		for tokenRows.Next() {
			var st SessionTokens
			if tokenRows.Scan(&st.SessionID, &st.Input, &st.Output) == nil {
				summary.TokensBySession = append(summary.TokensBySession, st)
			}
		}
	}

	return summary, nil
}
