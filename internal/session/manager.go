package session

import (
	"bufio"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/myuon/agmux/internal/db"
	"github.com/myuon/agmux/internal/tmux"
)

type Manager struct {
	db              *sql.DB
	tmux            *tmux.Client
	claudeCommand   string
	apiPort         int
	streamProcesses map[string]*StreamProcess
	streamMu        sync.Mutex
}

func NewManager(db *sql.DB, tmuxClient *tmux.Client, claudeCommand string, apiPort int) *Manager {
	if claudeCommand == "" {
		claudeCommand = "claude --dangerously-skip-permissions"
	}
	return &Manager{
		db:              db,
		tmux:            tmuxClient,
		claudeCommand:   claudeCommand,
		apiPort:         apiPort,
		streamProcesses: make(map[string]*StreamProcess),
	}
}

func (m *Manager) Create(name, projectPath, prompt string, outputMode OutputMode) (*Session, error) {
	if outputMode == "" {
		outputMode = OutputModeTerminal
	}

	id := uuid.New().String()
	tmuxSession := tmux.SessionPrefix + name

	// Generate MCP config for this session
	mcpConfigPath, err := writeMCPConfig(id, m.apiPort)
	if err != nil {
		return nil, fmt.Errorf("write mcp config: %w", err)
	}

	// Start tmux session with a shell
	if err := m.tmux.NewSession(name, projectPath, ""); err != nil {
		return nil, fmt.Errorf("create tmux session: %w", err)
	}

	if outputMode == OutputModeStream {
		// Stream mode: start Go subprocess instead of claude TUI
		sp, err := StartStreamProcess(id, projectPath, mcpConfigPath, false)
		if err != nil {
			_ = m.tmux.KillSession(name)
			return nil, fmt.Errorf("start stream process: %w", err)
		}
		m.streamMu.Lock()
		m.streamProcesses[id] = sp
		m.streamMu.Unlock()

		// Send initial prompt via stream process
		if prompt != "" {
			if err := sp.Send(prompt); err != nil {
				return nil, fmt.Errorf("send initial prompt: %w", err)
			}
		}
	} else {
		// Terminal mode: launch claude TUI in tmux
		time.Sleep(300 * time.Millisecond)
		claudeCmd := m.claudeCommand + " --session-id " + id + " --mcp-config " + mcpConfigPath + " --append-system-prompt " + shellQuote(agmuxSystemPrompt)
		if err := m.tmux.SendKeysOnce(tmuxSession, claudeCmd); err != nil {
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
	}

	now := time.Now()
	s := &Session{
		ID:            id,
		Name:          name,
		ProjectPath:   projectPath,
		InitialPrompt: prompt,
		TmuxSession:   tmuxSession,
		Status:        StatusWorking,
		Type:          TypeWorker,
		OutputMode:    outputMode,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if _, err := m.db.Exec(
		`INSERT INTO sessions (id, name, project_path, initial_prompt, tmux_session, status, type, output_mode, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, s.ProjectPath, s.InitialPrompt, s.TmuxSession, string(s.Status), string(s.Type), string(s.OutputMode), s.CreatedAt, s.UpdatedAt,
	); err != nil {
		// Cleanup tmux session on DB error
		_ = m.tmux.KillSession(name)
		return nil, fmt.Errorf("insert session: %w", err)
	}

	return s, nil
}

func (m *Manager) List() ([]Session, error) {
	rows, err := m.db.Query(
		`SELECT id, name, project_path, initial_prompt, tmux_session, status, type, output_mode, current_task, goal, goals, created_at, updated_at
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
		var outputMode string
		var prompt, currentTask, goal, goalsJSON sql.NullString
		if err := rows.Scan(&s.ID, &s.Name, &s.ProjectPath, &prompt, &s.TmuxSession, &status, &sessionType, &outputMode, &currentTask, &goal, &goalsJSON, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		s.Status = Status(status)
		s.Type = SessionType(sessionType)
		s.OutputMode = OutputMode(outputMode)
		if prompt.Valid {
			s.InitialPrompt = prompt.String
		}
		if currentTask.Valid {
			s.CurrentTask = currentTask.String
		}
		if goal.Valid {
			s.Goal = goal.String
		}
		if goalsJSON.Valid {
			s.Goals = ParseGoalStack(goalsJSON.String)
		}

		// Correct status based on tmux reality (only for non-stream modes)
		if s.OutputMode != OutputModeStream {
			if s.Status == StatusWorking || s.Status == StatusQuestionWaiting || s.Status == StatusIdle {
				if !m.tmux.HasSessionByFullName(s.TmuxSession) {
					s.Status = StatusStopped
					m.db.Exec("UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?", string(StatusStopped), time.Now(), s.ID)
				}
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
	var outputMode string
	var prompt, currentTask, goal, goalsJSON sql.NullString
	err := m.db.QueryRow(
		`SELECT id, name, project_path, initial_prompt, tmux_session, status, type, output_mode, current_task, goal, goals, created_at, updated_at
		 FROM sessions WHERE id = ?`, id,
	).Scan(&s.ID, &s.Name, &s.ProjectPath, &prompt, &s.TmuxSession, &status, &sessionType, &outputMode, &currentTask, &goal, &goalsJSON, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("query session: %w", err)
	}
	s.Status = Status(status)
	s.Type = SessionType(sessionType)
	s.OutputMode = OutputMode(outputMode)
	if prompt.Valid {
		s.InitialPrompt = prompt.String
	}
	if currentTask.Valid {
		s.CurrentTask = currentTask.String
	}
	if goal.Valid {
		s.Goal = goal.String
	}
	if goalsJSON.Valid {
		s.Goals = ParseGoalStack(goalsJSON.String)
	}
	return &s, nil
}

func (m *Manager) Stop(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}

	// Stop stream process if exists
	m.stopStreamProcess(id)

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

	// Stop stream process if exists
	m.stopStreamProcess(id)

	// Kill tmux session if it exists
	name := s.TmuxSession[len(tmux.SessionPrefix):]
	if m.tmux.HasSession(name) {
		_ = m.tmux.KillSession(name)
	}

	_, err = m.db.Exec("DELETE FROM sessions WHERE id = ?", id)
	return err
}

func (m *Manager) stopStreamProcess(id string) {
	m.streamMu.Lock()
	sp, ok := m.streamProcesses[id]
	if ok {
		delete(m.streamProcesses, id)
	}
	m.streamMu.Unlock()
	if ok {
		sp.Stop()
	}
}

// Clear resets the session context by stopping the current process,
// clearing the stream history, and restarting without --resume.
func (m *Manager) Clear(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}

	// Stop existing stream process if any
	m.stopStreamProcess(id)

	// Kill existing tmux session
	name := s.TmuxSession[len(tmux.SessionPrefix):]
	if m.tmux.HasSession(name) {
		_ = m.tmux.KillSession(name)
	}

	// Clear the JSONL stream file (truncate)
	if s.OutputMode == OutputModeStream {
		streamsDir, err := db.StreamsDir()
		if err == nil {
			streamPath := filepath.Join(streamsDir, id+".jsonl")
			_ = os.Truncate(streamPath, 0)
		}
	}

	// Generate fresh MCP config
	mcpConfigPath, err := writeMCPConfig(id, m.apiPort)
	if err != nil {
		return fmt.Errorf("write mcp config: %w", err)
	}

	// Recreate tmux session
	if err := m.tmux.NewSession(name, s.ProjectPath, ""); err != nil {
		return fmt.Errorf("create tmux session: %w", err)
	}

	if s.OutputMode == OutputModeStream {
		// Start fresh with a new CLI session ID to avoid resuming the old conversation
		freshCLISessionID := uuid.New().String()
		sp, err := StartStreamProcess(id, s.ProjectPath, mcpConfigPath, false, freshCLISessionID)
		if err != nil {
			return fmt.Errorf("start stream process: %w", err)
		}
		m.streamMu.Lock()
		m.streamProcesses[id] = sp
		m.streamMu.Unlock()
	} else {
		time.Sleep(300 * time.Millisecond)
		claudeCmd := m.claudeCommand + " --session-id " + id + " --mcp-config " + mcpConfigPath + " --append-system-prompt " + shellQuote(agmuxSystemPrompt)
		if err := m.tmux.SendKeysOnce(s.TmuxSession, claudeCmd); err != nil {
			return fmt.Errorf("launch claude: %w", err)
		}
	}

	// Reset task/goal and set status to working
	_, err = m.db.Exec("UPDATE sessions SET status = ?, current_task = NULL, goal = NULL, goals = NULL, updated_at = ? WHERE id = ?", string(StatusWorking), time.Now(), id)
	return err
}

func (m *Manager) SendKeys(id string, text string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	if s.OutputMode == OutputModeStream {
		m.streamMu.Lock()
		sp, ok := m.streamProcesses[id]
		m.streamMu.Unlock()
		if !ok {
			// Auto-recover: restart stream process after server restart
			mcpPath, _ := writeMCPConfig(s.ID, m.apiPort)
			claudeSessionID := ReadClaudeSessionID(s.ID)
			var err error
			sp, err = StartStreamProcess(s.ID, s.ProjectPath, mcpPath, true, claudeSessionID)
			if err != nil {
				return fmt.Errorf("restart stream process: %w", err)
			}
			m.streamMu.Lock()
			m.streamProcesses[id] = sp
			m.streamMu.Unlock()
		}
		return sp.Send(text)
	}
	return m.tmux.SendKeys(s.TmuxSession, text)
}

// GetStreamLines returns the last N lines from the stream process memory,
// falling back to file if no active process (e.g. after server restart).
func (m *Manager) GetStreamLines(id string, limit int) ([]string, error) {
	m.streamMu.Lock()
	sp, ok := m.streamProcesses[id]
	m.streamMu.Unlock()

	if ok {
		return sp.GetLines(limit), nil
	}

	// Fallback: read from file
	return m.readStreamFile(id, limit)
}

func (m *Manager) readStreamFile(id string, limit int) ([]string, error) {
	streamsDir, err := db.StreamsDir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(streamsDir, id+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		return []string{}, nil
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if limit > 0 && len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return lines, nil
}

// GetStreamLinesAfter returns lines added after the given index and the current total.
func (m *Manager) GetStreamLinesAfter(id string, after int) ([]string, int, error) {
	m.streamMu.Lock()
	sp, ok := m.streamProcesses[id]
	m.streamMu.Unlock()

	if ok {
		lines, total := sp.GetLinesAfter(after)
		return lines, total, nil
	}

	// Fallback: read from file
	lines, err := m.readStreamFileAfter(id, after)
	if err != nil {
		return nil, 0, err
	}
	total := after + len(lines)
	return lines, total, nil
}

func (m *Manager) readStreamFileAfter(id string, after int) ([]string, error) {
	streamsDir, err := db.StreamsDir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(streamsDir, id+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		return []string{}, nil
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	lineIdx := 0
	for scanner.Scan() {
		if lineIdx >= after {
			lines = append(lines, scanner.Text())
		}
		lineIdx++
	}
	return lines, nil
}

func (m *Manager) CaptureOutput(id string) (string, error) {
	s, err := m.Get(id)
	if err != nil {
		return "", err
	}
	if s.OutputMode == OutputModeStream {
		return "", nil
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
	var outputMode string
	var prompt, currentTask, goal, goalsJSON sql.NullString
	err := m.db.QueryRow(
		`SELECT id, name, project_path, initial_prompt, tmux_session, status, type, output_mode, current_task, goal, goals, created_at, updated_at
		 FROM sessions WHERE type = ? LIMIT 1`, string(TypeController),
	).Scan(&s.ID, &s.Name, &s.ProjectPath, &prompt, &s.TmuxSession, &status, &sessionType, &outputMode, &currentTask, &goal, &goalsJSON, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query controller session: %w", err)
	}
	s.Status = Status(status)
	s.Type = SessionType(sessionType)
	s.OutputMode = OutputMode(outputMode)
	if prompt.Valid {
		s.InitialPrompt = prompt.String
	}
	if currentTask.Valid {
		s.CurrentTask = currentTask.String
	}
	if goal.Valid {
		s.Goal = goal.String
	}
	if goalsJSON.Valid {
		s.Goals = ParseGoalStack(goalsJSON.String)
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
	if existing != nil && (existing.Status == StatusWorking || existing.Status == StatusIdle || existing.Status == StatusQuestionWaiting) {
		// Already running, return existing
		return existing, nil
	}

	// If a stopped controller exists, delete it first
	if existing != nil {
		_ = m.Delete(existing.ID)
	}

	id := uuid.New().String()
	name := "controller"
	tmuxSession := tmux.SessionPrefix + name

	mcpConfigPath, err := writeMCPConfig(id, m.apiPort)
	if err != nil {
		return nil, fmt.Errorf("write mcp config: %w", err)
	}

	if err := m.tmux.NewSession(name, projectPath, ""); err != nil {
		return nil, fmt.Errorf("create controller tmux session: %w", err)
	}

	time.Sleep(300 * time.Millisecond)
	claudeCmd := m.claudeCommand + " --session-id " + id + " --mcp-config " + mcpConfigPath + " --append-system-prompt " + shellQuote(agmuxSystemPrompt)
	if err := m.tmux.SendKeysOnce(tmuxSession, claudeCmd); err != nil {
		return nil, fmt.Errorf("launch claude for controller: %w", err)
	}

	now := time.Now()
	s := &Session{
		ID:          id,
		Name:        name,
		ProjectPath: projectPath,
		TmuxSession: tmuxSession,
		Status:      StatusWorking,
		Type:        TypeController,
		OutputMode:  OutputModeTerminal,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err = m.db.Exec(
		`INSERT INTO sessions (id, name, project_path, initial_prompt, tmux_session, status, type, output_mode, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, s.ProjectPath, s.InitialPrompt, s.TmuxSession, string(s.Status), string(s.Type), string(s.OutputMode), s.CreatedAt, s.UpdatedAt,
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

func (m *Manager) UpdateContext(id string, currentTask, goal string) error {
	_, err := m.db.Exec("UPDATE sessions SET current_task = ?, goal = ?, updated_at = ? WHERE id = ?", currentTask, goal, time.Now(), id)
	return err
}

func (m *Manager) CreateGoal(id string, currentTask, goal string, subgoal bool) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}

	var newGoals GoalStack
	if subgoal {
		newGoals = s.Goals.Push(GoalEntry{CurrentTask: currentTask, Goal: goal})
	} else {
		newGoals = GoalStack{GoalEntry{CurrentTask: currentTask, Goal: goal}}
	}

	_, err = m.db.Exec(
		"UPDATE sessions SET current_task = ?, goal = ?, goals = ?, updated_at = ? WHERE id = ?",
		currentTask, goal, newGoals.ToJSON(), time.Now(), id,
	)
	return err
}

func (m *Manager) CompleteGoal(id string) (*GoalEntry, error) {
	s, err := m.Get(id)
	if err != nil {
		return nil, err
	}

	newGoals := s.Goals.Pop()

	var currentTask, goal string
	if top := newGoals.Top(); top != nil {
		currentTask = top.CurrentTask
		goal = top.Goal
	}

	_, err = m.db.Exec(
		"UPDATE sessions SET current_task = ?, goal = ?, goals = ?, updated_at = ? WHERE id = ?",
		currentTask, goal, newGoals.ToJSON(), time.Now(), id,
	)
	if err != nil {
		return nil, err
	}

	if top := newGoals.Top(); top != nil {
		return top, nil
	}
	return nil, nil
}

// Reconnect restarts the Claude process for an existing session,
// preserving the Claude session ID (--resume) and injecting a fresh MCP config.
func (m *Manager) Reconnect(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}

	// Stop existing stream process if any
	m.stopStreamProcess(id)

	// Kill existing tmux session
	name := s.TmuxSession[len(tmux.SessionPrefix):]
	if m.tmux.HasSession(name) {
		_ = m.tmux.KillSession(name)
	}

	// Generate fresh MCP config
	mcpConfigPath, err := writeMCPConfig(id, m.apiPort)
	if err != nil {
		return fmt.Errorf("write mcp config: %w", err)
	}

	// Recreate tmux session
	if err := m.tmux.NewSession(name, s.ProjectPath, ""); err != nil {
		return fmt.Errorf("create tmux session: %w", err)
	}

	if s.OutputMode == OutputModeStream {
		claudeSessionID := ReadClaudeSessionID(id)
		sp, err := StartStreamProcess(id, s.ProjectPath, mcpConfigPath, true, claudeSessionID)
		if err != nil {
			return fmt.Errorf("start stream process: %w", err)
		}
		m.streamMu.Lock()
		m.streamProcesses[id] = sp
		m.streamMu.Unlock()
	} else {
		time.Sleep(300 * time.Millisecond)
		claudeCmd := m.claudeCommand + " --resume --session-id " + id + " --mcp-config " + mcpConfigPath + " --append-system-prompt " + shellQuote(agmuxSystemPrompt)
		if err := m.tmux.SendKeysOnce(s.TmuxSession, claudeCmd); err != nil {
			return fmt.Errorf("launch claude: %w", err)
		}
	}

	_, err = m.db.Exec("UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?", string(StatusWorking), time.Now(), id)
	return err
}
