package claudeauth

import (
	"sort"
	"sync"
	"time"
)

// State is the global "Claude needs a login" state, kept in memory only. After
// a daemon restart it is detected again by the next failing session.
type State struct {
	LoginRequired bool      `json:"loginRequired"`
	Since         time.Time `json:"since,omitempty"`
	Reason        string    `json:"reason,omitempty"`
	// StoppedSessions are sessions that stopped because of the lost login,
	// oldest first. They stay listed after the login is restored so the user
	// can resume them.
	StoppedSessions []string `json:"stoppedSessions"`
}

// Monitor tracks the login-required state. All methods are no-ops (or report
// "not required") while the experimental feature is off.
type Monitor struct {
	enabled func() bool
	now     func() time.Time

	mu       sync.Mutex
	required bool
	since    time.Time
	reason   string
	stopped  map[string]time.Time

	// onChange is called (outside the lock) whenever the state changes;
	// becameRequired is true only on the transition into login-required.
	onChange func(st State, becameRequired bool)
}

// NewMonitor creates a monitor. enabled reports whether the feature is on.
func NewMonitor(enabled func() bool) *Monitor {
	return &Monitor{enabled: enabled, now: time.Now, stopped: map[string]time.Time{}}
}

// SetOnChange registers the state change callback.
func (m *Monitor) SetOnChange(fn func(st State, becameRequired bool)) {
	m.mu.Lock()
	m.onChange = fn
	m.mu.Unlock()
}

func (m *Monitor) on() bool { return m.enabled == nil || m.enabled() }

// Report records that sessionID failed because the login is gone.
func (m *Monitor) Report(sessionID, reason string) {
	if !m.on() {
		return
	}
	m.mu.Lock()
	became := !m.required
	changed := became
	if became {
		m.required = true
		m.since = m.now()
		m.reason = reason
	}
	if sessionID != "" {
		if _, ok := m.stopped[sessionID]; !ok {
			m.stopped[sessionID] = m.now()
			changed = true
		}
	}
	st := m.stateLocked()
	cb := m.onChange
	m.mu.Unlock()
	if changed && cb != nil {
		cb(st, became)
	}
}

// Required reports whether Claude currently needs a login.
func (m *Monitor) Required() bool {
	if !m.on() {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.required
}

// Clear marks the login as restored. Stopped sessions stay listed.
func (m *Monitor) Clear() {
	m.mu.Lock()
	changed := m.required
	m.required = false
	m.since = time.Time{}
	m.reason = ""
	st := m.stateLocked()
	cb := m.onChange
	m.mu.Unlock()
	if changed && cb != nil {
		cb(st, false)
	}
}

// RemoveStopped drops a session from the stopped list (resumed or dismissed).
// An empty id clears the whole list.
func (m *Monitor) RemoveStopped(sessionID string) {
	m.mu.Lock()
	changed := false
	if sessionID == "" {
		changed = len(m.stopped) > 0
		m.stopped = map[string]time.Time{}
	} else if _, ok := m.stopped[sessionID]; ok {
		delete(m.stopped, sessionID)
		changed = true
	}
	st := m.stateLocked()
	cb := m.onChange
	m.mu.Unlock()
	if changed && cb != nil {
		cb(st, false)
	}
}

// IsStopped reports whether the session is in the stopped list.
func (m *Monitor) IsStopped(sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.stopped[sessionID]
	return ok
}

// State returns a snapshot. With the feature off it is always the zero state.
func (m *Monitor) State() State {
	if !m.on() {
		return State{StoppedSessions: []string{}}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stateLocked()
}

func (m *Monitor) stateLocked() State {
	ids := make([]string, 0, len(m.stopped))
	for id := range m.stopped {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		ti, tj := m.stopped[ids[i]], m.stopped[ids[j]]
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return ids[i] < ids[j]
	})
	return State{LoginRequired: m.required, Since: m.since, Reason: m.reason, StoppedSessions: ids}
}
