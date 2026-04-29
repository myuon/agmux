package session

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	gonanoid "github.com/matoous/go-nanoid/v2"
	"github.com/myuon/agmux/internal/db"
)

type Manager struct {
	db                 *sql.DB
	claudeCommand      string
	codexCommand       string
	permissionMode     string
	apiPort            int
	systemPrompt       string
	claudeDefaultModel string
	codexDefaultModel  string
	notifyInterval     time.Duration
	streamProcesses    map[string]*HolderStreamProcess
	streamMu           sync.Mutex
	// deletingSet tracks sessions currently being deleted so that auto-recovery
	// (RecoverStreamProcesses, SendKeysWithImages) does not spawn a new holder
	// while Delete() is in progress.
	deletingSet map[string]struct{}
	deletingMu  sync.Mutex
	// spawnMu prevents concurrent holder spawns for the same session in SendKeysWithImages.
	spawnMu sync.Mutex
	// ephemeralCancels stores cancel functions for active ephemeral timeout goroutines.
	// When a session is manually stopped/deleted/archived, its cancel func is invoked
	// so the goroutine exits immediately rather than leaking until the timer fires.
	ephemeralCancels   map[string]context.CancelFunc
	ephemeralCancelsMu sync.Mutex
	logger             *slog.Logger
	onNewLines     func(sessionID string, newLines []string, total int)
	onStatusChange func(sessionID string, status Status, lastError string)
	backgroundTasks *BackgroundTaskStore
}

const nanoidAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"

func newSessionID() (string, error) {
	return gonanoid.Generate(nanoidAlphabet, 5)
}

func NewManager(db *sql.DB, claudeCommand string, permissionMode string, apiPort int, logger *slog.Logger, systemPrompt string) *Manager {
	if claudeCommand == "" {
		claudeCommand = "claude"
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
		permissionMode:  permissionMode,
		apiPort:         apiPort,
		systemPrompt:    systemPrompt,
		streamProcesses:  make(map[string]*HolderStreamProcess),
		deletingSet:      make(map[string]struct{}),
		ephemeralCancels: make(map[string]context.CancelFunc),
		logger:           logger.With("component", "session_manager"),
		backgroundTasks:  NewBackgroundTaskStore(db),
	}
}

// BackgroundTasks returns the manager's background task store.
func (m *Manager) BackgroundTasks() *BackgroundTaskStore {
	return m.backgroundTasks
}

// ListBackgroundTasks returns the persisted background tasks for the session.
func (m *Manager) ListBackgroundTasks(sessionID string) ([]BackgroundTask, error) {
	if m.backgroundTasks == nil {
		return []BackgroundTask{}, nil
	}
	return m.backgroundTasks.List(sessionID)
}

// ManagedHolderPIDs returns the PIDs of all holder processes currently managed by this Manager.
// DB holder_pid is always positive for active sessions, so querying the DB is sufficient.
func (m *Manager) ManagedHolderPIDs() []int {
	rows, err := m.db.Query("SELECT holder_pid FROM sessions WHERE holder_pid > 0")
	if err != nil {
		return nil
	}
	defer rows.Close()

	var pids []int
	for rows.Next() {
		var pid int
		if err := rows.Scan(&pid); err == nil {
			pids = append(pids, pid)
		}
	}
	if rows.Err() != nil {
		return nil
	}
	return pids
}

// SetCodexCommand sets the codex command for the manager.
func (m *Manager) SetCodexCommand(cmd string) {
	if cmd != "" {
		m.codexCommand = cmd
	}
}

// SetDefaultModels configures provider-specific default models.
func (m *Manager) SetDefaultModels(claudeDefaultModel, codexDefaultModel string) {
	m.claudeDefaultModel = claudeDefaultModel
	m.codexDefaultModel = codexDefaultModel
}

// SetOnNewLines sets a callback that fires when new stream lines arrive for any session.
func (m *Manager) SetOnNewLines(fn func(sessionID string, newLines []string, total int)) {
	m.onNewLines = fn
}

// SetOnStatusChange sets a callback that fires when a session status changes.
func (m *Manager) SetOnStatusChange(fn func(sessionID string, status Status, lastError string)) {
	m.onStatusChange = fn
}

// getProvider returns a Provider for the given provider name.
func (m *Manager) getProvider(name ProviderName) Provider {
	switch name {
	case ProviderCodex:
		return GetProvider(ProviderCodex, m.codexCommand, "")
	default:
		return GetProvider(ProviderClaude, m.claudeCommand, m.permissionMode)
	}
}

// SystemPrompt returns the system prompt used for sessions.
func (m *Manager) SystemPrompt() string {
	return m.systemPrompt
}

// buildEffectiveSystemPrompt returns the effective system prompt by combining
// the global default system prompt with a per-session custom system prompt.
func (m *Manager) buildEffectiveSystemPrompt(customSystemPrompt string) string {
	if customSystemPrompt == "" {
		return m.systemPrompt
	}
	return m.systemPrompt + "\n\n" + customSystemPrompt
}

// RecoverStreamProcesses recovers stream processes for all sessions with holder_pid > 0.
// If a holder process is still alive (survived server restart), reconnect to it.
// Otherwise, start a new holder process with --resume.
func (m *Manager) RecoverStreamProcesses() {
	rows, err := m.db.Query(
		`SELECT id, project_path, provider, cli_session_id, model, system_prompt, holder_pid, clear_offset, conversation_started FROM sessions WHERE holder_pid > 0`,
	)
	if err != nil {
		m.logger.Error("recover stream processes: query failed", "error", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id, projectPath, providerStr, dbCliSessionID, dbModel string
		var dbSystemPrompt sql.NullString
		var holderPID int
		var clearOffset int64
		var conversationStartedInt int
		if err := rows.Scan(&id, &projectPath, &providerStr, &dbCliSessionID, &dbModel, &dbSystemPrompt, &holderPID, &clearOffset, &conversationStartedInt); err != nil {
			m.logger.Error("recover stream processes: scan failed", "error", err)
			continue
		}
		conversationStarted := conversationStartedInt != 0

		// Skip sessions currently being deleted to avoid spawning a new holder
		// in the window between Delete() killing the old holder and removing the DB record.
		m.deletingMu.Lock()
		_, isDeleting := m.deletingSet[id]
		m.deletingMu.Unlock()
		if isDeleting {
			m.logger.Info("recover: skipping session currently being deleted", "sessionId", id)
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
		customPrompt := ""
		if dbSystemPrompt.Valid {
			customPrompt = dbSystemPrompt.String
		}

		opts := StreamOpts{
			SessionID:     id,
			ProjectPath:   projectPath,
			MCPConfigPath: mcpPath,
			SystemPrompt:  m.buildEffectiveSystemPrompt(customPrompt),
			Resume:        conversationStarted,
			CLISessionID:  cliSessionID,
			Model:         dbModel,
			APIPort:       m.apiPort,
			ClearOffset:   clearOffset,
		}

		// Rebuild background_tasks DB from the JSONL so the API reflects
		// the current task list before any new live lines arrive.
		if m.backgroundTasks != nil {
			if err := m.backgroundTasks.RebuildFromJSONL(id, clearOffset); err != nil {
				m.logger.Warn("failed to rebuild background tasks from JSONL", "sessionId", id, "error", err)
			}
		}

		// Try to reconnect to existing holder first
		if holderPID > 0 && IsHolderAlive(holderPID) {
			m.logger.Info("holder still alive, reconnecting", "sessionId", id, "holderPid", holderPID)
			sp, err := ReconnectHolderStreamProcess(opts, provider, holderPID)
			if err != nil {
				m.logger.Warn("reconnect to holder failed, will start new holder", "sessionId", id, "error", err)
				m.killStaleHolder(id)
			} else {
				m.wireSessionIDCallback(id, sp)
				m.streamMu.Lock()
				m.streamProcesses[id] = sp
				m.streamMu.Unlock()
				m.logger.Info("reconnected to existing holder", "sessionId", id, "holderPid", holderPID)
				continue
			}
		}

		// Start new holder process
		sp, err := StartHolderStreamProcess(opts, provider)
		if err != nil {
			m.logger.Error("recover stream processes: start holder failed", "sessionId", id, "error", err)
			continue
		}
		m.wireSessionIDCallback(id, sp)
		m.streamMu.Lock()
		m.streamProcesses[id] = sp
		m.streamMu.Unlock()
		if _, err := m.db.Exec("UPDATE sessions SET holder_pid = ?, updated_at = ? WHERE id = ?", sp.HolderPID(), time.Now(), id); err != nil {
			m.logger.Error("failed to update holder_pid", "sessionId", id, "pid", sp.HolderPID(), "error", err)
		}
		m.logger.Info("recovered stream process via new holder", "sessionId", id, "holderPid", sp.HolderPID())
	}
	if err := rows.Err(); err != nil {
		m.logger.Error("recover stream processes: rows iteration error", "error", err)
	}

	// Recover ephemeral timeout goroutines for active ephemeral sessions
	m.recoverEphemeralTimeouts()
}

// ephemeralTimeoutEntry holds the data scanned from a single row in recoverEphemeralTimeouts.
type ephemeralTimeoutEntry struct {
	id             string
	parentID       string
	timeoutSeconds int
	createdAt      time.Time
}

// recoverEphemeralTimeouts scans ephemeral sessions with a timeout that are still
// in an active state (working/idle) and restarts their timeout goroutines with
// the remaining time based on created_at + timeout - now.
func (m *Manager) recoverEphemeralTimeouts() {
	rows, err := m.db.Query(
		`SELECT id, parent_session_id, ephemeral_timeout_seconds, created_at
		 FROM sessions
		 WHERE type = ? AND ephemeral_timeout_seconds IS NOT NULL AND status IN (?, ?)`,
		string(TypeEphemeral), string(StatusWorking), string(StatusIdle),
	)
	if err != nil {
		m.logger.Error("recover ephemeral timeouts: query failed", "error", err)
		return
	}

	// Collect all rows before closing so subsequent DB writes don't compete for the connection.
	var entries []ephemeralTimeoutEntry
	for rows.Next() {
		var id string
		var parentSessionID sql.NullString
		var timeoutSeconds int
		var createdAt time.Time
		if err := rows.Scan(&id, &parentSessionID, &timeoutSeconds, &createdAt); err != nil {
			m.logger.Error("recover ephemeral timeouts: scan failed", "error", err)
			continue
		}
		parentID := ""
		if parentSessionID.Valid {
			parentID = parentSessionID.String
		}
		entries = append(entries, ephemeralTimeoutEntry{
			id:             id,
			parentID:       parentID,
			timeoutSeconds: timeoutSeconds,
			createdAt:      createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		m.logger.Error("recover ephemeral timeouts: rows iteration error", "error", err)
	}
	rows.Close()

	now := time.Now()
	for _, e := range entries {
		deadline := e.createdAt.Add(time.Duration(e.timeoutSeconds) * time.Second)
		remaining := deadline.Sub(now)

		if remaining <= 0 {
			// Already expired, archive immediately
			m.logger.Info("ephemeral session already expired, archiving now",
				"sessionId", e.id, "deadline", deadline)
			m.stopStreamProcess(e.id)
			// Atomically update status and clear holder_pid in one query
			if err := m.UpdateStatusWithHolderPIDClear(e.id, StatusArchived); err != nil {
				m.logger.Error("recover ephemeral timeouts: failed to archive",
					"sessionId", e.id, "error", err)
			}
			if e.parentID != "" {
				timeout := time.Duration(e.timeoutSeconds) * time.Second
				msg := fmt.Sprintf("[system] Ephemeral session %s has been archived due to timeout (%s).", e.id, timeout)
				if err := m.SendKeys(e.parentID, msg); err != nil {
					m.logger.Warn("recover ephemeral timeouts: failed to notify parent",
						"sessionId", e.id, "parentSessionId", e.parentID, "error", err)
				}
			}
		} else {
			m.logger.Info("restarting ephemeral timeout goroutine",
				"sessionId", e.id, "remaining", remaining)
			m.startEphemeralTimeout(e.id, e.parentID, remaining)
		}
	}
	if err := rows.Err(); err != nil {
		m.logger.Error("recover ephemeral timeouts: rows iteration error", "error", err)
	}
}

// updateHolderPID persists the holder PID to the database.
func (m *Manager) updateHolderPID(id string, pid int) {
	if _, err := m.db.Exec("UPDATE sessions SET holder_pid = ?, updated_at = ? WHERE id = ?", pid, time.Now(), id); err != nil {
		m.logger.Error("failed to update holder_pid", "sessionId", id, "pid", pid, "error", err)
	}
}

// killStaleHolder checks the DB for the session's current holder_pid and, if the process
// is still alive, sends SIGTERM and waits for graceful shutdown. Falls back to SIGKILL if
// SIGTERM does not terminate the process within the timeout. This prevents the race where
// SIGKILL leaves claude CLI holding the session lock file, causing "Session ID is already
// in use" when the new holder spawns immediately after.
func (m *Manager) killStaleHolder(sessionID string) {
	var holderPID int
	if err := m.db.QueryRow("SELECT holder_pid FROM sessions WHERE id = ?", sessionID).Scan(&holderPID); err != nil {
		// Session may not exist yet or query failed; nothing to kill
		return
	}
	if holderPID <= 0 {
		return
	}
	if !IsHolderAlive(holderPID) {
		return
	}
	// Send SIGTERM only to the holder process (not the process group).
	// The holder's SIGTERM handler closes stdin, which triggers claude CLI's
	// graceful shutdown and session lock release. Sending SIGTERM to the entire
	// group would kill claude CLI directly (exit 143), leaving the lock file.
	syscall.Kill(holderPID, syscall.SIGTERM)
	waitForProcessExit(holderPID, 5*time.Second, 100*time.Millisecond)
	if !IsHolderAlive(holderPID) {
		m.logger.Info("killStaleHolder: terminated stale holder via SIGTERM", "sessionId", sessionID, "holderPid", holderPID)
		return
	}
	// Fallback: SIGTERM did not work; kill the entire process group
	syscall.Kill(-holderPID, syscall.SIGKILL)
	waitForProcessExit(holderPID, 3*time.Second, 100*time.Millisecond)
	m.logger.Info("killStaleHolder: killed stale holder via SIGKILL (SIGTERM fallback)", "sessionId", sessionID, "holderPid", holderPID)
}

// CreateOpts contains optional parameters for creating a session.
type CreateOpts struct {
	Provider                ProviderName
	Model                   string
	FullAuto                bool   // enable full-auto mode (bypasses permission prompts for Codex)
	SystemPrompt            string // per-session custom system prompt (appended to defaultSystemPrompt)
	ParentSessionID         string // parent session ID for sub-session creation
	RoleTemplate            string // name of the role template used to create this session
	Ephemeral               bool   // create as ephemeral session
	EphemeralTimeoutSeconds *int   // timeout in seconds for ephemeral sessions
}

func (m *Manager) Create(name, projectPath, prompt string, worktree bool, opts ...CreateOpts) (*Session, error) {
	pn := ProviderClaude
	model := ""
	fullAuto := false
	customSystemPrompt := ""
	parentSessionID := ""
	roleTemplate := ""
	ephemeral := false
	var ephemeralTimeoutSeconds *int
	if len(opts) > 0 {
		if opts[0].Provider != "" {
			pn = opts[0].Provider
		}
		model = opts[0].Model
		fullAuto = opts[0].FullAuto
		customSystemPrompt = opts[0].SystemPrompt
		parentSessionID = opts[0].ParentSessionID
		roleTemplate = opts[0].RoleTemplate
		ephemeral = opts[0].Ephemeral
		ephemeralTimeoutSeconds = opts[0].EphemeralTimeoutSeconds
	}

	// Validate parent session exists when creating a sub-session
	if parentSessionID != "" {
		if _, err := m.Get(parentSessionID); err != nil {
			return nil, fmt.Errorf("parent session %s not found", parentSessionID)
		}
	}

	// Resolve default model from config if not explicitly specified
	if model == "" {
		switch pn {
		case ProviderClaude:
			model = m.claudeDefaultModel
		case ProviderCodex:
			if m.codexDefaultModel != "" {
				model = m.codexDefaultModel
			} else {
				model = ReadCodexDefaultModel()
			}
		}
	}

	provider := m.getProvider(pn)

	id, err := newSessionID()
	if err != nil {
		return nil, fmt.Errorf("generate session id: %w", err)
	}

	// Generate MCP config for this session
	mcpConfigPath, err := provider.SetupMCP(id, m.apiPort)
	if err != nil {
		return nil, fmt.Errorf("write mcp config: %w", err)
	}

	effectiveSystemPrompt := m.buildEffectiveSystemPrompt(customSystemPrompt)

	// Start stream process
	streamOpts := StreamOpts{
		SessionID:     id,
		ProjectPath:   projectPath,
		MCPConfigPath: mcpConfigPath,
		SystemPrompt:  effectiveSystemPrompt,
		Resume:        false,
		Worktree:      worktree,
		Model:         model,
		FullAuto:      fullAuto,
	}
	streamOpts.APIPort = m.apiPort
	var sp *HolderStreamProcess
	if pn == ProviderCodex && prompt != "" {
		// Codex: pass initial prompt as command-line argument (not stdin)
		streamOpts.InitialPrompt = prompt
		hsp, err := StartHolderStreamProcess(streamOpts, provider)
		if err != nil {
			return nil, fmt.Errorf("start stream process: %w", err)
		}
		// Record the user message in the stream for UI display
		hsp.recordUserMessage(prompt)
		sp = hsp
	} else {
		hsp, err := StartHolderStreamProcess(streamOpts, provider)
		if err != nil {
			return nil, fmt.Errorf("start stream process: %w", err)
		}
		// Send initial prompt via stream process (stdin for Claude)
		if prompt != "" {
			if err := hsp.Send(prompt); err != nil {
				return nil, fmt.Errorf("send initial prompt: %w", err)
			}
		}
		sp = hsp
	}
	m.wireSessionIDCallback(id, sp)
	m.streamMu.Lock()
	m.streamProcesses[id] = sp
	m.streamMu.Unlock()

	now := time.Now()
	sessionType := TypeWorker
	if ephemeral {
		sessionType = TypeEphemeral
	}
	s := &Session{
		ID:                      id,
		Name:                    name,
		ProjectPath:             projectPath,
		InitialPrompt:           prompt,
		SystemPrompt:            customSystemPrompt,
		Status:                  StatusIdle,
		Type:                    sessionType,
		Provider:                pn,
		Model:                   model,
		ParentSessionID:         parentSessionID,
		RoleTemplate:            roleTemplate,
		EphemeralTimeoutSeconds: ephemeralTimeoutSeconds,
		CreatedAt:               now,
		UpdatedAt:               now,
	}

	if _, err := m.db.Exec(
		`INSERT INTO sessions (id, name, project_path, initial_prompt, tmux_session, status, type, output_mode, provider, model, system_prompt, parent_session_id, role_template, holder_pid, ephemeral_timeout_seconds, completion_report, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, s.ProjectPath, s.InitialPrompt, "", string(s.Status), string(s.Type), "stream", string(s.Provider), s.Model, s.SystemPrompt, s.ParentSessionID, s.RoleTemplate, sp.HolderPID(), s.EphemeralTimeoutSeconds, s.CompletionReport, s.CreatedAt, s.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("insert session: %w", err)
	}

	// Start ephemeral timeout goroutine if applicable
	if ephemeral && ephemeralTimeoutSeconds != nil && *ephemeralTimeoutSeconds > 0 {
		m.startEphemeralTimeout(s.ID, s.ParentSessionID, time.Duration(*ephemeralTimeoutSeconds)*time.Second)
	}

	return s, nil
}

// startEphemeralTimeout launches a goroutine that waits for the given duration,
// then checks if the session is still active (working/idle) and archives it if so.
// If the session has a parent, a notification is sent to the parent via SendKeys.
// The returned cancel func (also stored in m.ephemeralCancels) can be used to
// terminate the goroutine early when the session is manually stopped/deleted.
func (m *Manager) startEphemeralTimeout(sessionID, parentSessionID string, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	m.ephemeralCancelsMu.Lock()
	m.ephemeralCancels[sessionID] = cancel
	m.ephemeralCancelsMu.Unlock()

	go func() {
		defer func() {
			cancel()
			m.ephemeralCancelsMu.Lock()
			delete(m.ephemeralCancels, sessionID)
			m.ephemeralCancelsMu.Unlock()
		}()

		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				// Timer fired normally — proceed to archive
			} else {
				// Cancelled externally (manual stop/delete/archive)
				m.logger.Info("ephemeral timeout goroutine cancelled",
					"sessionId", sessionID)
				return
			}
		}

		s, err := m.Get(sessionID)
		if err != nil {
			m.logger.Warn("ephemeral timeout: session not found", "sessionId", sessionID, "error", err)
			return
		}

		// Only archive if still in an active state
		if s.Status != StatusWorking && s.Status != StatusIdle {
			m.logger.Info("ephemeral timeout: session already in terminal state, skipping",
				"sessionId", sessionID, "status", s.Status)
			return
		}

		// Stop the stream process and archive (atomically update status + holder_pid in one query)
		m.stopStreamProcess(sessionID)
		if err := m.UpdateStatusWithHolderPIDClear(sessionID, StatusArchived); err != nil {
			m.logger.Error("ephemeral timeout: failed to archive session",
				"sessionId", sessionID, "error", err)
			return
		}

		m.logger.Info("ephemeral session archived due to timeout",
			"sessionId", sessionID, "timeout", timeout)

		// Notify parent session if exists
		if parentSessionID != "" {
			msg := fmt.Sprintf("[system] Ephemeral session %s has been archived due to timeout (%s).", sessionID, timeout)
			if err := m.SendKeys(parentSessionID, msg); err != nil {
				m.logger.Warn("ephemeral timeout: failed to notify parent session",
					"sessionId", sessionID, "parentSessionId", parentSessionID, "error", err)
			}
		}
	}()
}

// cancelEphemeralTimeout cancels the ephemeral timeout goroutine for the given session,
// if one is active. This should be called when a session is manually stopped or deleted.
func (m *Manager) cancelEphemeralTimeout(sessionID string) {
	m.ephemeralCancelsMu.Lock()
	cancel, ok := m.ephemeralCancels[sessionID]
	m.ephemeralCancelsMu.Unlock()
	if ok {
		cancel()
	}
}

// UpdateStatusWithHolderPIDClear updates the session status and clears holder_pid atomically.
func (m *Manager) UpdateStatusWithHolderPIDClear(id string, status Status) error {
	_, err := m.db.Exec("UPDATE sessions SET status = ?, holder_pid = 0, updated_at = ? WHERE id = ?", string(status), time.Now(), id)
	if err == nil && m.onStatusChange != nil {
		m.onStatusChange(id, status, "")
	}
	return err
}

func (m *Manager) List() ([]Session, error) {
	rows, err := m.db.Query(
		`SELECT id, name, project_path, initial_prompt, system_prompt, status, type, provider, cli_session_id, model, parent_session_id, role_template, current_task, goal, goals, last_error, holder_pid, clear_offset, conversation_started, ephemeral_timeout_seconds, completion_report, created_at, updated_at
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
		var prompt, systemPrompt, parentSessionID, roleTemplate, currentTask, goal, goalsJSON, lastError, completionReport sql.NullString
		var conversationStartedInt int
		var ephemeralTimeoutSeconds sql.NullInt64
		if err := rows.Scan(&s.ID, &s.Name, &s.ProjectPath, &prompt, &systemPrompt, &status, &sessionType, &providerStr, &s.CliSessionID, &s.Model, &parentSessionID, &roleTemplate, &currentTask, &goal, &goalsJSON, &lastError, &s.HolderPID, &s.ClearOffset, &conversationStartedInt, &ephemeralTimeoutSeconds, &completionReport, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		s.ConversationStarted = conversationStartedInt != 0
		s.Status = Status(status)
		s.Type = SessionType(sessionType)
		s.Provider = ProviderName(providerStr)
		if s.Provider == "" {
			s.Provider = ProviderClaude
		}
		if prompt.Valid {
			s.InitialPrompt = prompt.String
		}
		if systemPrompt.Valid {
			s.SystemPrompt = systemPrompt.String
		}
		if parentSessionID.Valid {
			s.ParentSessionID = parentSessionID.String
		}
		if roleTemplate.Valid {
			s.RoleTemplate = roleTemplate.String
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
		if lastError.Valid {
			s.LastError = lastError.String
		}
		if ephemeralTimeoutSeconds.Valid {
			v := int(ephemeralTimeoutSeconds.Int64)
			s.EphemeralTimeoutSeconds = &v
		}
		if completionReport.Valid {
			v := completionReport.String
			s.CompletionReport = &v
		}

		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}


func (m *Manager) Get(id string) (*Session, error) {
	var s Session
	var status string
	var sessionType string
	var providerStr string
	var prompt, systemPrompt, parentSessionID, roleTemplate, currentTask, goal, goalsJSON, lastError, completionReport sql.NullString
	var conversationStartedInt int
	var ephemeralTimeoutSeconds sql.NullInt64
	err := m.db.QueryRow(
		`SELECT id, name, project_path, initial_prompt, system_prompt, status, type, provider, cli_session_id, model, parent_session_id, role_template, current_task, goal, goals, last_error, holder_pid, clear_offset, conversation_started, ephemeral_timeout_seconds, completion_report, created_at, updated_at
		 FROM sessions WHERE id = ?`, id,
	).Scan(&s.ID, &s.Name, &s.ProjectPath, &prompt, &systemPrompt, &status, &sessionType, &providerStr, &s.CliSessionID, &s.Model, &parentSessionID, &roleTemplate, &currentTask, &goal, &goalsJSON, &lastError, &s.HolderPID, &s.ClearOffset, &conversationStartedInt, &ephemeralTimeoutSeconds, &completionReport, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("query session: %w", err)
	}
	s.ConversationStarted = conversationStartedInt != 0
	s.Status = Status(status)
	s.Type = SessionType(sessionType)
	s.Provider = ProviderName(providerStr)
	if s.Provider == "" {
		s.Provider = ProviderClaude
	}
	if prompt.Valid {
		s.InitialPrompt = prompt.String
	}
	if systemPrompt.Valid {
		s.SystemPrompt = systemPrompt.String
	}
	if parentSessionID.Valid {
		s.ParentSessionID = parentSessionID.String
	}
	if roleTemplate.Valid {
		s.RoleTemplate = roleTemplate.String
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
	if lastError.Valid {
		s.LastError = lastError.String
	}
	if ephemeralTimeoutSeconds.Valid {
		v := int(ephemeralTimeoutSeconds.Int64)
		s.EphemeralTimeoutSeconds = &v
	}
	if completionReport.Valid {
		v := completionReport.String
		s.CompletionReport = &v
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
		Provider:     src.Provider,
		Model:        src.Model,
		SystemPrompt: src.SystemPrompt,
		RoleTemplate: src.RoleTemplate,
	})
}

// forkSystemReminder is appended to the system prompt when forking a session to
// signal that the conversation is a fork and Claude should focus on the new direction.
const forkSystemReminder = "この会話は親セッションからフォークされました。親の作業内容は参考情報として利用できますが、これから指示する新しい内容に集中してください。"

// Fork creates a new session by forking an existing session's conversation history.
// It copies the stream JSONL file and starts a new CLI process with --resume --fork-session.
// initialPrompt is required and must be non-empty.
func (m *Manager) Fork(id string, preserveContext bool, initialPrompt string) (*Session, error) {
	if strings.TrimSpace(initialPrompt) == "" {
		return nil, fmt.Errorf("initialPrompt is required for fork")
	}
	src, err := m.Get(id)
	if err != nil {
		return nil, err
	}

	provider := m.getProvider(src.Provider)

	newID, err := newSessionID()
	if err != nil {
		return nil, fmt.Errorf("generate session id: %w", err)
	}

	var cliSessionID string
	if preserveContext {
		// Read the CLI session ID from the source session's stream file
		cliSessionID = src.CliSessionID
		if cliSessionID == "" {
			cliSessionID = ReadCLISessionID(id, provider)
		}
		if cliSessionID == "" {
			return nil, fmt.Errorf("cannot fork: source session has no CLI session ID")
		}

		// Copy stream JSONL file from source to new session
		streamsDir, err := db.StreamsDir()
		if err != nil {
			return nil, fmt.Errorf("get streams dir: %w", err)
		}
		srcPath := filepath.Join(streamsDir, id+".jsonl")
		dstPath := filepath.Join(streamsDir, newID+".jsonl")
		if err := copyFile(srcPath, dstPath); err != nil {
			return nil, fmt.Errorf("copy stream file: %w", err)
		}
	}

	// Generate MCP config for new session
	mcpConfigPath, err := provider.SetupMCP(newID, m.apiPort)
	if err != nil {
		return nil, fmt.Errorf("write mcp config: %w", err)
	}

	effectiveSystemPrompt := m.buildEffectiveSystemPrompt(src.SystemPrompt)
	effectiveSystemPrompt += "\n\n" + forkSystemReminder

	// For Codex, pass initialPrompt as command-line argument via StreamOpts.
	// For Claude, the prompt is sent via stdin after the holder starts.
	streamOptsInitialPrompt := ""
	if src.Provider == ProviderCodex {
		streamOptsInitialPrompt = initialPrompt
	}

	// Start new CLI process via holder
	sp, err := StartHolderStreamProcess(StreamOpts{
		SessionID:     newID,
		ProjectPath:   src.ProjectPath,
		MCPConfigPath: mcpConfigPath,
		SystemPrompt:  effectiveSystemPrompt,
		Resume:        preserveContext,
		ForkSession:   preserveContext,
		CLISessionID:  cliSessionID,
		Model:         src.Model,
		APIPort:       m.apiPort,
		InitialPrompt: streamOptsInitialPrompt,
	}, provider)
	if err != nil {
		return nil, fmt.Errorf("start stream process: %w", err)
	}

	// For non-Codex providers (Claude), send the initial prompt via stdin.
	if src.Provider != ProviderCodex {
		if err := sp.Send(initialPrompt); err != nil {
			return nil, fmt.Errorf("send initial prompt: %w", err)
		}
	}

	m.wireSessionIDCallback(newID, sp)
	m.streamMu.Lock()
	m.streamProcesses[newID] = sp
	m.streamMu.Unlock()

	now := time.Now()
	newName := src.Name + " (fork)"
	s := &Session{
		ID:              newID,
		Name:            newName,
		ProjectPath:     src.ProjectPath,
		InitialPrompt:   initialPrompt,
		SystemPrompt:    src.SystemPrompt,
		Status:          StatusIdle,
		Type:            TypeWorker,
		Provider:        src.Provider,
		Model:           src.Model,
		ParentSessionID: id,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if _, err := m.db.Exec(
		`INSERT INTO sessions (id, name, project_path, initial_prompt, tmux_session, status, type, output_mode, provider, model, system_prompt, parent_session_id, holder_pid, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, s.ProjectPath, s.InitialPrompt, "", string(s.Status), string(s.Type), "stream", string(s.Provider), s.Model, s.SystemPrompt, s.ParentSessionID, sp.HolderPID(), s.CreatedAt, s.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("insert session: %w", err)
	}

	return s, nil
}

// copyFile copies the contents of src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no stream file to copy is OK
		}
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := out.ReadFrom(in); err != nil {
		return err
	}
	return nil
}

func (m *Manager) Stop(id string) error {
	_, err := m.Get(id)
	if err != nil {
		return err
	}

	// Cancel ephemeral timeout goroutine if active
	m.cancelEphemeralTimeout(id)

	// Stop stream process if exists
	m.stopStreamProcess(id)

	_, err = m.db.Exec("UPDATE sessions SET status = ?, holder_pid = 0, updated_at = ? WHERE id = ?", string(StatusPaused), time.Now(), id)
	return err
}

func (m *Manager) Delete(id string) error {
	_, err := m.Get(id)
	if err != nil {
		return err
	}

	// Mark the session as being deleted BEFORE killing processes.
	// This prevents RecoverStreamProcesses and SendKeysWithImages from spawning
	// a new holder in the window between killing the old holder and removing the DB record.
	m.deletingMu.Lock()
	m.deletingSet[id] = struct{}{}
	m.deletingMu.Unlock()
	defer func() {
		m.deletingMu.Lock()
		delete(m.deletingSet, id)
		m.deletingMu.Unlock()
	}()

	// Cancel ephemeral timeout goroutine if active
	m.cancelEphemeralTimeout(id)

	// Stop stream process if exists
	m.stopStreamProcess(id)

	// Kill holder process via DB holder_pid even if not in streamProcesses map
	// (e.g. after server restart, the map may not have the entry)
	//
	// ⚠️ CRITICAL: holder の graceful shutdown は SIGTERM でのみ動作する。
	// holder は SIGTERM を受けると stdin.Close() を実行し、子プロセスの claude CLI が
	// stdin EOF で正常終了する。SIGKILL を使うと stdin.Close() が実行されず、
	// claude CLI が孤児化して external process として残留する。
	// この問題は #441, #472, #502, #529, #569, #574 と6回再発している。
	// 修正時は holder.go の SIGTERM ハンドラとの整合性を必ず確認すること。
	//
	// プロセスグループ単位で kill することで、holder が SIGKILL されても
	// 子プロセス (claude CLI) も確実に終了する。holder は Setsid: true で起動されるため
	// holder の PID = PGID になっており、-PID でグループ全体を kill できる。
	// See: https://github.com/myuon/agmux/issues/574
	var holderPID int
	if err := m.db.QueryRow("SELECT holder_pid FROM sessions WHERE id = ?", id).Scan(&holderPID); err == nil {
		if holderPID > 0 && IsHolderAlive(holderPID) {
			// Send SIGTERM to the entire process group so claude CLI also receives the signal
			syscall.Kill(-holderPID, syscall.SIGTERM)
			// Wait up to 3 seconds for graceful termination
			waitForProcessExit(holderPID, 3*time.Second, 100*time.Millisecond)
			if !IsHolderAlive(holderPID) {
				m.logger.Info("terminated process group via SIGTERM", "sessionId", id, "holderPid", holderPID)
			} else {
				// Fallback: kill the entire process group with SIGKILL
				syscall.Kill(-holderPID, syscall.SIGKILL)
				waitForProcessExit(holderPID, 3*time.Second, 100*time.Millisecond)
				m.logger.Info("killed process group via SIGKILL (SIGTERM fallback)", "sessionId", id, "holderPid", holderPID)
			}
		}
	}

	// Detach child sessions so they become top-level instead of orphaned
	if _, err := m.db.Exec("UPDATE sessions SET parent_session_id = NULL WHERE parent_session_id = ?", id); err != nil {
		return fmt.Errorf("detach child sessions: %w", err)
	}

	_, err = m.db.Exec("DELETE FROM sessions WHERE id = ?", id)
	if err != nil {
		return err
	}
	// Drop background task rows for the deleted session.
	if m.backgroundTasks != nil {
		if err := m.backgroundTasks.ClearSession(id); err != nil {
			m.logger.Warn("failed to clear background tasks on delete", "sessionId", id, "error", err)
		}
	}
	return nil
}

// wireSessionIDCallback sets up the onSessionID callback on a stream process
// so that when the CLI session ID is captured, it gets persisted to the DB.
// It also wires periodic notification for background task tracking.
func (m *Manager) wireSessionIDCallback(sessionID string, sp *HolderStreamProcess) {
	m.wireNotifyCallback(sp)
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
	// Wrap onNewLines so we can update the background_tasks DB table per line.
	bgStore := m.backgroundTasks
	upstream := m.onNewLines
	sp.SetOnNewLines(func(sid string, newLines []string, total int) {
		if bgStore != nil {
			for _, line := range newLines {
				if err := bgStore.ApplyLine(sid, []byte(line)); err != nil {
					m.logger.Warn("background task apply line failed", "sessionId", sid, "error", err)
				}
			}
		}
		if upstream != nil {
			upstream(sid, newLines, total)
		}
	})
	if upstream == nil {
		// Background task DB updates still run via the wrapper above; only the
		// upstream broadcast (e.g. WebSocket relay) is missing.
		m.logger.Info("onNewLines upstream is nil; background task DB updates will still run, but no upstream broadcast is wired", "sessionId", sessionID)
	} else {
		m.logger.Info("onNewLines callback set for session", "sessionId", sessionID)
	}
	sp.SetOnTurnComplete(func(sid string) {
		m.logger.Info("turn completed (result event detected), setting status to idle",
			"sessionId", sid,
		)
		if err := m.UpdateStatus(sid, StatusIdle); err != nil {
			m.logger.Error("failed to update status after turn complete", "sessionId", sid, "error", err)
		}
		// Mark that at least one full turn has completed so auto-recovery can safely use --resume.
		if err := m.MarkConversationStarted(sid); err != nil {
			m.logger.Error("failed to mark conversation started", "sessionId", sid, "error", err)
		}
	})
	sp.SetOnHolderRestart(func(sid string, newPID int) {
		m.logger.Info("holder restarted (codex resume), updating holder_pid", "sessionId", sid, "newPid", newPID)
		m.updateHolderPID(sid, newPID)
	})
	sp.SetOnProcessExit(func(sid string, exitErr error) {
		// For Codex provider, exit code 0 is normal (exec finishes after each prompt).
		// Keep the session running and the stream process in the map so that
		// sendCodex can restart it with "codex exec resume" on the next message.
		if sp.ProviderName() == ProviderCodex && exitErr == nil {
			m.logger.Info("codex process exited normally (exit code 0), keeping session running for resume",
				"sessionId", sid,
			)
			return
		}

		// Exit code 0 means the CLI process exited normally (e.g. turn complete).
		// Transition to idle so the session remains usable; SendKeys will
		// auto-recover by spawning a new holder when the next message arrives.
		if exitErr == nil {
			m.logger.Info("CLI process exited normally (exit code 0), setting status to idle",
				"sessionId", sid,
			)
			if err := m.UpdateStatus(sid, StatusIdle); err != nil {
				m.logger.Error("failed to update status after normal exit", "sessionId", sid, "error", err)
			}
			// Clean up the stream process from the map (holder has exited)
			m.streamMu.Lock()
			delete(m.streamProcesses, sid)
			m.streamMu.Unlock()
			return
		}

		errMsg := exitErr.Error()
		m.logger.Warn("process exited unexpectedly, updating status to exited",
			"sessionId", sid,
			"exitError", errMsg,
		)
		if err := m.UpdateStatusWithError(sid, StatusExited, errMsg); err != nil {
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

// SetNotifyInterval sets the periodic notification interval for background task tracking.
func (m *Manager) SetNotifyInterval(d time.Duration) {
	m.notifyInterval = d
}

// wireNotifyCallback configures the periodic notification on a stream process.
func (m *Manager) wireNotifyCallback(sp *HolderStreamProcess) {
	if m.notifyInterval > 0 {
		sp.SetNotifyInterval(m.notifyInterval)
		sp.SetSendKeysFunc(m.SendKeys)
	}
}

// StopAllStreamProcesses disconnects from all holder processes on server shutdown.
// Unlike the old behavior, holders are NOT killed -- they survive server restart.
// Only the server's socket connections are closed.
func (m *Manager) StopAllStreamProcesses() {
	m.streamMu.Lock()
	processes := make(map[string]*HolderStreamProcess, len(m.streamProcesses))
	maps.Copy(processes, m.streamProcesses)
	m.streamProcesses = make(map[string]*HolderStreamProcess)
	m.streamMu.Unlock()

	for id, sp := range processes {
		// Just close the socket connection; don't kill the holder
		m.logger.Info("disconnecting from holder (holder stays alive)", "sessionId", id, "holderPid", sp.HolderPID())
		sp.conn.Close()
	}
}

// Clear resets the session context by recording a clear offset in the DB
// (the current JSONL file size) instead of truncating the file.
// This preserves the JSONL data on disk while hiding it from the UI.
func (m *Manager) Clear(id string) error {
	_, err := m.Get(id)
	if err != nil {
		return err
	}

	// Get current JSONL file size to use as clear offset
	var clearOffset int64
	streamsDir, err := db.StreamsDir()
	if err == nil {
		streamPath := filepath.Join(streamsDir, id+".jsonl")
		if info, statErr := os.Stat(streamPath); statErr == nil {
			clearOffset = info.Size()
		}
	}

	// Clear in-memory lines in the stream process
	m.streamMu.Lock()
	sp, ok := m.streamProcesses[id]
	m.streamMu.Unlock()
	if ok {
		sp.ClearLines()
	}

	// Store clear_offset and reset task/goal but keep the existing process and CLI session running.
	_, err = m.db.Exec("UPDATE sessions SET last_error = NULL, current_task = NULL, goal = NULL, goals = '[]', clear_offset = ?, updated_at = ? WHERE id = ?", clearOffset, time.Now(), id)
	if err != nil {
		return err
	}
	// Drop background task rows tied to the cleared portion of the stream.
	if m.backgroundTasks != nil {
		if err := m.backgroundTasks.ClearSession(id); err != nil {
			m.logger.Warn("failed to clear background tasks", "sessionId", id, "error", err)
		}
	}
	return nil
}

func (m *Manager) SendKeys(id string, text string) error {
	return m.SendKeysWithImages(id, text, nil)
}

func (m *Manager) SendKeysWithImages(id string, text string, images []ImageData) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}

	// Reject if Delete() is in progress for this session.
	m.deletingMu.Lock()
	_, isDeleting := m.deletingSet[id]
	m.deletingMu.Unlock()
	if isDeleting {
		return fmt.Errorf("session %s is being deleted", id)
	}

	// Hold spawnMu for the entire check-and-spawn block to prevent duplicate holders
	// when concurrent goroutines both see no active stream process and both try to spawn.
	m.spawnMu.Lock()
	defer m.spawnMu.Unlock()

	// Re-check deletingSet after acquiring spawnMu to close the TOCTOU window:
	// Delete() may have set the session as deleting between the first check above
	// and the acquisition of spawnMu.
	m.deletingMu.Lock()
	_, isDeleting = m.deletingSet[id]
	m.deletingMu.Unlock()
	if isDeleting {
		return fmt.Errorf("session %s is being deleted", id)
	}

	m.streamMu.Lock()
	sp, ok := m.streamProcesses[id]
	m.streamMu.Unlock()
	if !ok {
		// Auto-recover: restart stream process via holder after server restart
		provider := m.getProvider(s.Provider)
		mcpPath, _ := provider.SetupMCP(s.ID, m.apiPort)
		// Prefer DB-stored cli_session_id; fall back to JSONL file scan
		cliSessionID := s.CliSessionID
		if cliSessionID == "" {
			cliSessionID = ReadCLISessionID(s.ID, provider)
		}

		// Resume if the DB flag says a conversation was started, OR if we found
		// a CLI session ID in the JSONL stream (which proves a conversation exists
		// even when the DB flag wasn't updated — e.g. session created via CLI).
		canResume := s.ConversationStarted || cliSessionID != ""

		effectiveSP := m.buildEffectiveSystemPrompt(s.SystemPrompt)
		if s.Provider == ProviderCodex && cliSessionID != "" && canResume {
			// For Codex, start resume directly with the prompt as a positional arg.
			m.killStaleHolder(id)
			hsp, err := StartHolderStreamProcess(StreamOpts{
				SessionID:     s.ID,
				ProjectPath:   s.ProjectPath,
				MCPConfigPath: mcpPath,
				SystemPrompt:  effectiveSP,
				InitialPrompt: text,
				Resume:        true,
				CLISessionID:  cliSessionID,
				Model:         s.Model,
				APIPort:       m.apiPort,
				ClearOffset:   s.ClearOffset,
			}, provider)
			if err != nil {
				return fmt.Errorf("restart stream process: %w", err)
			}
			hsp.recordUserMessage(text)
			sp = hsp
			m.wireSessionIDCallback(id, sp)
			m.streamMu.Lock()
			m.streamProcesses[id] = sp
			m.streamMu.Unlock()
			m.updateHolderPID(id, hsp.HolderPID())
			return nil
		}

		m.killStaleHolder(id)
		hsp, err := StartHolderStreamProcess(StreamOpts{
			SessionID:     s.ID,
			ProjectPath:   s.ProjectPath,
			MCPConfigPath: mcpPath,
			SystemPrompt:  effectiveSP,
			Resume:        canResume,
			CLISessionID:  cliSessionID,
			Model:         s.Model,
			APIPort:       m.apiPort,
			ClearOffset:   s.ClearOffset,
		}, provider)
		if err != nil {
			return fmt.Errorf("restart stream process: %w", err)
		}
		sp = hsp
		m.wireSessionIDCallback(id, sp)
		m.streamMu.Lock()
		m.streamProcesses[id] = sp
		m.streamMu.Unlock()
		m.updateHolderPID(id, hsp.HolderPID())
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

	// Get clear_offset from DB
	s, err := m.Get(id)
	if err != nil {
		return nil, 0, err
	}

	// Fallback: read all lines from file, then truncate to limit
	allLines, err := m.readStreamFile(id, 0, s.ClearOffset)
	if err != nil {
		return nil, 0, err
	}
	total := len(allLines)
	if limit > 0 && len(allLines) > limit {
		allLines = allLines[len(allLines)-limit:]
	}
	return allLines, total, nil
}

func (m *Manager) readStreamFile(id string, limit int, clearOffset int64) ([]string, error) {
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

	// Seek past the clear offset so only post-clear content is read
	if clearOffset > 0 {
		if _, err := f.Seek(clearOffset, io.SeekStart); err != nil {
			return []string{}, nil
		}
	}

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

	// Get clear_offset from DB
	s, err := m.Get(id)
	if err != nil {
		return nil, 0, err
	}

	// Fallback: read from file
	lines, err := m.readStreamFileAfter(id, after, s.ClearOffset)
	if err != nil {
		return nil, 0, err
	}
	total := after + len(lines)
	return lines, total, nil
}

func (m *Manager) readStreamFileAfter(id string, after int, clearOffset int64) ([]string, error) {
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

	// Seek past the clear offset so only post-clear content is read
	if clearOffset > 0 {
		if _, err := f.Seek(clearOffset, io.SeekStart); err != nil {
			return []string{}, nil
		}
	}

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
	var prompt, systemPrompt, currentTask, goal, goalsJSON, lastError sql.NullString
	err := m.db.QueryRow(
		`SELECT id, name, project_path, initial_prompt, system_prompt, status, type, provider, cli_session_id, model, current_task, goal, goals, last_error, clear_offset, created_at, updated_at
		 FROM sessions WHERE type = ? LIMIT 1`, string(TypeController),
	).Scan(&s.ID, &s.Name, &s.ProjectPath, &prompt, &systemPrompt, &status, &sessionType, &providerStr, &s.CliSessionID, &s.Model, &currentTask, &goal, &goalsJSON, &lastError, &s.ClearOffset, &s.CreatedAt, &s.UpdatedAt)
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
	if systemPrompt.Valid {
		s.SystemPrompt = systemPrompt.String
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
	if lastError.Valid {
		s.LastError = lastError.String
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
	if existing != nil && (existing.Status == StatusWorking || existing.Status == StatusIdle || existing.Status == StatusWaitingInput) {
		// Already running, return existing
		return existing, nil
	}

	// If a stopped controller exists, delete it first
	if existing != nil {
		_ = m.Delete(existing.ID)
	}

	provider := m.getProvider(ProviderClaude)

	id, err := newSessionID()
	if err != nil {
		return nil, fmt.Errorf("generate session id: %w", err)
	}
	name := "controller"

	mcpConfigPath, err := provider.SetupMCP(id, m.apiPort)
	if err != nil {
		return nil, fmt.Errorf("write mcp config: %w", err)
	}

	// Start stream process via holder
	sp, err := StartHolderStreamProcess(StreamOpts{
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
		Status:      StatusIdle,
		Type:        TypeController,
		Provider:    ProviderClaude,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err = m.db.Exec(
		`INSERT INTO sessions (id, name, project_path, initial_prompt, tmux_session, status, type, output_mode, provider, holder_pid, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, s.ProjectPath, s.InitialPrompt, "", string(s.Status), string(s.Type), "stream", string(s.Provider), sp.HolderPID(), s.CreatedAt, s.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert controller session: %w", err)
	}

	return s, nil
}

func (m *Manager) UpdateStatus(id string, status Status) error {
	_, err := m.db.Exec("UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?", string(status), time.Now(), id)
	if err == nil && m.onStatusChange != nil {
		m.onStatusChange(id, status, "")
	}
	return err
}

// UpdateStatusWithError updates the session status and records the last error message.
func (m *Manager) UpdateStatusWithError(id string, status Status, lastError string) error {
	_, err := m.db.Exec("UPDATE sessions SET status = ?, last_error = ?, holder_pid = 0, updated_at = ? WHERE id = ?", string(status), lastError, time.Now(), id)
	if err == nil && m.onStatusChange != nil {
		m.onStatusChange(id, status, lastError)
	}
	return err
}

// UpdateCliSessionID persists the CLI-assigned session ID to the database.
func (m *Manager) UpdateCliSessionID(id string, cliSessionID string) error {
	_, err := m.db.Exec("UPDATE sessions SET cli_session_id = ?, updated_at = ? WHERE id = ?", cliSessionID, time.Now(), id)
	return err
}

// MarkConversationStarted sets conversation_started = 1 for the session.
// This is called when the first successful turn completes so that subsequent
// auto-recoveries know they can safely use --resume.
func (m *Manager) MarkConversationStarted(id string) error {
	_, err := m.db.Exec("UPDATE sessions SET conversation_started = 1, updated_at = ? WHERE id = ?", time.Now(), id)
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
	CompletedGoal   *GoalEntry // the goal that was just completed
	ParentGoal      *GoalEntry // the new top of stack (nil if empty)
	AutoArchived    bool       // true if session was auto-archived (ephemeral with empty goal stack)
	ParentSessionID string     // parent session ID for notification (set when auto-archived)
	SessionName     string     // name of the completed session
}

func (m *Manager) CompleteGoal(id string, report string) (*CompleteGoalResult, error) {
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

	tx, err := m.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	_, err = tx.Exec(
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

	// When goal stack is empty and session is ephemeral, auto-archive and save report
	if newGoals.Top() == nil && s.Type == TypeEphemeral {
		if report != "" {
			_, err = tx.Exec(
				"UPDATE sessions SET completion_report = ?, status = ?, updated_at = ? WHERE id = ?",
				report, string(StatusArchived), time.Now(), id,
			)
		} else {
			_, err = tx.Exec(
				"UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?",
				string(StatusArchived), time.Now(), id,
			)
		}
		if err != nil {
			return nil, err
		}
		result.AutoArchived = true
		result.ParentSessionID = s.ParentSessionID
		result.SessionName = s.Name
	} else if report != "" {
		// Save completion report even if not auto-archiving
		_, err = tx.Exec(
			"UPDATE sessions SET completion_report = ?, updated_at = ? WHERE id = ?",
			report, time.Now(), id,
		)
		if err != nil {
			return nil, err
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}

	return result, nil
}

// ListRecentProjects returns recently used project paths, ordered by last usage.
func (m *Manager) ListRecentProjects(limit int) ([]RecentProject, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := m.db.Query(
		`SELECT project_path, MAX(updated_at) AS last_used_at, COUNT(*) AS session_count
		 FROM sessions
		 WHERE type != 'controller'
		 GROUP BY project_path
		 ORDER BY last_used_at DESC
		 LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query recent projects: %w", err)
	}
	defer rows.Close()

	var projects []RecentProject
	for rows.Next() {
		var p RecentProject
		var lastUsed string
		if err := rows.Scan(&p.ProjectPath, &lastUsed, &p.SessionCount); err != nil {
			return nil, fmt.Errorf("scan recent project: %w", err)
		}
		p.LastUsedAt, _ = time.Parse(time.RFC3339, lastUsed)
		projects = append(projects, p)
	}
	return projects, rows.Err()
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

	// Only resume if a conversation turn has been completed before.
	// If conversation_started is false, resuming a non-existent conversation
	// causes "No conversation found with session ID" errors.
	canResume := s.ConversationStarted

	m.killStaleHolder(id)
	sp, err := StartHolderStreamProcess(StreamOpts{
		SessionID:     id,
		ProjectPath:   s.ProjectPath,
		MCPConfigPath: mcpConfigPath,
		SystemPrompt:  m.buildEffectiveSystemPrompt(s.SystemPrompt),
		Resume:        canResume,
		CLISessionID:  cliSessionID,
		Model:         s.Model,
		APIPort:       m.apiPort,
		ClearOffset:   s.ClearOffset,
	}, provider)
	if err != nil {
		return fmt.Errorf("start stream process: %w", err)
	}
	m.wireSessionIDCallback(id, sp)
	m.streamMu.Lock()
	m.streamProcesses[id] = sp
	m.streamMu.Unlock()
	m.updateHolderPID(id, sp.HolderPID())

	_, err = m.db.Exec("UPDATE sessions SET status = ?, last_error = NULL, updated_at = ? WHERE id = ?", string(StatusWorking), time.Now(), id)
	return err
}

// Restart stops the existing holder/claude processes and spawns a new one with
// --resume, preserving the conversation history (CLI session ID). Unlike Reconnect,
// Restart explicitly requires conversation_started to be true and returns an error
// if there is no CLI session ID to resume.
func (m *Manager) Restart(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}

	if !s.ConversationStarted {
		return fmt.Errorf("session %s has not started a conversation; use start instead of restart", id)
	}

	cliSessionID := s.CliSessionID
	if cliSessionID == "" {
		cliSessionID = ReadCLISessionID(id, m.getProvider(s.Provider))
	}
	if cliSessionID == "" {
		return fmt.Errorf("session %s has no CLI session ID; cannot resume conversation", id)
	}

	provider := m.getProvider(s.Provider)

	// Stop existing stream process
	m.stopStreamProcess(id)

	// Generate fresh MCP config
	mcpConfigPath, err := provider.SetupMCP(id, m.apiPort)
	if err != nil {
		return fmt.Errorf("write mcp config: %w", err)
	}

	// Kill any stale holder process
	m.killStaleHolder(id)

	// Reset status to idle before spawning new holder
	if _, err := m.db.Exec("UPDATE sessions SET status = ?, last_error = NULL, updated_at = ? WHERE id = ?", string(StatusIdle), time.Now(), id); err != nil {
		return fmt.Errorf("reset session status: %w", err)
	}

	sp, err := StartHolderStreamProcess(StreamOpts{
		SessionID:     id,
		ProjectPath:   s.ProjectPath,
		MCPConfigPath: mcpConfigPath,
		SystemPrompt:  m.buildEffectiveSystemPrompt(s.SystemPrompt),
		Resume:        true,
		CLISessionID:  cliSessionID,
		Model:         s.Model,
		APIPort:       m.apiPort,
		ClearOffset:   s.ClearOffset,
	}, provider)
	if err != nil {
		return fmt.Errorf("start stream process: %w", err)
	}
	m.wireSessionIDCallback(id, sp)
	m.streamMu.Lock()
	m.streamProcesses[id] = sp
	m.streamMu.Unlock()
	m.updateHolderPID(id, sp.HolderPID())

	_, err = m.db.Exec("UPDATE sessions SET status = ?, last_error = NULL, updated_at = ? WHERE id = ?", string(StatusWorking), time.Now(), id)
	return err
}

// waitForProcessExit polls until the given PID no longer exists or the timeout expires.
// This is needed because os.Process.Wait() only works for child processes on Unix,
// not for processes found via os.FindProcess() after a server restart.
func waitForProcessExit(pid int, timeout, interval time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !IsHolderAlive(pid) {
			return
		}
		time.Sleep(interval)
	}
}

// DismissTask writes a dismissed task_notification event to the JSONL stream
// for frontend record-keeping, and directly decrements the running task count
// in the stream process. The readLoop reads from the holder socket (claude stdout),
// not the JSONL file, so there is no double-decrement risk.
func (m *Manager) DismissTask(sessionID, taskID string) error {
	streamsDir, err := db.StreamsDir()
	if err != nil {
		return fmt.Errorf("get streams dir: %w", err)
	}

	type dismissEvent struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		TaskID  string `json:"task_id"`
		Status  string `json:"status"`
	}
	ev := dismissEvent{
		Type:    "system",
		Subtype: "task_notification",
		TaskID:  taskID,
		Status:  "dismissed",
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal dismiss event: %w", err)
	}

	path := filepath.Join(streamsDir, sessionID+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open stream file: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(string(line) + "\n"); err != nil {
		return fmt.Errorf("write dismiss event: %w", err)
	}

	// Decrement running tasks in stream process if present
	m.streamMu.Lock()
	sp, ok := m.streamProcesses[sessionID]
	m.streamMu.Unlock()
	if ok {
		sp.DecrementRunningTasks()
	}

	// Remove DB row so the background task list endpoint stops returning it.
	if m.backgroundTasks != nil {
		if err := m.backgroundTasks.Dismiss(sessionID, taskID); err != nil {
			m.logger.Warn("failed to dismiss background task in DB", "sessionId", sessionID, "taskId", taskID, "error", err)
		}
	}

	return nil
}
