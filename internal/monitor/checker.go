package monitor

import (
	"context"
	"log"
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
	onUpdate func(sessions []session.Session)
	interval time.Duration
}

func NewStatusChecker(monitor *Monitor, sessions *session.Manager, tmuxClient *tmux.Client, interval time.Duration) *StatusChecker {
	return &StatusChecker{
		monitor:  monitor,
		sessions: sessions,
		tmux:     tmuxClient,
		interval: interval,
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

		// Check if tmux session still exists
		if !sc.tmux.HasSessionByFullName(s.TmuxSession) {
			if s.Status != session.StatusStopped {
				log.Printf("status checker: %s (%s): tmux session gone, marking stopped", s.Name, s.ID[:8])
				if err := sc.sessions.UpdateStatus(s.ID, session.StatusStopped); err != nil {
					log.Printf("status checker: update %s error: %v", s.Name, err)
					continue
				}
				s.Status = session.StatusStopped
				changed = true
			}
			continue
		}

		// JSONL-based status detection
		newStatus := sc.monitor.CheckStatus(s)
		if newStatus != s.Status {
			log.Printf("status checker: %s (%s): %s -> %s", s.Name, s.ID[:8], s.Status, newStatus)
			if err := sc.sessions.UpdateStatus(s.ID, newStatus); err != nil {
				log.Printf("status checker: update %s error: %v", s.Name, err)
				continue
			}
			s.Status = newStatus
			changed = true
		}
	}

	if changed && sc.onUpdate != nil {
		sc.onUpdate(sessions)
	}
}
