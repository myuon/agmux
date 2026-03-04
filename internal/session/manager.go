package session

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/myuon/agmux/internal/tmux"
)

type Manager struct {
	db   *sql.DB
	tmux *tmux.Client
}

func NewManager(db *sql.DB, tmuxClient *tmux.Client) *Manager {
	return &Manager{db: db, tmux: tmuxClient}
}

func (m *Manager) Create(name, projectPath, prompt string) (*Session, error) {
	id := uuid.New().String()
	tmuxSession := tmux.SessionPrefix + name

	// Start claude code in tmux session
	command := "claude --dangerously-skip-permissions"
	if err := m.tmux.NewSession(name, projectPath, command); err != nil {
		return nil, fmt.Errorf("create tmux session: %w", err)
	}

	// Wait briefly for process to start
	time.Sleep(500 * time.Millisecond)

	// Send initial prompt if provided
	if prompt != "" {
		if err := m.tmux.SendKeys(tmuxSession, prompt); err != nil {
			return nil, fmt.Errorf("send initial prompt: %w", err)
		}
	}

	now := time.Now()
	s := &Session{
		ID:            id,
		Name:          name,
		ProjectPath:   projectPath,
		InitialPrompt: prompt,
		TmuxSession:   tmuxSession,
		Status:        StatusRunning,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	_, err := m.db.Exec(
		`INSERT INTO sessions (id, name, project_path, initial_prompt, tmux_session, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, s.ProjectPath, s.InitialPrompt, s.TmuxSession, string(s.Status), s.CreatedAt, s.UpdatedAt,
	)
	if err != nil {
		// Cleanup tmux session on DB error
		_ = m.tmux.KillSession(name)
		return nil, fmt.Errorf("insert session: %w", err)
	}

	return s, nil
}

func (m *Manager) List() ([]Session, error) {
	rows, err := m.db.Query(
		`SELECT id, name, project_path, initial_prompt, tmux_session, status, created_at, updated_at
		 FROM sessions ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var s Session
		var status string
		var prompt sql.NullString
		if err := rows.Scan(&s.ID, &s.Name, &s.ProjectPath, &prompt, &s.TmuxSession, &status, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		s.Status = Status(status)
		if prompt.Valid {
			s.InitialPrompt = prompt.String
		}

		// Correct status based on tmux reality
		if s.Status == StatusRunning || s.Status == StatusWaiting {
			if !m.tmux.HasSessionByFullName(s.TmuxSession) {
				s.Status = StatusStopped
				m.db.Exec("UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?", string(StatusStopped), time.Now(), s.ID)
			}
		}

		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

func (m *Manager) Get(id string) (*Session, error) {
	var s Session
	var status string
	var prompt sql.NullString
	err := m.db.QueryRow(
		`SELECT id, name, project_path, initial_prompt, tmux_session, status, created_at, updated_at
		 FROM sessions WHERE id = ?`, id,
	).Scan(&s.ID, &s.Name, &s.ProjectPath, &prompt, &s.TmuxSession, &status, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("query session: %w", err)
	}
	s.Status = Status(status)
	if prompt.Valid {
		s.InitialPrompt = prompt.String
	}
	return &s, nil
}

func (m *Manager) Stop(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}

	// Kill tmux session (strip prefix for KillSession which adds it)
	name := s.TmuxSession[len(tmux.SessionPrefix):]
	if m.tmux.HasSession(name) {
		if err := m.tmux.KillSession(name); err != nil {
			return fmt.Errorf("kill tmux session: %w", err)
		}
	}

	_, err = m.db.Exec("UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?", string(StatusStopped), time.Now(), id)
	return err
}

func (m *Manager) Delete(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}

	// Kill tmux session if it exists
	name := s.TmuxSession[len(tmux.SessionPrefix):]
	if m.tmux.HasSession(name) {
		_ = m.tmux.KillSession(name)
	}

	_, err = m.db.Exec("DELETE FROM sessions WHERE id = ?", id)
	return err
}

func (m *Manager) SendKeys(id string, text string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	return m.tmux.SendKeys(s.TmuxSession, text)
}

func (m *Manager) CaptureOutput(id string) (string, error) {
	s, err := m.Get(id)
	if err != nil {
		return "", err
	}
	return m.tmux.CapturePane(s.TmuxSession, 200)
}

func (m *Manager) UpdateStatus(id string, status Status) error {
	_, err := m.db.Exec("UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?", string(status), time.Now(), id)
	return err
}
