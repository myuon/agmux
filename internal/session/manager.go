package session

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/myuon/agmux/internal/tmux"
)

type Manager struct {
	db            *sql.DB
	tmux          *tmux.Client
	claudeCommand string
}

func NewManager(db *sql.DB, tmuxClient *tmux.Client, claudeCommand string) *Manager {
	if claudeCommand == "" {
		claudeCommand = "claude --dangerously-skip-permissions"
	}
	return &Manager{db: db, tmux: tmuxClient, claudeCommand: claudeCommand}
}

func (m *Manager) Create(name, projectPath, prompt string) (*Session, error) {
	id := uuid.New().String()
	tmuxSession := tmux.SessionPrefix + name

	// Start tmux session with a shell, then launch claude via SendKeys
	// (passing command directly to new-session causes immediate exit)
	if err := m.tmux.NewSession(name, projectPath, ""); err != nil {
		return nil, fmt.Errorf("create tmux session: %w", err)
	}

	// Wait for shell to be ready, then send claude command
	time.Sleep(300 * time.Millisecond)
	if err := m.tmux.SendKeysOnce(tmuxSession, m.claudeCommand); err != nil {
		return nil, fmt.Errorf("launch claude: %w", err)
	}

	// Wait for claude to start and handle trust prompt, then send initial prompt
	if prompt != "" {
		if err := m.waitForClaudeReady(tmuxSession, 30*time.Second); err != nil {
			return nil, fmt.Errorf("wait for claude: %w", err)
		}
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
		Type:          TypeWorker,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	_, err := m.db.Exec(
		`INSERT INTO sessions (id, name, project_path, initial_prompt, tmux_session, status, type, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, s.ProjectPath, s.InitialPrompt, s.TmuxSession, string(s.Status), string(s.Type), s.CreatedAt, s.UpdatedAt,
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
		`SELECT id, name, project_path, initial_prompt, tmux_session, status, type, created_at, updated_at
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
		var sessionType string
		var prompt sql.NullString
		if err := rows.Scan(&s.ID, &s.Name, &s.ProjectPath, &prompt, &s.TmuxSession, &status, &sessionType, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		s.Status = Status(status)
		s.Type = SessionType(sessionType)
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
	var sessionType string
	var prompt sql.NullString
	err := m.db.QueryRow(
		`SELECT id, name, project_path, initial_prompt, tmux_session, status, type, created_at, updated_at
		 FROM sessions WHERE id = ?`, id,
	).Scan(&s.ID, &s.Name, &s.ProjectPath, &prompt, &s.TmuxSession, &status, &sessionType, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("query session: %w", err)
	}
	s.Status = Status(status)
	s.Type = SessionType(sessionType)
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

// waitForClaudeReady polls the tmux pane until claude is ready for input.
// It handles the workspace trust prompt by sending Enter if detected.
func (m *Manager) waitForClaudeReady(tmuxSession string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	trustHandled := false

	for time.Now().Before(deadline) {
		output, err := m.tmux.CapturePane(tmuxSession, 30)
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		// Handle trust prompt
		if !trustHandled && strings.Contains(output, "Yes, I trust this folder") {
			_ = m.tmux.SendKeysRaw(tmuxSession, "Enter")
			trustHandled = true
			time.Sleep(2 * time.Second)
			continue
		}

		// Claude is ready when we see the input prompt (> or similar)
		if trustHandled || !strings.Contains(output, "trust this folder") {
			// Check for claude's input prompt indicator
			if strings.Contains(output, "> ") || strings.Contains(output, "Claude") {
				return nil
			}
		}

		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("claude did not become ready within %s", timeout)
}

// GetController returns the controller session if it exists.
func (m *Manager) GetController() (*Session, error) {
	var s Session
	var status string
	var sessionType string
	var prompt sql.NullString
	err := m.db.QueryRow(
		`SELECT id, name, project_path, initial_prompt, tmux_session, status, type, created_at, updated_at
		 FROM sessions WHERE type = ? LIMIT 1`, string(TypeController),
	).Scan(&s.ID, &s.Name, &s.ProjectPath, &prompt, &s.TmuxSession, &status, &sessionType, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query controller session: %w", err)
	}
	s.Status = Status(status)
	s.Type = SessionType(sessionType)
	if prompt.Valid {
		s.InitialPrompt = prompt.String
	}
	return &s, nil
}

// CreateController creates the singleton controller session.
// Returns the existing controller session if one already exists and is active.
func (m *Manager) CreateController(projectPath string) (*Session, error) {
	existing, err := m.GetController()
	if err != nil {
		return nil, err
	}
	if existing != nil && (existing.Status == StatusRunning || existing.Status == StatusWaiting) {
		// Already running, return existing
		return existing, nil
	}

	// If a stopped/done controller exists, delete it first
	if existing != nil {
		_ = m.Delete(existing.ID)
	}

	id := uuid.New().String()
	name := "controller"
	tmuxSession := tmux.SessionPrefix + name

	if err := m.tmux.NewSession(name, projectPath, ""); err != nil {
		return nil, fmt.Errorf("create controller tmux session: %w", err)
	}

	time.Sleep(300 * time.Millisecond)
	if err := m.tmux.SendKeysOnce(tmuxSession, m.claudeCommand); err != nil {
		return nil, fmt.Errorf("launch claude for controller: %w", err)
	}

	now := time.Now()
	s := &Session{
		ID:          id,
		Name:        name,
		ProjectPath: projectPath,
		TmuxSession: tmuxSession,
		Status:      StatusRunning,
		Type:        TypeController,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err = m.db.Exec(
		`INSERT INTO sessions (id, name, project_path, initial_prompt, tmux_session, status, type, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, s.ProjectPath, s.InitialPrompt, s.TmuxSession, string(s.Status), string(s.Type), s.CreatedAt, s.UpdatedAt,
	)
	if err != nil {
		_ = m.tmux.KillSession(name)
		return nil, fmt.Errorf("insert controller session: %w", err)
	}

	return s, nil
}

func (m *Manager) UpdateStatus(id string, status Status) error {
	_, err := m.db.Exec("UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?", string(status), time.Now(), id)
	return err
}
