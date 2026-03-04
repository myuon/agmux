package daemon

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/myuon/agmux/internal/llm"
	"github.com/myuon/agmux/internal/session"
	"github.com/myuon/agmux/internal/tmux"
)

type Daemon struct {
	sessions    *session.Manager
	tmux        *tmux.Client
	llm         *llm.Client
	db          *sql.DB
	logger      *slog.Logger
	broadcast   func(actionType string, detail interface{})
	interval    time.Duration
	mu          sync.Mutex
	lastOutputs map[string]string
}

func New(sessions *session.Manager, tmuxClient *tmux.Client, llmClient *llm.Client, db *sql.DB, interval time.Duration, logger *slog.Logger) *Daemon {
	return &Daemon{
		sessions:    sessions,
		tmux:        tmuxClient,
		llm:         llmClient,
		db:          db,
		logger:      logger.With("component", "daemon"),
		interval:    interval,
		lastOutputs: make(map[string]string),
	}
}

func (d *Daemon) SetBroadcast(fn func(actionType string, detail interface{})) {
	d.broadcast = fn
}

func (d *Daemon) Start(ctx context.Context) {
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	d.logger.Info("daemon started", "interval", d.interval.String())

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("daemon stopped")
			return
		case <-ticker.C:
			d.patrol()
		}
	}
}

// outputTail returns the last n lines of the output string.
func outputTail(output string, n int) string {
	lines := strings.Split(output, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (d *Daemon) patrol() {
	sessions, err := d.sessions.List()
	if err != nil {
		d.logger.Error("list sessions failed", "error", err)
		return
	}

	var targetCount, skipCount int
	for _, s := range sessions {
		if s.Status == session.StatusRunning || s.Status == session.StatusWaiting || s.Status == session.StatusError {
			targetCount++
		} else {
			skipCount++
			d.logger.Debug("skipping session", "session", s.Name, "status", string(s.Status))
		}
	}
	d.logger.Info("patrol started", "targetSessions", targetCount, "skippedSessions", skipCount)

	for _, s := range sessions {
		if s.Status != session.StatusRunning && s.Status != session.StatusWaiting && s.Status != session.StatusError {
			continue
		}

		log := d.logger.With("session", s.Name, "sessionId", s.ID)

		output, err := d.tmux.CapturePane(s.TmuxSession, 100)
		if err != nil {
			log.Error("capture pane failed", "error", err)
			continue
		}

		capturedLines := len(strings.Split(output, "\n"))
		log.Info("captured pane output", "lines", capturedLines)
		log.Debug("captured output tail", "tail", outputTail(output, 20))

		// Skip if output hasn't changed
		d.mu.Lock()
		if d.lastOutputs[s.ID] == output {
			d.mu.Unlock()
			log.Debug("output unchanged, skipping")
			continue
		}
		d.lastOutputs[s.ID] = output
		d.mu.Unlock()

		// Ask LLM to analyze
		previousStatus := string(s.Status)
		log.Info("analyzing session",
			"projectPath", s.ProjectPath,
			"previousStatus", previousStatus,
			"capturedLines", capturedLines,
		)
		prompt := llm.BuildAnalysisPrompt(s.Name, s.ProjectPath, string(s.Status), output)
		log.Debug("analysis prompt", "prompt", prompt)
		result, err := d.llm.Analyze(prompt)
		if err != nil {
			log.Error("analyze failed", "error", err)
			continue
		}

		log.Info("analysis result",
			"status", result.Status,
			"action", result.Action,
			"reason", result.Reason,
			"sendText", result.SendText,
		)

		// Update session status
		newStatus := session.Status(result.Status)
		if newStatus != s.Status {
			log.Info("status changed", "previousStatus", previousStatus, "newStatus", string(newStatus))
			if err := d.sessions.UpdateStatus(s.ID, newStatus); err != nil {
				log.Error("update status failed", "error", err)
			}
		}

		// Execute action
		d.executeAction(&s, result, outputTail(output, 20), previousStatus, string(newStatus))
	}
}

func (d *Daemon) executeAction(s *session.Session, result *llm.AnalysisResult, capturedOutputTail, previousStatus, newStatus string) {
	log := d.logger.With("session", s.Name, "sessionId", s.ID, "action", result.Action)

	switch result.Action {
	case "approve", "retry":
		if result.SendText != "" {
			if err := d.sessions.SendKeys(s.ID, result.SendText); err != nil {
				log.Error("send keys failed", "error", err)
				return
			}
			log.Info("sent text", "text", result.SendText)
		}
	case "escalate":
		log.Warn("escalation required", "reason", result.Reason)
	case "none":
		return
	default:
		log.Warn("unknown action", "action", result.Action)
		return
	}

	// Record action
	d.recordAction(s.ID, result, capturedOutputTail, previousStatus, newStatus)
}

func (d *Daemon) recordAction(sessionID string, result *llm.AnalysisResult, capturedOutputTail, previousStatus, newStatus string) {
	detail := result.Reason
	if result.SendText != "" {
		detail += " | sent: " + result.SendText
	}

	_, err := d.db.Exec(
		"INSERT INTO daemon_actions (session_id, action_type, detail, captured_output_tail, previous_status, new_status) VALUES (?, ?, ?, ?, ?, ?)",
		sessionID, result.Action, detail, capturedOutputTail, previousStatus, newStatus,
	)
	if err != nil {
		d.logger.Error("record action failed", "error", err)
		return
	}

	if d.broadcast != nil {
		d.broadcast("action_log", map[string]interface{}{
			"sessionId":          sessionID,
			"actionType":         result.Action,
			"detail":             detail,
			"capturedOutputTail": capturedOutputTail,
			"previousStatus":     previousStatus,
			"newStatus":          newStatus,
			"timestamp":          time.Now(),
		})
	}
}
