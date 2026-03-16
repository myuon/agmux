package session

import (
	"bufio"
	"database/sql"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/myuon/agmux/internal/db"
)

type Manager struct {
	db              *sql.DB
	claudeCommand   string
	codexCommand    string
	apiPort         int
	systemPrompt    string
	streamProcesses map[string]*StreamProcess
	streamMu        sync.Mutex
	logger          *slog.Logger
	onNewLines      func(sessionID string, newLines []string, total int)
}

func NewManager(db *sql.DB, claudeCommand string, apiPort int, logger *slog.Logger, systemPrompt string) *Manager {
	if claudeCommand == "" {
		claudeCommand = "claude --dangerously-skip-permissions"
	}
	if logger == nil {
		logger = slog.Default()
	}
	if systemPrompt == "" {
		systemPrompt = defaultSystemPrompt
	}
	return &Manager{
		db:              db,
		claudeCommand:   claudeCommand,
		codexCommand:    "codex",
		apiPort:         apiPort,
		systemPrompt:    systemPrompt,
		streamProcesses: make(map[string]*StreamProcess),
		logger:          logger.With("component", "session_manager"),
	}
}

// SetCodexCommand sets the codex command for the manager.
func (m *Manager) SetCodexCommand(cmd string) {
	if cmd != "" {
		m.codexCommand = cmd
	}
}

// SetOnNewLines sets a callback that fires when new stream lines arrive for any session.
func (m *Manager) SetOnNewLines(fn func(sessionID string, newLines []string, total int)) {
	m.onNewLines = fn
}

// getProvider returns a Provider for the given provider name.
func (m *Manager) getProvider(name ProviderName) Provider {
	switch name {
	case ProviderCodex:
		return GetProvider(ProviderCodex, m.codexCommand)
	default:
		return GetProvider(ProviderClaude, m.claudeCommand)
	}
}

// SystemPrompt returns the system prompt used for sessions.
func (m *Manager) SystemPrompt() string {
	return m.systemPrompt
}

// RecoverStreamProcesses restarts stream processes for all working sessions.
// This should be called at server startup to recover sessions that were running
// before the server was restarted.
func (m *Manager) RecoverStreamProcesses() {
	rows, err := m.db.Query(
		`SELECT id, project_path, provider, cli_session_id, model FROM sessions WHERE status = ?`,
		string(StatusWorking),
	)
	if err != nil {
		m.logger.Error("recover stream processes: query failed", "error", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id, projectPath, providerStr, dbCliSessionID, dbModel string
		if err := rows.Scan(&id, &projectPath, &providerStr, &dbCliSessionID, &dbModel); err != nil {
			m.logger.Error("recover stream processes: scan failed", "error", err)
			continue
		}
		provider := m.getProvider(ProviderName(providerStr))
		mcpPath, err := provider.SetupMCP(id, m.apiPort)
		if err != nil {
			m.logger.Error("recover stream processes: mcp config failed", "sessionId", id, "error", err)
			continue
		}
		// Prefer DB-stored cli_session_id; fall back to JSONL file scan
		cliSessionID := dbCliSessionID
		if cliSessionID == "" {
			cliSessionID = ReadCLISessionID(id, provider)
		}
		// Backfill model from JSONL if not stored in DB
		if dbModel == "" {
			if model := ReadModelFromStream(id, provider); model != "" {
				dbModel = model
				_ = m.UpdateModel(id, model)
			}
		}
		sp, err := StartStreamProcessWithOpts(StreamOpts{
			SessionID:     id,
			ProjectPath:   projectPath,
			MCPConfigPath: mcpPath,
			SystemPrompt:  m.systemPrompt,
			Resume:        true,
			CLISessionID:  cliSessionID,
			Model:         dbModel,
			APIPort:       m.apiPort,
		}, provider)
		if err != nil {
			m.logger.Error("recover stream processes: start failed", "sessionId", id, "error", err)
			continue
		}
		m.wireSessionIDCallback(id, sp)
		m.streamMu.Lock()
		m.streamProcesses[id] = sp
		m.streamMu.Unlock()
		m.logger.Info("recovered stream process", "sessionId", id)
	}
}

// CreateOpts contains optional parameters for creating a session.
type CreateOpts struct {
	Provider ProviderName
	Model    string
	FullAuto bool // enable full-auto mode (bypasses permission prompts for Codex)
}

func (m *Manager) Create(name, projectPath, prompt string, worktree bool, opts ...CreateOpts) (*Session, error) {
	pn := ProviderClaude
	model := ""
	fullAuto := false
	if len(opts) > 0 {
		if opts[0].Provider != "" {
			pn = opts[0].Provider
		}
		model = opts[0].Model
		fullAuto = opts[0].FullAuto
	}

	// For Codex, resolve default model from config if not explicitly specified
	if pn == ProviderCodex && model == "" {
		model = ReadCodexDefaultModel()
	}

	provider := m.getProvider(pn)

	id := uuid.New().String()

	// Generate MCP config for this session
	mcpConfigPath, err := provider.SetupMCP(id, m.apiPort)
	if err != nil {
		return nil, fmt.Errorf("write mcp config: %w", err)
	}

	// Start stream process
	streamOpts := StreamOpts{
		SessionID:     id,
		ProjectPath:   projectPath,
		MCPConfigPath: mcpConfigPath,
		SystemPrompt:  m.systemPrompt,
		Resume:        false,
		Worktree:      worktree,
		Model:         model,
		FullAuto:      fullAuto,
	}
	streamOpts.APIPort = m.apiPort
	var sp *StreamProcess
	if pn == ProviderCodex && prompt != "" {
		// Codex: pass initial prompt as command-line argument (not stdin)
		streamOpts.InitialPrompt = prompt
		var err error
		sp, err = StartStreamProcessWithOpts(streamOpts, provider)
		if err != nil {
			return nil, fmt.Errorf("start stream process: %w", err)
		}
		// Record the user message in the stream for UI display
		sp.recordUserMessage(prompt)
	} else {
		var err error
		sp, err = StartStreamProcessWithOpts(streamOpts, provider)
		if err != nil {
			return nil, fmt.Errorf("start stream process: %w", err)
		}
		// Send initial prompt via stream process (stdin for Claude)
		if prompt != "" {
			if err := sp.Send(prompt); err != nil {
				return nil, fmt.Errorf("send initial prompt: %w", err)
			}
		}
	}
	m.wireSessionIDCallback(id, sp)
	m.streamMu.Lock()
	m.streamProcesses[id] = sp
	m.streamMu.Unlock()

	now := time.Now()
	s := &Session{
		ID:            id,
		Name:          name,
		ProjectPath:   projectPath,
		InitialPrompt: prompt,
		Status:        StatusWorking,
		Type:          TypeWorker,
		Provider:      pn,
		Model:         model,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if _, err := m.db.Exec(
		`INSERT INTO sessions (id, name, project_path, initial_prompt, tmux_session, status, type, output_mode, provider, model, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, s.ProjectPath, s.InitialPrompt, "", string(s.Status), string(s.Type), "stream", string(s.Provider), s.Model, s.CreatedAt, s.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("insert session: %w", err)
	}

	return s, nil
}

func (m *Manager) List() ([]Session, error) {
	rows, err := m.db.Query(
		`SELECT id, name, project_path, initial_prompt, status, type, provider, cli_session_id, model, current_task, goal, goals, created_at, updated_at
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
		var providerStr string
		var prompt, currentTask, goal, goalsJSON sql.NullString
		if err := rows.Scan(&s.ID, &s.Name, &s.ProjectPath, &prompt, &status, &sessionType, &providerStr, &s.CliSessionID, &s.Model, &currentTask, &goal, &goalsJSON, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		s.Status = Status(status)
		s.Type = SessionType(sessionType)
		s.Provider = ProviderName(providerStr)
		if s.Provider == "" {
			s.Provider = ProviderClaude
		}
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

		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// ResolveID resolves a (possibly abbreviated) session ID prefix to a full ID.
// Returns an error if the prefix matches zero or multiple sessions.
func (m *Manager) ResolveID(prefix string) (string, error) {
	rows, err := m.db.Query(`SELECT id FROM sessions WHERE id LIKE ?`, prefix+"%")
	if err != nil {
		return "", fmt.Errorf("query sessions: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", fmt.Errorf("scan session id: %w", err)
		}
		ids = append(ids, id)
	}

	switch len(ids) {
	case 0:
		return "", fmt.Errorf("session not found: %s", prefix)
	case 1:
		return ids[0], nil
	default:
		return "", fmt.Errorf("ambiguous session ID prefix '%s' matches %d sessions", prefix, len(ids))
	}
}

func (m *Manager) Get(id string) (*Session, error) {
	var s Session
	var status string
	var sessionType string
	var providerStr string
	var prompt, currentTask, goal, goalsJSON sql.NullString
	err := m.db.QueryRow(
		`SELECT id, name, project_path, initial_prompt, status, type, provider, cli_session_id, model, current_task, goal, goals, created_at, updated_at
		 FROM sessions WHERE id = ?`, id,
	).Scan(&s.ID, &s.Name, &s.ProjectPath, &prompt, &status, &sessionType, &providerStr, &s.CliSessionID, &s.Model, &currentTask, &goal, &goalsJSON, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("query session: %w", err)
	}
	s.Status = Status(status)
	s.Type = SessionType(sessionType)
	s.Provider = ProviderName(providerStr)
	if s.Provider == "" {
		s.Provider = ProviderClaude
	}
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

// Duplicate creates a new session by copying configuration from an existing session.
// It copies Name (with " (copy)" suffix), ProjectPath, Provider, and Model.
// It does NOT copy InitialPrompt, Status, CurrentTask, Goal, Goals, or CliSessionID.
func (m *Manager) Duplicate(id string) (*Session, error) {
	src, err := m.Get(id)
	if err != nil {
		return nil, err
	}

	newName := src.Name + " (copy)"
	return m.Create(newName, src.ProjectPath, "", false, CreateOpts{
		Provider: src.Provider,
		Model:    src.Model,
	})
}

func (m *Manager) Stop(id string) error {
	_, err := m.Get(id)
	if err != nil {
		return err
	}

	// Stop stream process if exists
	m.stopStreamProcess(id)

	_, err = m.db.Exec("UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?", string(StatusStopped), time.Now(), id)
	return err
}

func (m *Manager) Delete(id string) error {
	_, err := m.Get(id)
	if err != nil {
		return err
	}

	// Stop stream process if exists
	m.stopStreamProcess(id)

	_, err = m.db.Exec("DELETE FROM sessions WHERE id = ?", id)
	return err
}

// wireSessionIDCallback sets up the onSessionID callback on a stream process
// so that when the CLI session ID is captured, it gets persisted to the DB.
func (m *Manager) wireSessionIDCallback(sessionID string, sp *StreamProcess) {
	sp.SetOnSessionID(func(cliSessionID string) {
		if err := m.UpdateCliSessionID(sessionID, cliSessionID); err != nil {
			m.logger.Error("failed to persist cli session id", "sessionId", sessionID, "cliSessionId", cliSessionID, "error", err)
		}
	})
	sp.SetOnModel(func(model string) {
		if err := m.UpdateModel(sessionID, model); err != nil {
			m.logger.Error("failed to persist model name", "sessionId", sessionID, "model", model, "error", err)
		} else {
			m.logger.Info("model name captured from stream", "sessionId", sessionID, "model", model)
		}
	})
	if m.onNewLines != nil {
		sp.SetOnNewLines(m.onNewLines)
		m.logger.Info("onNewLines callback set for session", "sessionId", sessionID)
	} else {
		m.logger.Warn("onNewLines callback is nil, WebSocket updates will not work", "sessionId", sessionID)
	}
	sp.SetOnProcessExit(func(sid string, exitErr error) {
		// For Codex provider, exit code 0 is normal (exec finishes after each prompt).
		// Keep the session running and the stream process in the map so that
		// sendCodex can restart it with "codex exec resume" on the next message.
		if sp.provider.Name() == ProviderCodex && exitErr == nil {
			m.logger.Info("codex process exited normally (exit code 0), keeping session running for resume",
				"sessionId", sid,
			)
			return
		}

		errMsg := "<nil>"
		if exitErr != nil {
			errMsg = exitErr.Error()
		}
		m.logger.Warn("claude process exited unexpectedly, updating status to stopped",
			"sessionId", sid,
			"exitError", errMsg,
		)
		if err := m.UpdateStatus(sid, StatusStopped); err != nil {
			m.logger.Error("failed to update status after process exit", "sessionId", sid, "error", err)
		}
		// Clean up the stream process from the map
		m.streamMu.Lock()
		delete(m.streamProcesses, sid)
		m.streamMu.Unlock()
	})
}

// IsStreamProcessAlive returns true if a stream process exists and has not exited.
func (m *Manager) IsStreamProcessAlive(id string) bool {
	m.streamMu.Lock()
	sp, ok := m.streamProcesses[id]
	m.streamMu.Unlock()
	if !ok {
		return false
	}
	return !sp.IsExited()
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

// StopAllStreamProcesses gracefully stops all running stream processes.
// This should be called during server shutdown to ensure output is flushed.
func (m *Manager) StopAllStreamProcesses() {
	m.streamMu.Lock()
	processes := make(map[string]*StreamProcess, len(m.streamProcesses))
	maps.Copy(processes, m.streamProcesses)
	m.streamProcesses = make(map[string]*StreamProcess)
	m.streamMu.Unlock()

	for id, sp := range processes {
		m.logger.Info("stopping stream process", "sessionId", id)
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

	provider := m.getProvider(s.Provider)

	// Stop existing stream process if any
	m.stopStreamProcess(id)

	// Clear the JSONL stream file (truncate)
	streamsDir, err := db.StreamsDir()
	if err == nil {
		streamPath := filepath.Join(streamsDir, id+".jsonl")
		_ = os.Truncate(streamPath, 0)
	}

	// Generate fresh MCP config
	mcpConfigPath, err := provider.SetupMCP(id, m.apiPort)
	if err != nil {
		return fmt.Errorf("write mcp config: %w", err)
	}

	// Start fresh with a new CLI session ID to avoid resuming the old conversation
	freshCLISessionID := uuid.New().String()
	sp, err := StartStreamProcessWithOpts(StreamOpts{
		SessionID:     id,
		ProjectPath:   s.ProjectPath,
		MCPConfigPath: mcpConfigPath,
		SystemPrompt:  m.systemPrompt,
		CLISessionID:  freshCLISessionID,
		Model:         s.Model,
		APIPort:       m.apiPort,
	}, provider)
	if err != nil {
		return fmt.Errorf("start stream process: %w", err)
	}
	m.wireSessionIDCallback(id, sp)
	m.streamMu.Lock()
	m.streamProcesses[id] = sp
	m.streamMu.Unlock()

	// Reset task/goal/cli_session_id and set status to working
	_, err = m.db.Exec("UPDATE sessions SET status = ?, current_task = NULL, goal = NULL, goals = '[]', cli_session_id = '', updated_at = ? WHERE id = ?", string(StatusWorking), time.Now(), id)
	return err
}

func (m *Manager) SendKeys(id string, text string) error {
	return m.SendKeysWithImages(id, text, nil)
}

func (m *Manager) SendKeysWithImages(id string, text string, images []ImageData) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	m.streamMu.Lock()
	sp, ok := m.streamProcesses[id]
	m.streamMu.Unlock()
	if !ok {
		// Auto-recover: restart stream process after server restart
		provider := m.getProvider(s.Provider)
		mcpPath, _ := provider.SetupMCP(s.ID, m.apiPort)
		// Prefer DB-stored cli_session_id; fall back to JSONL file scan
		cliSessionID := s.CliSessionID
		if cliSessionID == "" {
			cliSessionID = ReadCLISessionID(s.ID, provider)
		}

		var err error
		if s.Provider == ProviderCodex && cliSessionID != "" {
			// For Codex, start resume directly with the prompt as a positional arg.
			// Codex exec exits after processing, so we can't send via stdin later.
			sp, err = StartStreamProcessWithOpts(StreamOpts{
				SessionID:     s.ID,
				ProjectPath:   s.ProjectPath,
				MCPConfigPath: mcpPath,
				SystemPrompt:  m.systemPrompt,
				InitialPrompt: text,
				Resume:        true,
				CLISessionID:  cliSessionID,
				Model:         s.Model,
				APIPort:       m.apiPort,
			}, provider)
			if err != nil {
				return fmt.Errorf("restart stream process: %w", err)
			}
			sp.recordUserMessage(text)
			m.wireSessionIDCallback(id, sp)
			m.streamMu.Lock()
			m.streamProcesses[id] = sp
			m.streamMu.Unlock()
			return nil
		}

		sp, err = StartStreamProcessWithOpts(StreamOpts{
			SessionID:     s.ID,
			ProjectPath:   s.ProjectPath,
			MCPConfigPath: mcpPath,
			SystemPrompt:  m.systemPrompt,
			Resume:        true,
			CLISessionID:  cliSessionID,
			Model:         s.Model,
		}, provider)
		if err != nil {
			return fmt.Errorf("restart stream process: %w", err)
		}
		m.wireSessionIDCallback(id, sp)
		m.streamMu.Lock()
		m.streamProcesses[id] = sp
		m.streamMu.Unlock()
	}
	return sp.SendWithImages(text, images)
}

// GetStreamLines returns the last N lines from the stream process memory,
// falling back to file if no active process (e.g. after server restart).
// Also returns the total line count so callers can use it as a cursor for delta fetches.
func (m *Manager) GetStreamLines(id string, limit int) ([]string, int, error) {
	m.streamMu.Lock()
	sp, ok := m.streamProcesses[id]
	m.streamMu.Unlock()

	if ok {
		total := sp.TotalLines()
		return sp.GetLines(limit), total, nil
	}

	// Fallback: read all lines from file, then truncate to limit
	allLines, err := m.readStreamFile(id, 0)
	if err != nil {
		return nil, 0, err
	}
	total := len(allLines)
	if limit > 0 && len(allLines) > limit {
		allLines = allLines[len(allLines)-limit:]
	}
	return allLines, total, nil
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

// GetController returns the controller session if it exists.
func (m *Manager) GetController() (*Session, error) {
	var s Session
	var status string
	var sessionType string
	var providerStr string
	var prompt, currentTask, goal, goalsJSON sql.NullString
	err := m.db.QueryRow(
		`SELECT id, name, project_path, initial_prompt, status, type, provider, cli_session_id, model, current_task, goal, goals, created_at, updated_at
		 FROM sessions WHERE type = ? LIMIT 1`, string(TypeController),
	).Scan(&s.ID, &s.Name, &s.ProjectPath, &prompt, &status, &sessionType, &providerStr, &s.CliSessionID, &s.Model, &currentTask, &goal, &goalsJSON, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query controller session: %w", err)
	}
	s.Status = Status(status)
	s.Type = SessionType(sessionType)
	s.Provider = ProviderName(providerStr)
	if s.Provider == "" {
		s.Provider = ProviderClaude
	}
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

	provider := m.getProvider(ProviderClaude)

	id := uuid.New().String()
	name := "controller"

	mcpConfigPath, err := provider.SetupMCP(id, m.apiPort)
	if err != nil {
		return nil, fmt.Errorf("write mcp config: %w", err)
	}

	// Start stream process
	sp, err := StartStreamProcessWithOpts(StreamOpts{
		SessionID:     id,
		ProjectPath:   projectPath,
		MCPConfigPath: mcpConfigPath,
		SystemPrompt:  m.systemPrompt,
		APIPort:       m.apiPort,
	}, provider)
	if err != nil {
		return nil, fmt.Errorf("start stream process for controller: %w", err)
	}
	m.wireSessionIDCallback(id, sp)
	m.streamMu.Lock()
	m.streamProcesses[id] = sp
	m.streamMu.Unlock()

	now := time.Now()
	s := &Session{
		ID:          id,
		Name:        name,
		ProjectPath: projectPath,
		Status:      StatusWorking,
		Type:        TypeController,
		Provider:    ProviderClaude,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err = m.db.Exec(
		`INSERT INTO sessions (id, name, project_path, initial_prompt, tmux_session, status, type, output_mode, provider, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, s.ProjectPath, s.InitialPrompt, "", string(s.Status), string(s.Type), "stream", string(s.Provider), s.CreatedAt, s.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert controller session: %w", err)
	}

	return s, nil
}

func (m *Manager) UpdateStatus(id string, status Status) error {
	_, err := m.db.Exec("UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?", string(status), time.Now(), id)
	return err
}

// UpdateCliSessionID persists the CLI-assigned session ID to the database.
func (m *Manager) UpdateCliSessionID(id string, cliSessionID string) error {
	_, err := m.db.Exec("UPDATE sessions SET cli_session_id = ?, updated_at = ? WHERE id = ?", cliSessionID, time.Now(), id)
	return err
}

func (m *Manager) UpdateModel(id string, model string) error {
	_, err := m.db.Exec("UPDATE sessions SET model = ?, updated_at = ? WHERE id = ?", model, time.Now(), id)
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

	entry := GoalEntry{CurrentTask: currentTask, Goal: goal, StartedAt: time.Now()}
	var newGoals GoalStack
	if subgoal {
		newGoals = s.Goals.Push(entry)
	} else {
		newGoals = GoalStack{entry}
	}

	_, err = m.db.Exec(
		"UPDATE sessions SET current_task = ?, goal = ?, goals = ?, updated_at = ? WHERE id = ?",
		currentTask, goal, newGoals.ToJSON(), time.Now(), id,
	)
	return err
}

// CompleteGoalResult contains the result of completing a goal.
type CompleteGoalResult struct {
	CompletedGoal *GoalEntry // the goal that was just completed
	ParentGoal    *GoalEntry // the new top of stack (nil if empty)
}

func (m *Manager) CompleteGoal(id string) (*CompleteGoalResult, error) {
	s, err := m.Get(id)
	if err != nil {
		return nil, err
	}

	completedGoal := s.Goals.Top()
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

	result := &CompleteGoalResult{}
	if completedGoal != nil {
		copied := *completedGoal
		result.CompletedGoal = &copied
	}
	if top := newGoals.Top(); top != nil {
		result.ParentGoal = top
	}
	return result, nil
}

// Reconnect restarts the CLI process for an existing session,
// preserving the CLI session ID (--resume) and injecting a fresh MCP config.
func (m *Manager) Reconnect(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}

	provider := m.getProvider(s.Provider)

	// Stop existing stream process if any
	m.stopStreamProcess(id)

	// Generate fresh MCP config
	mcpConfigPath, err := provider.SetupMCP(id, m.apiPort)
	if err != nil {
		return fmt.Errorf("write mcp config: %w", err)
	}

	// Resume the existing CLI session to preserve conversation history.
	// Prefer DB-stored cli_session_id; fall back to JSONL file scan.
	cliSessionID := s.CliSessionID
	if cliSessionID == "" {
		cliSessionID = ReadCLISessionID(id, provider)
	}
	sp, err := StartStreamProcessWithOpts(StreamOpts{
		SessionID:     id,
		ProjectPath:   s.ProjectPath,
		MCPConfigPath: mcpConfigPath,
		SystemPrompt:  m.systemPrompt,
		Resume:        true,
		CLISessionID:  cliSessionID,
		Model:         s.Model,
		APIPort:       m.apiPort,
	}, provider)
	if err != nil {
		return fmt.Errorf("start stream process: %w", err)
	}
	m.wireSessionIDCallback(id, sp)
	m.streamMu.Lock()
	m.streamProcesses[id] = sp
	m.streamMu.Unlock()

	_, err = m.db.Exec("UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?", string(StatusWorking), time.Now(), id)
	return err
}
