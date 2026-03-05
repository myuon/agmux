package monitor

import (
	"context"
	"fmt"
	"log"
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
		log.Printf("status checker: list error: %v", err)
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

		// Check if tmux session still exists
		if !sc.tmux.HasSessionByFullName(s.TmuxSession) {
			if s.Status != session.StatusStopped {
				sc.logger.Info(fmt.Sprintf("%s (%s): tmux session gone, %s → stopped",
					s.Name, shortID, s.Status),
					slog.String("category", "status_checker"),
					slog.String("sessionId", s.ID),
				)
				if err := sc.sessions.UpdateStatus(s.ID, session.StatusStopped); err != nil {
					log.Printf("status checker: update %s (%s) error: %v", s.Name, shortID, err)
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
				log.Printf("status checker: update %s (%s) error: %v", s.Name, shortID, err)
				continue
			}
			s.Status = result.Status
			changed = true

			// Auto-resume paused sessions
			if result.Status == session.StatusPaused {
				sc.logger.Info(fmt.Sprintf("%s (%s): auto-resuming paused session",
					s.Name, shortID),
					slog.String("category", "status_checker"),
					slog.String("sessionId", s.ID),
				)
				if err := sc.sessions.SendKeys(s.ID, "作業を進めてください"); err != nil {
					log.Printf("status checker: auto-resume %s (%s) error: %v", s.Name, shortID, err)
				} else {
					_ = sc.sessions.UpdateStatus(s.ID, session.StatusWorking)
					s.Status = session.StatusWorking
				}
			}
		} else {
			sc.logger.Info(fmt.Sprintf("[%s] %s (%s): %s (%s)",
				s.OutputMode, s.Name, shortID,
				s.Status, result.Reason),
				slog.String("category", "status_checker"),
				slog.String("sessionId", s.ID),
			)
		}
	}

	// Log summary
	counts := map[session.Status]int{}
	for _, s := range sessions {
		counts[s.Status]++
	}
	sc.logger.Info(fmt.Sprintf("checked %d sessions: %d working, %d idle, %d paused, %d question, %d alignment, %d stopped",
		len(sessions),
		counts[session.StatusWorking],
		counts[session.StatusIdle],
		counts[session.StatusPaused],
		counts[session.StatusQuestionWaiting],
		counts[session.StatusAlignmentNeeded],
		counts[session.StatusStopped]),
		slog.String("category", "status_checker"),
	)

	if changed && sc.onUpdate != nil {
		sc.onUpdate(sessions)
	}
}
