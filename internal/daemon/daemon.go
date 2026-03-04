package daemon

import (
	"context"
	"database/sql"
	"log"
	"sync"
	"time"

	"github.com/myuon/agmux/internal/llm"
	"github.com/myuon/agmux/internal/session"
	"github.com/myuon/agmux/internal/tmux"
)

type Broadcaster interface {
	Broadcast(msg interface{})
}

type Daemon struct {
	sessions    *session.Manager
	tmux        *tmux.Client
	llm         *llm.Client
	db          *sql.DB
	broadcast   func(actionType string, detail interface{})
	interval    time.Duration
	mu          sync.Mutex
	lastOutputs map[string]string
}

func New(sessions *session.Manager, tmuxClient *tmux.Client, llmClient *llm.Client, db *sql.DB, interval time.Duration) *Daemon {
	return &Daemon{
		sessions:    sessions,
		tmux:        tmuxClient,
		llm:         llmClient,
		db:          db,
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

	log.Printf("Daemon started (interval: %s)", d.interval)

	for {
		select {
		case <-ctx.Done():
			log.Println("Daemon stopped")
			return
		case <-ticker.C:
			d.patrol()
		}
	}
}

func (d *Daemon) patrol() {
	sessions, err := d.sessions.List()
	if err != nil {
		log.Printf("daemon: list sessions error: %v", err)
		return
	}

	for _, s := range sessions {
		if s.Status != session.StatusRunning && s.Status != session.StatusWaiting && s.Status != session.StatusError {
			continue
		}

		output, err := d.tmux.CapturePane(s.TmuxSession, 100)
		if err != nil {
			log.Printf("daemon: capture %s error: %v", s.Name, err)
			continue
		}

		// Skip if output hasn't changed
		d.mu.Lock()
		if d.lastOutputs[s.ID] == output {
			d.mu.Unlock()
			continue
		}
		d.lastOutputs[s.ID] = output
		d.mu.Unlock()

		// Ask LLM to analyze
		prompt := llm.BuildAnalysisPrompt(s.Name, s.ProjectPath, string(s.Status), output)
		result, err := d.llm.Analyze(prompt)
		if err != nil {
			log.Printf("daemon: analyze %s error: %v", s.Name, err)
			continue
		}

		log.Printf("daemon: %s -> status=%s action=%s reason=%s", s.Name, result.Status, result.Action, result.Reason)

		// Update session status
		newStatus := session.Status(result.Status)
		if newStatus != s.Status {
			if err := d.sessions.UpdateStatus(s.ID, newStatus); err != nil {
				log.Printf("daemon: update status %s error: %v", s.Name, err)
			}
		}

		// Execute action
		d.executeAction(&s, result)
	}
}

func (d *Daemon) executeAction(s *session.Session, result *llm.AnalysisResult) {
	switch result.Action {
	case "approve", "retry":
		if result.SendText != "" {
			if err := d.sessions.SendKeys(s.ID, result.SendText); err != nil {
				log.Printf("daemon: send keys to %s error: %v", s.Name, err)
				return
			}
			log.Printf("daemon: sent '%s' to %s (%s)", result.SendText, s.Name, result.Action)
		}
	case "escalate":
		log.Printf("daemon: ESCALATION for %s: %s", s.Name, result.Reason)
	case "none":
		return
	default:
		log.Printf("daemon: unknown action '%s' for %s", result.Action, s.Name)
		return
	}

	// Record action
	d.recordAction(s.ID, result)
}

func (d *Daemon) recordAction(sessionID string, result *llm.AnalysisResult) {
	detail := result.Reason
	if result.SendText != "" {
		detail += " | sent: " + result.SendText
	}

	_, err := d.db.Exec(
		"INSERT INTO daemon_actions (session_id, action_type, detail) VALUES (?, ?, ?)",
		sessionID, result.Action, detail,
	)
	if err != nil {
		log.Printf("daemon: record action error: %v", err)
		return
	}

	if d.broadcast != nil {
		d.broadcast("action_log", map[string]interface{}{
			"sessionId":  sessionID,
			"actionType": result.Action,
			"detail":     detail,
			"timestamp":  time.Now(),
		})
	}
}
