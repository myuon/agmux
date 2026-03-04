package monitor

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/myuon/agmux/internal/session"
)

// Broadcaster is an interface for sending WebSocket messages.
type Broadcaster interface {
	Broadcast(msg interface{})
}

type StatusChecker struct {
	monitor     *Monitor
	sessions    *session.Manager
	onUpdate    func(sessions []session.Session)
	interval    time.Duration
	mu          sync.RWMutex
	outputCache map[string]string
}

func NewStatusChecker(monitor *Monitor, sessions *session.Manager, interval time.Duration) *StatusChecker {
	return &StatusChecker{
		monitor:     monitor,
		sessions:    sessions,
		interval:    interval,
		outputCache: make(map[string]string),
	}
}

func (sc *StatusChecker) SetOnUpdate(fn func(sessions []session.Session)) {
	sc.onUpdate = fn
}

func (sc *StatusChecker) GetCachedOutput(sessionID string) string {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.outputCache[sessionID]
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
		if s.Status == session.StatusStopped || s.Status == session.StatusDone {
			continue
		}

		newStatus, output, err := sc.monitor.CheckStatus(s)
		if err != nil {
			log.Printf("status checker: check %s error: %v", s.Name, err)
			continue
		}

		sc.mu.Lock()
		sc.outputCache[s.ID] = output
		sc.mu.Unlock()

		if newStatus != s.Status {
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
