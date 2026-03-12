package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/myuon/agmux/internal/session"
	"github.com/myuon/agmux/internal/tmux"
)

// Broadcaster is an interface for sending WebSocket messages.
type Broadcaster interface {
	Broadcast(msg interface{})
}

type StatusChecker struct {
	monitor  *Monitor
	sessions *session.Manager
	tmux     *tmux.Client
	logger   *slog.Logger
	onUpdate func(sessions []session.Session)
	onNotify func(sessionId, sessionName, status, summary string)
	interval time.Duration
}

func NewStatusChecker(monitor *Monitor, sessions *session.Manager, tmuxClient *tmux.Client, interval time.Duration, logger *slog.Logger) *StatusChecker {
	return &StatusChecker{
		monitor:  monitor,
		sessions: sessions,
		tmux:     tmuxClient,
		interval: interval,
		logger:   logger,
	}
}

func (sc *StatusChecker) SetOnUpdate(fn func(sessions []session.Session)) {
	sc.onUpdate = fn
}

func (sc *StatusChecker) SetOnNotify(fn func(sessionId, sessionName, status, summary string)) {
	sc.onNotify = fn
}

func (sc *StatusChecker) Start(ctx context.Context) {
	ticker := time.NewTicker(sc.interval)
	defer ticker.Stop()

	// Run immediately once
	sc.check()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sc.check()
		}
	}
}

func (sc *StatusChecker) check() {
	sessions, err := sc.sessions.List()
	if err != nil {
		sc.logger.Error("status checker: list error", "error", err)
		return
	}

	changed := false
	for i := range sessions {
		s := &sessions[i]
		if s.Status == session.StatusStopped {
			continue
		}
		if s.OutputMode != session.OutputModeStream {
			continue
		}

		shortID := s.ID[:8]

		// Check if tmux session still exists (only for non-stream modes)
		if s.OutputMode != session.OutputModeStream && !sc.tmux.HasSessionByFullName(s.TmuxSession) {
			if s.Status != session.StatusStopped {
				sc.logger.Info(fmt.Sprintf("%s (%s): tmux session gone, %s → stopped",
					s.Name, shortID, s.Status),
					slog.String("category", "status_checker"),
					slog.String("sessionId", s.ID),
				)
				if err := sc.sessions.UpdateStatus(s.ID, session.StatusStopped); err != nil {
					sc.logger.Error("status checker: update error", "name", s.Name, "sessionId", s.ID, "error", err)
					continue
				}
				s.Status = session.StatusStopped
				changed = true
			}
			continue
		}

		// Mode-based status detection
		result := sc.monitor.CheckStatus(s)
		if result.Status != s.Status {
			sc.logger.Info(fmt.Sprintf("[%s] %s (%s): %s → %s (%s)",
				s.OutputMode, s.Name, shortID,
				s.Status, result.Status, result.Reason),
				slog.String("category", "status_checker"),
				slog.String("sessionId", s.ID),
			)
			if err := sc.sessions.UpdateStatus(s.ID, result.Status); err != nil {
				sc.logger.Error("status checker: update error", "name", s.Name, "sessionId", s.ID, "error", err)
				continue
			}
			s.Status = result.Status
			changed = true

			// Log and broadcast notification-worthy status changes
			if result.Status == session.StatusQuestionWaiting || result.Status == session.StatusAlignmentNeeded {
				summary := result.Summary
				if summary == "" {
					if result.Status == session.StatusQuestionWaiting {
						summary = "ユーザーの入力を待っています"
					} else {
						summary = "ユーザーとのアラインメントが必要です"
					}
				}
				sc.logger.Info(fmt.Sprintf("[notify] %s (%s): %s",
					s.Name, shortID, summary),
					slog.String("category", "status_checker"),
					slog.String("sessionId", s.ID),
				)
				if sc.onNotify != nil {
					sc.onNotify(s.ID, s.Name, string(result.Status), summary)
				}
			}
		}

		// Nudge: send "作業を進めてください" if last event is tool_use and 30+ min old
		if sc.monitor.ShouldNudge(s) {
			sc.logger.Info(fmt.Sprintf("%s (%s): nudging stalled session (tool_use + 30min)",
				s.Name, shortID),
				slog.String("category", "status_checker"),
				slog.String("sessionId", s.ID),
			)
			if err := sc.sessions.SendKeys(s.ID, "作業を進めてください"); err != nil {
				sc.logger.Error("status checker: nudge error", "name", s.Name, "sessionId", s.ID, "error", err)
			} else {
				_ = sc.sessions.UpdateStatus(s.ID, session.StatusWorking)
				s.Status = session.StatusWorking
				changed = true
			}
		}
	}

	if changed && sc.onUpdate != nil {
		sc.onUpdate(sessions)
	}
}
