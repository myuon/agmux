package automation

import (
	"database/sql"
	"fmt"
	"time"

	gonanoid "github.com/matoous/go-nanoid/v2"
)

const nanoidAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"

func newAutomationID() (string, error) {
	return gonanoid.Generate(nanoidAlphabet, 8)
}

// Store manages automations / automation_runs rows in SQLite.
type Store struct {
	db *sql.DB
}

// NewStore creates a store backed by the given SQLite DB.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// CreateParams holds the fields required to create an automation.
type CreateParams struct {
	Name         string
	Prompt       string
	TriggerType  TriggerType
	TriggerValue string
	ProjectPath  string // empty = controller area
	Enabled      bool
}

// Create inserts a new automation and returns it.
func (s *Store) Create(p CreateParams) (*Automation, error) {
	if p.Name == "" {
		return nil, fmt.Errorf("automation name is required")
	}
	if p.Prompt == "" {
		return nil, fmt.Errorf("automation prompt is required")
	}
	if err := ValidateTrigger(p.TriggerType, p.TriggerValue); err != nil {
		return nil, err
	}

	id, err := newAutomationID()
	if err != nil {
		return nil, fmt.Errorf("generate automation id: %w", err)
	}

	now := time.Now()
	a := &Automation{
		ID:           id,
		Name:         p.Name,
		Prompt:       p.Prompt,
		TriggerType:  p.TriggerType,
		TriggerValue: p.TriggerValue,
		ProjectPath:  p.ProjectPath,
		Enabled:      p.Enabled,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if _, err := s.db.Exec(
		`INSERT INTO automations (id, name, prompt, trigger_type, trigger_value, project_path, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.Prompt, string(a.TriggerType), a.TriggerValue, a.ProjectPath, boolToInt(a.Enabled), a.CreatedAt, a.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("insert automation: %w", err)
	}
	return a, nil
}

// UpdateParams holds the editable fields of an automation.
type UpdateParams struct {
	Name         string
	Prompt       string
	TriggerType  TriggerType
	TriggerValue string
	ProjectPath  string
	Enabled      bool
}

// Update replaces the editable fields of an automation.
func (s *Store) Update(id string, p UpdateParams) (*Automation, error) {
	if p.Name == "" {
		return nil, fmt.Errorf("automation name is required")
	}
	if p.Prompt == "" {
		return nil, fmt.Errorf("automation prompt is required")
	}
	if err := ValidateTrigger(p.TriggerType, p.TriggerValue); err != nil {
		return nil, err
	}
	res, err := s.db.Exec(
		`UPDATE automations SET name = ?, prompt = ?, trigger_type = ?, trigger_value = ?, project_path = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		p.Name, p.Prompt, string(p.TriggerType), p.TriggerValue, p.ProjectPath, boolToInt(p.Enabled), time.Now(), id,
	)
	if err != nil {
		return nil, fmt.Errorf("update automation: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("automation not found: %s", id)
	}
	return s.Get(id)
}

// SetEnabled toggles the enabled flag of an automation.
func (s *Store) SetEnabled(id string, enabled bool) error {
	res, err := s.db.Exec(
		`UPDATE automations SET enabled = ?, updated_at = ? WHERE id = ?`,
		boolToInt(enabled), time.Now(), id,
	)
	if err != nil {
		return fmt.Errorf("update automation enabled: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("automation not found: %s", id)
	}
	return nil
}

// Delete removes an automation and its run history.
func (s *Store) Delete(id string) error {
	res, err := s.db.Exec(`DELETE FROM automations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete automation: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("automation not found: %s", id)
	}
	if _, err := s.db.Exec(`DELETE FROM automation_runs WHERE automation_id = ?`, id); err != nil {
		return fmt.Errorf("delete automation runs: %w", err)
	}
	return nil
}

// Get returns an automation by ID.
func (s *Store) Get(id string) (*Automation, error) {
	row := s.db.QueryRow(
		`SELECT id, name, prompt, trigger_type, trigger_value, project_path, enabled, created_at, updated_at
		 FROM automations WHERE id = ?`, id,
	)
	a, err := scanAutomation(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("automation not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("query automation: %w", err)
	}
	return a, nil
}

// List returns all automations ordered by creation time (newest first).
func (s *Store) List() ([]Automation, error) {
	return s.list(`SELECT id, name, prompt, trigger_type, trigger_value, project_path, enabled, created_at, updated_at
		FROM automations ORDER BY created_at DESC`)
}

// ListEnabled returns all enabled automations.
func (s *Store) ListEnabled() ([]Automation, error) {
	return s.list(`SELECT id, name, prompt, trigger_type, trigger_value, project_path, enabled, created_at, updated_at
		FROM automations WHERE enabled = 1 ORDER BY created_at DESC`)
}

func (s *Store) list(query string) ([]Automation, error) {
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("query automations: %w", err)
	}
	defer rows.Close()

	var automations []Automation
	for rows.Next() {
		a, err := scanAutomation(rows)
		if err != nil {
			return nil, fmt.Errorf("scan automation: %w", err)
		}
		automations = append(automations, *a)
	}
	return automations, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAutomation(row rowScanner) (*Automation, error) {
	var a Automation
	var triggerType string
	var projectPath sql.NullString
	var enabledInt int
	if err := row.Scan(&a.ID, &a.Name, &a.Prompt, &triggerType, &a.TriggerValue, &projectPath, &enabledInt, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	a.TriggerType = TriggerType(triggerType)
	if projectPath.Valid {
		a.ProjectPath = projectPath.String
	}
	a.Enabled = enabledInt != 0
	return &a, nil
}

// InsertRun appends an execution history entry and returns it with its ID set.
func (s *Store) InsertRun(run Run) (*Run, error) {
	now := time.Now()
	res, err := s.db.Exec(
		`INSERT INTO automation_runs (automation_id, fired_at, session_id, status, message, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		run.AutomationID, run.FiredAt, run.SessionID, string(run.Status), run.Message, now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert automation run: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("automation run last insert id: %w", err)
	}
	run.ID = id
	run.CreatedAt = now
	return &run, nil
}

// ListRuns returns the execution history of an automation, newest first.
func (s *Store) ListRuns(automationID string, limit int) ([]Run, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT id, automation_id, fired_at, session_id, status, message, created_at
		 FROM automation_runs WHERE automation_id = ? ORDER BY fired_at DESC, id DESC LIMIT ?`,
		automationID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query automation runs: %w", err)
	}
	defer rows.Close()

	var runs []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scan automation run: %w", err)
		}
		runs = append(runs, *r)
	}
	return runs, rows.Err()
}

// LatestRun returns the most recent run of an automation regardless of
// status, or nil if the automation has never fired.
func (s *Store) LatestRun(automationID string) (*Run, error) {
	row := s.db.QueryRow(
		`SELECT id, automation_id, fired_at, session_id, status, message, created_at
		 FROM automation_runs WHERE automation_id = ? ORDER BY fired_at DESC, id DESC LIMIT 1`,
		automationID,
	)
	r, err := scanRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query latest automation run: %w", err)
	}
	return r, nil
}

// LatestSessionRun returns the most recent run that created a session, or
// nil if no run has created a session yet. Used for the multi-run skip check.
func (s *Store) LatestSessionRun(automationID string) (*Run, error) {
	row := s.db.QueryRow(
		`SELECT id, automation_id, fired_at, session_id, status, message, created_at
		 FROM automation_runs
		 WHERE automation_id = ? AND session_id IS NOT NULL AND session_id != ''
		 ORDER BY fired_at DESC, id DESC LIMIT 1`,
		automationID,
	)
	r, err := scanRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query latest session run: %w", err)
	}
	return r, nil
}

func scanRun(row rowScanner) (*Run, error) {
	var r Run
	var sessionID, message sql.NullString
	var status string
	if err := row.Scan(&r.ID, &r.AutomationID, &r.FiredAt, &sessionID, &status, &message, &r.CreatedAt); err != nil {
		return nil, err
	}
	if sessionID.Valid {
		r.SessionID = sessionID.String
	}
	if message.Valid {
		r.Message = message.String
	}
	r.Status = RunStatus(status)
	return &r, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
