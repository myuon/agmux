package automation

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// FeatureLoops is the experimental feature flag gating the loop task list.
const FeatureLoops = "loops"

// MaxTaskAttempts is how many times an abandoned in_progress task is retried
// before it is marked failed.
const MaxTaskAttempts = 3

// orphanGrace is how long an in_progress task without a run is left alone
// before it is reclaimed (the run row is inserted shortly after the session
// is created, so a task may briefly have no run).
const orphanGrace = 30 * time.Minute

// TaskStatus is the state of a loop task.
type TaskStatus string

const (
	TaskPending    TaskStatus = "pending"
	TaskInProgress TaskStatus = "in_progress"
	TaskDone       TaskStatus = "done"
	TaskFailed     TaskStatus = "failed"
)

// Valid reports whether the status is a known task status.
func (s TaskStatus) Valid() bool {
	switch s {
	case TaskPending, TaskInProgress, TaskDone, TaskFailed:
		return true
	}
	return false
}

// Task creators.
const (
	CreatedByUser  = "user"
	CreatedByAgent = "agent"
)

// Task is one entry of an automation's remaining-task list.
type Task struct {
	ID           int64      `json:"id"`
	AutomationID string     `json:"automationId"`
	Title        string     `json:"title"`
	Body         string     `json:"body"`
	Status       TaskStatus `json:"status"`
	RunID        *int64     `json:"runId,omitempty"`
	Result       string     `json:"result"`
	CreatedBy    string     `json:"createdBy"`
	Attempts     int        `json:"attempts"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

const taskColumns = `id, automation_id, title, body, status, run_id, result, created_by, attempts, created_at, updated_at`

func scanTask(row rowScanner) (*Task, error) {
	var t Task
	var status string
	var runID sql.NullInt64
	if err := row.Scan(&t.ID, &t.AutomationID, &t.Title, &t.Body, &status, &runID, &t.Result, &t.CreatedBy, &t.Attempts, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	t.Status = TaskStatus(status)
	if runID.Valid {
		v := runID.Int64
		t.RunID = &v
	}
	return &t, nil
}

// AddTask appends a pending task to the automation's list.
func (s *Store) AddTask(automationID, title, body, createdBy string) (*Task, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("task title is required")
	}
	if createdBy != CreatedByAgent {
		createdBy = CreatedByUser
	}
	if _, err := s.Get(automationID); err != nil {
		return nil, err
	}
	now := time.Now()
	res, err := s.db.Exec(
		`INSERT INTO automation_tasks (automation_id, title, body, status, result, created_by, attempts, created_at, updated_at)
		 VALUES (?, ?, ?, 'pending', '', ?, 0, ?, ?)`,
		automationID, title, body, createdBy, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert task: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("task last insert id: %w", err)
	}
	return s.GetTask(automationID, id)
}

// GetTask returns a task of the automation.
func (s *Store) GetTask(automationID string, id int64) (*Task, error) {
	row := s.db.QueryRow(`SELECT `+taskColumns+` FROM automation_tasks WHERE id = ? AND automation_id = ?`, id, automationID)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("task not found: %d", id)
	}
	if err != nil {
		return nil, fmt.Errorf("query task: %w", err)
	}
	return t, nil
}

// ListTasks returns all tasks of the automation, oldest first.
func (s *Store) ListTasks(automationID string) ([]Task, error) {
	rows, err := s.db.Query(`SELECT `+taskColumns+` FROM automation_tasks WHERE automation_id = ? ORDER BY id ASC`, automationID)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}
	defer rows.Close()
	tasks := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, *t)
	}
	return tasks, rows.Err()
}

// UpdateTaskParams holds the editable fields of a task; nil keeps the value.
// Setting the status back to pending resets run, result and attempts.
type UpdateTaskParams struct {
	Title  *string
	Body   *string
	Status *TaskStatus
}

// UpdateTask edits a task.
func (s *Store) UpdateTask(automationID string, id int64, p UpdateTaskParams) (*Task, error) {
	t, err := s.GetTask(automationID, id)
	if err != nil {
		return nil, err
	}
	if p.Title != nil {
		title := strings.TrimSpace(*p.Title)
		if title == "" {
			return nil, fmt.Errorf("task title is required")
		}
		t.Title = title
	}
	if p.Body != nil {
		t.Body = *p.Body
	}
	if p.Status != nil {
		if !p.Status.Valid() {
			return nil, fmt.Errorf("invalid task status %q", *p.Status)
		}
		if *p.Status == TaskPending && t.Status != TaskPending {
			t.RunID, t.Result, t.Attempts = nil, "", 0
		}
		t.Status = *p.Status
	}
	if _, err := s.db.Exec(
		`UPDATE automation_tasks SET title = ?, body = ?, status = ?, run_id = ?, result = ?, attempts = ?, updated_at = ? WHERE id = ? AND automation_id = ?`,
		t.Title, t.Body, string(t.Status), t.RunID, t.Result, t.Attempts, time.Now(), id, automationID,
	); err != nil {
		return nil, fmt.Errorf("update task: %w", err)
	}
	return s.GetTask(automationID, id)
}

// DeleteTask removes a task.
func (s *Store) DeleteTask(automationID string, id int64) error {
	res, err := s.db.Exec(`DELETE FROM automation_tasks WHERE id = ? AND automation_id = ?`, id, automationID)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("task not found: %d", id)
	}
	return nil
}

// NextTask takes the oldest pending task, marks it in_progress and links it
// to runID (nil when the run is not known yet). It returns nil when there is
// no pending task.
func (s *Store) NextTask(automationID string, runID *int64) (*Task, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()
	row := tx.QueryRow(`SELECT `+taskColumns+` FROM automation_tasks WHERE automation_id = ? AND status = 'pending' ORDER BY id ASC LIMIT 1`, automationID)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query next task: %w", err)
	}
	now := time.Now()
	if _, err := tx.Exec(`UPDATE automation_tasks SET status = 'in_progress', run_id = ?, updated_at = ? WHERE id = ?`, runID, now, t.ID); err != nil {
		return nil, fmt.Errorf("update next task: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	t.Status, t.RunID, t.UpdatedAt = TaskInProgress, runID, now
	return t, nil
}

// FinishTask marks a task done or failed and records the result.
func (s *Store) FinishTask(automationID string, id int64, status TaskStatus, result string) (*Task, error) {
	if status != TaskDone && status != TaskFailed {
		return nil, fmt.Errorf("status must be done or failed, got %q", status)
	}
	if _, err := s.GetTask(automationID, id); err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(`UPDATE automation_tasks SET status = ?, result = ?, updated_at = ? WHERE id = ? AND automation_id = ?`,
		string(status), result, time.Now(), id, automationID); err != nil {
		return nil, fmt.Errorf("finish task: %w", err)
	}
	return s.GetTask(automationID, id)
}

// AutomationIDForSession resolves the automation that created a session.
// It returns "" for sessions that were not created by an automation.
func (s *Store) AutomationIDForSession(sessionID string) (string, error) {
	var id sql.NullString
	err := s.db.QueryRow(`SELECT automation_id FROM sessions WHERE id = ?`, sessionID).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("query session automation: %w", err)
	}
	return id.String, nil
}

// RunForSession returns the most recent run that created the session, or nil.
func (s *Store) RunForSession(sessionID string) (*Run, error) {
	row := s.db.QueryRow(
		`SELECT id, automation_id, fired_at, session_id, status, message, created_at
		 FROM automation_runs WHERE session_id = ? ORDER BY id DESC LIMIT 1`, sessionID)
	r, err := scanRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query run for session: %w", err)
	}
	return r, nil
}

// ReclaimAbandonedTasks returns in_progress tasks to pending when the session
// of their run is no longer active (isActive false), counting the attempt; a
// task that reaches MaxTaskAttempts becomes failed instead. Tasks without a
// run are reclaimed once they are older than a grace period. It returns the
// number of tasks changed.
func (s *Store) ReclaimAbandonedTasks(isActive func(sessionID string) bool, now time.Time) (int, error) {
	rows, err := s.db.Query(
		`SELECT t.id, t.attempts, t.updated_at, COALESCE(r.session_id, '')
		 FROM automation_tasks t LEFT JOIN automation_runs r ON r.id = t.run_id
		 WHERE t.status = 'in_progress'`)
	if err != nil {
		return 0, fmt.Errorf("query in_progress tasks: %w", err)
	}
	type cand struct {
		id       int64
		attempts int
	}
	var cands []cand
	for rows.Next() {
		var id int64
		var attempts int
		var updated time.Time
		var sessionID string
		if err := rows.Scan(&id, &attempts, &updated, &sessionID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan in_progress task: %w", err)
		}
		if sessionID == "" {
			if now.Sub(updated) < orphanGrace {
				continue
			}
		} else if isActive(sessionID) {
			continue
		}
		cands = append(cands, cand{id, attempts})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, c := range cands {
		attempts := c.attempts + 1
		status, result := TaskPending, ""
		if attempts >= MaxTaskAttempts {
			status = TaskFailed
			result = fmt.Sprintf("loop_task_done が呼ばれないままセッションが終了することが %d 回続いたため失敗にしました", attempts)
		}
		if _, err := s.db.Exec(
			`UPDATE automation_tasks SET status = ?, attempts = ?, run_id = CASE WHEN ? = 'pending' THEN NULL ELSE run_id END,
			 result = CASE WHEN ? = '' THEN result ELSE ? END, updated_at = ? WHERE id = ? AND status = 'in_progress'`,
			string(status), attempts, string(status), result, result, now, c.id,
		); err != nil {
			return 0, fmt.Errorf("reclaim task: %w", err)
		}
	}
	return len(cands), nil
}
