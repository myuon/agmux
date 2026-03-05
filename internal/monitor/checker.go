package monitor

import (
	"context"
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

		shortID := s.ID[:8]

		// Check if tmux session still exists
		if !sc.tmux.HasSessionByFullName(s.TmuxSession) {
			if s.Status != session.StatusStopped {
				sc.logger.Info("status_check",
					slog.String("category", "status_checker"),
					slog.String("sessionId", s.ID),
					slog.String("sessionName", s.Name),
					slog.String("event", "tmux_session_gone"),
					slog.String("oldStatus", string(s.Status)),
					slog.String("newStatus", string(session.StatusStopped)),
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
		newStatus := sc.monitor.CheckStatus(s)
		if newStatus != s.Status {
			sc.logger.Info("status_check",
				slog.String("category", "status_checker"),
				slog.String("sessionId", s.ID),
				slog.String("sessionName", s.Name),
				slog.String("outputMode", string(s.OutputMode)),
				slog.String("event", "status_changed"),
				slog.String("oldStatus", string(s.Status)),
				slog.String("newStatus", string(newStatus)),
			)
			if err := sc.sessions.UpdateStatus(s.ID, newStatus); err != nil {
				log.Printf("status checker: update %s (%s) error: %v", s.Name, shortID, err)
				continue
			}
			s.Status = newStatus
			changed = true
		}
	}

	// Log summary
	counts := map[session.Status]int{}
	for _, s := range sessions {
		counts[s.Status]++
	}
	sc.logger.Info("status_check_summary",
		slog.String("category", "status_checker"),
		slog.Int("total", len(sessions)),
		slog.Int("working", counts[session.StatusWorking]),
		slog.Int("idle", counts[session.StatusIdle]),
		slog.Int("question_waiting", counts[session.StatusQuestionWaiting]),
		slog.Int("stopped", counts[session.StatusStopped]),
		slog.Bool("changed", changed),
	)

	if changed && sc.onUpdate != nil {
		sc.onUpdate(sessions)
	}
}
