package session

import (
	"os"
	"path/filepath"
	"time"

	"github.com/myuon/agmux/internal/db"
)

// Automation auto-stop (#748).
//
// A session created by an Automation (AutomationID != "") is "armed" at
// creation. When its turn ends it is stopped through the normal Stop path
// (status paused, holder + CLI gone, stream file and cliSessionId kept, so it
// can be resumed). Without this every run left an idle holder + CLI behind.
//
// The armed state is in-memory only (Manager.autoStop):
//   - any user-originated SendKeys, Reconnect or Restart disarms the session,
//     so a session someone interacts with becomes a normal session;
//   - Stop / Delete disarm it as well.
//
// Turn end: onTurnComplete only fires when no background task/subagent is
// running (holder_stream.go), so a turn that ends with tasks still running
// does not stop the session; the later turn that follows the tasks' completion
// does. A bounded fallback (autoStopMaxRuntime) stops sessions whose tasks
// never finish, but only once they have produced no output for
// autoStopIdleWindow, so a long but healthy run is not killed mid-turn.
//
// Restart: running holders survive an agmux restart but the armed set does
// not. RecoverStreamProcesses re-arms only sessions that are still in their
// first turn (conversation_started = 0 and status working). An already idle
// automation session is never re-armed, because we cannot tell whether the user
// resumed it; leaving one idle is preferred over killing one in use.

const (
	// defaultAutoStopDelay gives the holder time to flush the final output
	// to the stream file before the process is stopped.
	defaultAutoStopDelay = 3 * time.Second
	// defaultAutoStopMaxRuntime bounds sessions whose background tasks never
	// finish (turn-complete would never fire).
	defaultAutoStopMaxRuntime = 2 * time.Hour
	// defaultAutoStopIdleWindow is how long a session must have been silent
	// before the fallback may stop it; a session with newer output is rechecked
	// after this window instead.
	defaultAutoStopIdleWindow = 30 * time.Minute
)

func (m *Manager) armAutoStop(id string) {
	maxRuntime := m.autoStopMaxRuntime
	if maxRuntime <= 0 {
		maxRuntime = defaultAutoStopMaxRuntime
	}
	m.autoStopMu.Lock()
	defer m.autoStopMu.Unlock()
	if m.autoStop == nil {
		m.autoStop = make(map[string]*time.Timer)
	}
	if old, ok := m.autoStop[id]; ok && old != nil {
		old.Stop()
	}
	m.autoStop[id] = time.AfterFunc(maxRuntime, func() { m.tryAutoStop(id, true) })
}

func (m *Manager) isAutoStopArmed(id string) bool {
	m.autoStopMu.Lock()
	defer m.autoStopMu.Unlock()
	_, ok := m.autoStop[id]
	return ok
}

// disarmAutoStop removes the session from auto-stop. Safe to call for any id.
func (m *Manager) disarmAutoStop(id string) {
	m.autoStopMu.Lock()
	defer m.autoStopMu.Unlock()
	if t, ok := m.autoStop[id]; ok {
		if t != nil {
			t.Stop()
		}
		delete(m.autoStop, id)
	}
}

// scheduleAutoStop is called when a turn ends. It is a no-op for sessions that
// are not armed (including every non-automation session).
func (m *Manager) scheduleAutoStop(id string) {
	if !m.isAutoStopArmed(id) {
		return
	}
	delay := m.autoStopDelay
	if delay <= 0 {
		delay = defaultAutoStopDelay
	}
	time.AfterFunc(delay, func() { m.tryAutoStop(id, false) })
}

// tryAutoStop stops the session if it is still armed and eligible. It holds
// spawnMu so that it serializes with SendKeys/Restart/model switching. When
// fallback is true (max-runtime timer) running background tasks are ignored and
// a working session may be stopped if it has been silent for the idle window;
// waiting_input is never stopped.
func (m *Manager) tryAutoStop(id string, fallback bool) {
	m.spawnMu.Lock()
	defer m.spawnMu.Unlock()

	if !m.isAutoStopArmed(id) {
		return
	}
	s, err := m.Get(id)
	if err != nil {
		m.disarmAutoStop(id)
		return
	}
	switch s.Status {
	case StatusIdle:
	case StatusWorking:
		if !fallback {
			return // a new turn started during the delay
		}
	case StatusWaitingInput:
		return // pending escalation / permission prompt
	default:
		// paused / exited / archived etc.: nothing left to stop.
		m.disarmAutoStop(id)
		return
	}
	if fallback && m.hasRecentOutput(id) {
		m.recheckAutoStopLater(id)
		return
	}
	if !fallback {
		m.streamMu.Lock()
		sp := m.streamProcesses[id]
		m.streamMu.Unlock()
		if sp != nil && sp.RunningTasks() > 0 {
			return
		}
	}
	m.logger.Info("auto-stopping automation session", "sessionId", id, "fallback", fallback)
	if err := m.Stop(id); err != nil {
		m.logger.Warn("auto-stop failed", "sessionId", id, "error", err)
	}
}

func (m *Manager) autoStopIdle() time.Duration {
	if m.autoStopIdleWindow > 0 {
		return m.autoStopIdleWindow
	}
	return defaultAutoStopIdleWindow
}

// hasRecentOutput reports whether the session's stream file was written within
// the idle window. An unreadable stream counts as no recent output.
func (m *Manager) hasRecentOutput(id string) bool {
	dir, err := db.StreamsDir()
	if err != nil {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, id+".jsonl"))
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < m.autoStopIdle()
}

// recheckAutoStopLater re-runs the fallback after the idle window, if the
// session is still armed.
func (m *Manager) recheckAutoStopLater(id string) {
	m.autoStopMu.Lock()
	defer m.autoStopMu.Unlock()
	if _, ok := m.autoStop[id]; !ok {
		return
	}
	m.autoStop[id] = time.AfterFunc(m.autoStopIdle(), func() { m.tryAutoStop(id, true) })
}
