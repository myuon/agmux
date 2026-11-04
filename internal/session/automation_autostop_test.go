package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/myuon/agmux/internal/db"
)

// autoStopTestManager returns a manager whose Create uses fake holders and a
// short auto-stop delay.
func autoStopTestManager(t *testing.T) (*Manager, *spawnRecorder) {
	t.Helper()
	m, _ := newSwitchModelTestManager(t)
	m.systemPrompt = "test"
	m.autoStopDelay = 50 * time.Millisecond
	rec := newSpawnRecorder(t, m)
	return m, rec
}

// createAutoSession creates a session through Manager.Create with a fake
// holder, as the Automation runner does.
func createAutoSession(t *testing.T, m *Manager, rec *spawnRecorder, automationID string) (string, *fakeHolder) {
	t.Helper()
	s, err := m.Create("auto", t.TempDir(), "do it", false, CreateOpts{AutomationID: automationID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rec.waitSpawn()
	return s.ID, rec.last()
}

func waitStatus(t *testing.T, m *Manager, id string, want Status) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s, err := m.Get(id); err == nil && s.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	s, _ := m.Get(id)
	t.Fatalf("status did not become %s (now %+v)", want, s)
}

func writeLine(t *testing.T, fh *fakeHolder, line string) {
	t.Helper()
	if _, err := fh.server.Write([]byte(line + "\n")); err != nil {
		t.Fatalf("write %s: %v", line, err)
	}
}

func TestAutoStop_AutomationSessionStopsAfterTurn(t *testing.T) {
	m, rec := autoStopTestManager(t)
	id, fh := createAutoSession(t, m, rec, "auto-1")
	fh.emitTurnComplete(t)
	waitStatus(t, m, id, StatusPaused)
	select {
	case <-fh.stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("holder was not told to stop")
	}
	if m.IsStreamProcessAlive(id) {
		t.Error("stream process should be gone")
	}
	var pid int
	if err := m.db.QueryRow("SELECT holder_pid FROM sessions WHERE id = ?", id).Scan(&pid); err != nil || pid != 0 {
		t.Errorf("holder_pid = %d (err %v), want 0", pid, err)
	}
}

func TestAutoStop_NonAutomationSessionUntouched(t *testing.T) {
	m, rec := autoStopTestManager(t)
	id, fh := createAutoSession(t, m, rec, "")
	fh.emitTurnComplete(t)
	waitStatus(t, m, id, StatusIdle)
	time.Sleep(400 * time.Millisecond)
	if s, _ := m.Get(id); s.Status != StatusIdle {
		t.Fatalf("status = %s, want idle", s.Status)
	}
	if !m.IsStreamProcessAlive(id) {
		t.Fatal("stream process of a non-automation session must stay")
	}
}

func TestAutoStop_WaitsForBackgroundTasks(t *testing.T) {
	m, rec := autoStopTestManager(t)
	id, fh := createAutoSession(t, m, rec, "auto-1")
	writeLine(t, fh, `{"type":"system","subtype":"task_started","task_id":"t1"}`)
	// First turn ends with a task still running: no turn-complete, no stop.
	writeLine(t, fh, `{"type":"result","subtype":"success"}`)
	time.Sleep(300 * time.Millisecond)
	if s, _ := m.Get(id); s.Status != StatusWorking {
		t.Fatalf("status = %s, want working", s.Status)
	}
	// Task finishes; the CLI runs a follow-up turn that ends.
	writeLine(t, fh, `{"type":"system","subtype":"task_notification","task_id":"t1"}`)
	writeLine(t, fh, `{"type":"result","subtype":"success"}`)
	waitStatus(t, m, id, StatusPaused)
}

func TestAutoStop_TaskStartedDuringDelayBlocksStop(t *testing.T) {
	m, rec := autoStopTestManager(t)
	m.autoStopDelay = 300 * time.Millisecond
	id, fh := createAutoSession(t, m, rec, "auto-1")
	fh.emitTurnComplete(t)
	waitStatus(t, m, id, StatusIdle)
	writeLine(t, fh, `{"type":"system","subtype":"task_started","task_id":"t9"}`)
	time.Sleep(600 * time.Millisecond)
	if s, _ := m.Get(id); s.Status == StatusPaused {
		t.Fatal("stopped while a background task was running")
	}
}

func TestAutoStop_UserMessageDisarms(t *testing.T) {
	m, rec := autoStopTestManager(t)
	id, fh := createAutoSession(t, m, rec, "auto-1")
	if err := m.SendKeys(id, "hello"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	fh.emitTurnComplete(t)
	waitStatus(t, m, id, StatusIdle)
	time.Sleep(400 * time.Millisecond)
	if s, _ := m.Get(id); s.Status != StatusIdle {
		t.Fatalf("status = %s, want idle (user interacted)", s.Status)
	}
}

func TestAutoStop_UserMessageDuringDelayCancelsStop(t *testing.T) {
	m, rec := autoStopTestManager(t)
	m.autoStopDelay = 300 * time.Millisecond
	id, fh := createAutoSession(t, m, rec, "auto-1")
	fh.emitTurnComplete(t)
	waitStatus(t, m, id, StatusIdle)
	if err := m.SendKeys(id, "one more thing"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	time.Sleep(600 * time.Millisecond)
	if s, _ := m.Get(id); s.Status == StatusPaused {
		t.Fatal("stopped although the user sent a message during the delay")
	}
}

func TestAutoStop_ReconnectAndRestartDisarm(t *testing.T) {
	m, rec := autoStopTestManager(t)
	id, fh := createAutoSession(t, m, rec, "auto-1")
	fh.emitTurnComplete(t)
	waitStatus(t, m, id, StatusPaused)
	if m.isAutoStopArmed(id) {
		t.Fatal("still armed after stop")
	}
	// Resume the stopped session (as /reconnect does) and check it stays
	// alive across its next turn end.
	if err := m.Reconnect(id); err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	rec.waitSpawn()
	if m.isAutoStopArmed(id) {
		t.Fatal("Reconnect must leave the session disarmed")
	}
	rec.last().emitTurnComplete(t)
	waitStatus(t, m, id, StatusIdle)
	time.Sleep(400 * time.Millisecond)
	if s, _ := m.Get(id); s.Status != StatusIdle {
		t.Fatalf("resumed session status = %s, want idle (not auto-stopped)", s.Status)
	}
}

func TestAutoStop_WaitingInputNotStopped(t *testing.T) {
	m, rec := autoStopTestManager(t)
	m.autoStopDelay = 200 * time.Millisecond
	id, fh := createAutoSession(t, m, rec, "auto-1")
	fh.emitTurnComplete(t)
	waitStatus(t, m, id, StatusIdle)
	if err := m.UpdateStatus(id, StatusWaitingInput); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if s, _ := m.Get(id); s.Status != StatusWaitingInput {
		t.Fatalf("status = %s, want waiting_input", s.Status)
	}
}

func TestAutoStop_FailedTurnStops(t *testing.T) {
	m, rec := autoStopTestManager(t)
	id, fh := createAutoSession(t, m, rec, "auto-1")
	writeLine(t, fh, `{"type":"result","subtype":"error_during_execution"}`)
	waitStatus(t, m, id, StatusPaused)
}

func TestAutoStop_AlreadyStoppedIsHarmless(t *testing.T) {
	m, rec := autoStopTestManager(t)
	id, _ := createAutoSession(t, m, rec, "auto-1")
	if err := m.Stop(id); err != nil {
		t.Fatal(err)
	}
	m.tryAutoStop(id, false) // not armed: no-op
	m.armAutoStop(id)        // armed, but the session is paused
	m.tryAutoStop(id, false)
	if m.isAutoStopArmed(id) {
		t.Error("should disarm when the session is no longer active")
	}
	m.armAutoStop("does-not-exist")
	m.tryAutoStop("does-not-exist", true)
	if m.isAutoStopArmed("does-not-exist") {
		t.Error("should disarm for a missing session")
	}
}

func TestAutoStop_MaxRuntimeFallback(t *testing.T) {
	m, rec := autoStopTestManager(t)
	m.autoStopMaxRuntime = 150 * time.Millisecond
	id, fh := createAutoSession(t, m, rec, "auto-1")
	// A task that never finishes: turn-complete never fires.
	writeLine(t, fh, `{"type":"system","subtype":"task_started","task_id":"stuck"}`)
	waitStatus(t, m, id, StatusPaused)
}

func writeStreamFile(t *testing.T, id string) string {
	t.Helper()
	dir, err := db.StreamsDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAutoStop_FallbackKeepsHealthyLongRun(t *testing.T) {
	m, rec := autoStopTestManager(t)
	m.autoStopMaxRuntime = 50 * time.Millisecond
	m.autoStopIdleWindow = time.Hour
	id, _ := createAutoSession(t, m, rec, "auto-long")
	writeStreamFile(t, id)
	time.Sleep(300 * time.Millisecond)
	s, err := m.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status == StatusPaused {
		t.Fatalf("session with recent output was stopped by the fallback")
	}
	if !m.isAutoStopArmed(id) {
		t.Fatalf("session should stay armed so its turn end still stops it")
	}
	m.disarmAutoStop(id)
}

func TestAutoStop_FallbackStopsAfterSilence(t *testing.T) {
	m, rec := autoStopTestManager(t)
	m.autoStopMaxRuntime = 50 * time.Millisecond
	m.autoStopIdleWindow = 200 * time.Millisecond
	id, _ := createAutoSession(t, m, rec, "auto-silent")
	writeStreamFile(t, id)
	// First check sees recent output and rechecks later; by then it is silent.
	waitStatus(t, m, id, StatusPaused)
}
